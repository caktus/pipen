// pipen-operator reconciles pens.pipen.dev/v1alpha1 Pen objects: it brings
// each pen's home PVC, kata pod and tailnet-exposed Service up and reports
// the result on Pen status. Cluster shape (image, node selector, storage
// class, tailscale annotations) comes from the pipen-operator-config
// ConfigMap in the operator's own namespace.
package main

import (
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/caktus/pipen/operator/api/v1alpha1"
	"github.com/caktus/pipen/operator/internal/config"
	"github.com/caktus/pipen/operator/internal/pen"
)

func main() {
	var opNamespace string
	if env := os.Getenv("POD_NAMESPACE"); env != "" {
		opNamespace = env
	} else {
		opNamespace = "pipen"
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		fatal(err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		fatal(err)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"}, // no metrics in the MVP
		HealthProbeBindAddress: ":8081",
		LeaderElection:         true,
		LeaderElectionID:       "pipen-operator.pipen.dev",
	})
	if err != nil {
		fatal(err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		fatal(err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		fatal(err)
	}

	r := &penOperatorReconciler{client: mgr.GetClient(), scheme: scheme, opNamespace: opNamespace}
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Pen{}).
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(allPensMapFunc(mgr.GetClient())),
			builder.WithPredicates(penConfigPredicate(opNamespace)),
		).
		Complete(r); err != nil {
		fatal(err)
	}

	ctx := ctrl.SetupSignalHandler()
	if err := mgr.Start(ctx); err != nil {
		fatal(err)
	}
}

// penOperatorReconciler reads the operator config once per pass (fresh from
// the manager's cache) and hands the pass to the pure pen reconciler.
type penOperatorReconciler struct {
	client      client.Client
	scheme      *runtime.Scheme
	opNamespace string
}

func (r *penOperatorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cm := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: r.opNamespace, Name: config.ConfigMapName}
	if err := r.client.Get(ctx, key, cm); err != nil {
		// Requeue with backoff: the operator cannot create pens without
		// its config, and the chart always ships it.
		return ctrl.Result{}, fmt.Errorf("read operator config %s/%s: %w", r.opNamespace, config.ConfigMapName, err)
	}
	cfg, err := config.FromData(cm.Data)
	if err != nil {
		// Bad config: requeue with backoff so a chart rollback recovers
		// the operator without a restart.
		return ctrl.Result{}, fmt.Errorf("parse operator config: %w", err)
	}
	rec := &pen.Reconciler{Client: r.client, Scheme: r.scheme, Config: cfg}
	return rec.Reconcile(ctx, req)
}

// allPensMapFunc maps a config change to every Pen, so an operator config
// change re-reconciles all pens. It captures the manager client (the map
// function's context carries no client in controller-runtime v0.21).
func allPensMapFunc(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		list := &v1alpha1.PenList{}
		if err := c.List(ctx, list); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(list.Items))
		for i := range list.Items {
			p := &list.Items[i]
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(p)})
		}
		return reqs
	}
}

func penConfigPredicate(opNamespace string) predicate.Predicate {
	isOpConfig := func(o client.Object) bool {
		cm, ok := o.(*corev1.ConfigMap)
		return ok && cm.Name == config.ConfigMapName && cm.Namespace == opNamespace
	}
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool { return isOpConfig(e.Object) },
		UpdateFunc: func(e event.UpdateEvent) bool { return isOpConfig(e.ObjectNew) },
		DeleteFunc: func(e event.DeleteEvent) bool { return false },
		GenericFunc: func(e event.GenericEvent) bool {
			return isOpConfig(e.Object)
		},
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "pipen-operator:", err)
	os.Exit(1)
}
