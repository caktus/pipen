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
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/caktus/pipen/operator/api/v1alpha1"
	"github.com/caktus/pipen/operator/internal/config"
	"github.com/caktus/pipen/operator/internal/pen"
)

func main() {
	ctrl.SetLogger(zap.New())
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

	cfg := ctrl.GetConfigOrDie()
	ctx := ctrl.SetupSignalHandler()
	d, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		fatal(err)
	}
	ok, err := crdGroupAvailable(ctx, d)
	if err != nil {
		fatal(err)
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "pipen-operator: the pens.pipen.dev CRD is not installed on this cluster. The pipen-operator chart installs it (templates/crd.yaml): helm install pipen-operator charts/pipen-operator")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"}, // no metrics in the MVP
		HealthProbeBindAddress: ":8081",
		LeaderElection:         true,
		LeaderElectionID:       "pipen-operator.pipen.dev",
		// Set explicitly: POD_NAMESPACE is the operator's namespace (the
		// downward API gives it to us), so we never depend on the
		// serviceaccount namespace file being present.
		LeaderElectionNamespace: opNamespace,
		// The cache holds Pens (cluster-wide, the only cluster-scoped
		// list/watch the operator has) plus the operator's own ConfigMap,
		// scoped to the operator namespace so it opens no cluster-wide
		// ConfigMap informer. Everything else is read directly (APIReader).
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				&corev1.ConfigMap{}: {
					Namespaces: map[string]cache.Config{opNamespace: {}},
				},
			},
		},
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

	r := &penOperatorReconciler{
		client:      mgr.GetClient(),
		reader:      mgr.GetAPIReader(),
		scheme:      scheme,
		opNamespace: opNamespace,
	}
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

	if err := mgr.Start(ctx); err != nil {
		fatal(err)
	}
}

// crdGroupAvailable reports whether the pens.pipen.dev API group is served
// by the cluster. A missing CRD names itself at startup instead of
// surfacing later as an opaque Pen informer failure.
func crdGroupAvailable(ctx context.Context, d discovery.DiscoveryInterface) (bool, error) {
	groups, err := d.ServerGroups()
	if err != nil {
		return false, err
	}
	want := v1alpha1.GroupVersion.Group
	for _, g := range groups.Groups {
		if g.Name == want {
			return true, nil
		}
	}
	return false, nil
}

// penOperatorReconciler reads the operator config once per pass (fresh via
// the API reader) and hands the pass to the pure pen reconciler.
type penOperatorReconciler struct {
	client      client.Client
	reader      client.Reader
	scheme      *runtime.Scheme
	opNamespace string
}

func (r *penOperatorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cm := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: r.opNamespace, Name: config.ConfigMapName}
	if err := r.reader.Get(ctx, key, cm); err != nil {
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
	rec := &pen.Reconciler{Client: r.client, Reader: r.reader, Scheme: r.scheme, Config: cfg}
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
