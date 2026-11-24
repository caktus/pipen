// Package pen reconciles Pen objects: it brings the home PVC, the kata pod
// and the tailnet-exposed Service up and reports the result on Pen status.
// Reconcile is create-or-leave: specs of existing objects are never
// mutated, and the PVC is never deleted by the operator.
package pen

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1alpha1 "github.com/caktus/pipen/operator/api/v1alpha1"
	"github.com/caktus/pipen/operator/internal/config"
)

const (
	// ownerLabel marks every object the operator creates for a pen.
	ownerLabel = "herdr.dev/env"
	// ownerLabelKey is the namespace label a provisioned dev namespace has.
	ownerLabelKey = "herdr.dev/owner"

	requeueCreating = 10 * time.Second
	requeuePending  = 30 * time.Second
	// requeueReady keeps a Ready pen periodically reconciled: status stays
	// truthful (hostname changes, node moves) and a deleted pod/Service is
	// recreated by the next pass (create-or-leave still applies).
	requeueReady = 30 * time.Second
)

// Reconciler reconciles Pens using the operator's config.
type Reconciler struct {
	// Client is the manager's cached client. It is used for the Pen itself
	// and for status writes only — every other read goes through Reader,
	// because a cached read opens a cluster-wide informer, which the
	// operator's RBAC (deliberately no cluster-wide list/watch beyond
	// Pens) cannot serve.
	Client client.Client
	// Reader is a direct API reader (the manager's APIReader) for
	// everything that is not the Pen: namespace, herdr-dev CM/Secret,
	// PVC, Service, Pod.
	Reader client.Reader
	Scheme *runtime.Scheme
	// Config is the operator config for this reconcile pass; main reads it
	// fresh (via the API reader) when the ConfigMap changes.
	Config config.Config
}

// Reconcile brings one Pen's objects up and updates its status.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	pen := &v1alpha1.Pen{}
	if err := r.Client.Get(ctx, req.NamespacedName, pen); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil // deleted: GC handles the objects
		}
		return ctrl.Result{}, err
	}
	return r.reconcilePen(ctx, pen)
}

func (r *Reconciler) reconcilePen(ctx context.Context, pen *v1alpha1.Pen) (ctrl.Result, error) {
	devUser, reason := r.namespaceProvisioned(ctx, pen.Namespace)
	if reason != "" {
		return r.fail(ctx, pen, reason)
	}
	devNS := pen.Namespace
	if !r.objectExists(ctx, types.NamespacedName{Name: "herdr-dev-" + devNS, Namespace: devNS}, &corev1.ConfigMap{}) {
		return r.pending(ctx, pen, fmt.Sprintf("waiting for ConfigMap herdr-dev-%s (admin bootstrap)", devNS))
	}
	if !r.objectExists(ctx, types.NamespacedName{Name: "herdr-dev-" + devNS, Namespace: devNS}, &corev1.Secret{}) {
		return r.pending(ctx, pen, fmt.Sprintf("waiting for Secret herdr-dev-%s (admin bootstrap)", devNS))
	}

	cfg := r.Config
	image := pen.Spec.Image
	if image == "" {
		image = cfg.Image
	}
	homeSize := orDefault(pen.Spec.HomeSize, cfg.HomeSize)
	cpu := orDefault(pen.Spec.CPU, cfg.CPU)
	memory := orDefault(pen.Spec.Memory, cfg.Memory)
	homeQty, err := resource.ParseQuantity(homeSize)
	if err != nil {
		return r.fail(ctx, pen, fmt.Sprintf("invalid homeSize %q: %v", homeSize, err))
	}
	cpuQty, err := resource.ParseQuantity(cpu)
	if err != nil {
		return r.fail(ctx, pen, fmt.Sprintf("invalid cpu %q: %v", cpu, err))
	}
	memQty, err := resource.ParseQuantity(memory)
	if err != nil {
		return r.fail(ctx, pen, fmt.Sprintf("invalid memory %q: %v", memory, err))
	}

	if err := r.ensurePVC(ctx, pen, homeQty, cfg); err != nil {
		return r.fail(ctx, pen, err.Error())
	}
	if err := r.ensureService(ctx, pen, cfg); err != nil {
		return r.fail(ctx, pen, err.Error())
	}
	res := corev1.ResourceList{
		corev1.ResourceCPU:    cpuQty,
		corev1.ResourceMemory: memQty,
	}
	if err := r.ensurePod(ctx, pen, image, devUser, res, cfg); err != nil {
		return r.fail(ctx, pen, err.Error())
	}

	pod := &corev1.Pod{}
	if err := r.Reader.Get(ctx, types.NamespacedName{Name: podName(pen.Name), Namespace: pen.Namespace}, pod); err != nil {
		return r.fail(ctx, pen, fmt.Sprintf("cannot read pod: %v", err))
	}
	if !podReady(pod) {
		msg := "pod " + podName(pen.Name) + " " + podProgress(pod)
		return r.status(ctx, pen, v1alpha1.PhaseCreating, false, "", "", msg)
	}
	hostname := r.serviceHostname(ctx, pen)
	if hostname == "" {
		return r.status(ctx, pen, v1alpha1.PhaseCreating, false, "", "",
			"pod ready, waiting for tailnet hostname on service "+pen.Name)
	}
	node := pod.Spec.NodeName // the node a scheduled pod landed on
	return r.status(ctx, pen, v1alpha1.PhaseReady, true, hostname, node, "ready")
}

// namespaceProvisioned returns the owner label value (the dev name the
// guest unix user is named after) and a failure reason when the namespace
// is not provisioned (missing, no owner label, or an empty owner label).
func (r *Reconciler) namespaceProvisioned(ctx context.Context, ns string) (string, string) {
	n := &corev1.Namespace{}
	if err := r.Reader.Get(ctx, types.NamespacedName{Name: ns}, n); err != nil {
		return "", "namespace not provisioned"
	}
	owner, ok := n.Labels[ownerLabelKey]
	if !ok {
		return "", "namespace not provisioned"
	}
	if owner == "" {
		return "", fmt.Sprintf("namespace %s has an empty herdr.dev/owner label", ns)
	}
	return owner, ""
}

func (r *Reconciler) objectExists(ctx context.Context, key types.NamespacedName, obj client.Object) bool {
	err := r.Reader.Get(ctx, key, obj)
	return err == nil
}

func (r *Reconciler) ensurePVC(ctx context.Context, pen *v1alpha1.Pen, size resource.Quantity, cfg config.Config) error {
	key := types.NamespacedName{Name: pen.Name + "-home", Namespace: pen.Namespace}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.Reader.Get(ctx, key, pvc); err == nil {
		return nil // create-or-leave
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	sclass := cfg.StorageClass
	var storageClassNamePtr *string
	if sclass != "" {
		storageClassNamePtr = &sclass
	}
	pvc = &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: pen.Namespace,
			Labels:    map[string]string{ownerLabel: pen.Name},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: storageClassNamePtr,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: size},
			},
		},
	}
	if err := r.Client.Create(ctx, pvc); err != nil {
		return fmt.Errorf("create pvc %s: %w", key.Name, err)
	}
	return nil
}

func (r *Reconciler) ensureService(ctx context.Context, pen *v1alpha1.Pen, cfg config.Config) error {
	key := types.NamespacedName{Name: pen.Name, Namespace: pen.Namespace}
	svc := &corev1.Service{}
	if err := r.Reader.Get(ctx, key, svc); err == nil {
		return nil // create-or-leave
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	lbClass := cfg.LoadBalancerClass
	// Start from the config annotations, then set the per-pen MagicDNS
	// request name on top (it is per-pen, so it cannot live in config).
	annotations := make(map[string]string, len(cfg.ServiceAnnotations)+1)
	for k, v := range cfg.ServiceAnnotations {
		annotations[k] = v
	}
	annotations["tailscale.com/hostname"] = pen.Name
	svc = &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        key.Name,
			Namespace:   pen.Namespace,
			Labels:      map[string]string{ownerLabel: pen.Name},
			Annotations: annotations,
		},
		Spec: corev1.ServiceSpec{
			Type:              corev1.ServiceTypeLoadBalancer,
			LoadBalancerClass: &lbClass,
			Selector:          map[string]string{ownerLabel: pen.Name},
			Ports: []corev1.ServicePort{{
				Name: "ssh", Port: 22, TargetPort: intstr.FromInt32(22), Protocol: corev1.ProtocolTCP,
			}},
		},
	}
	if err := controllerutil.SetControllerReference(pen, svc, r.Scheme); err != nil {
		return err
	}
	if err := r.Client.Create(ctx, svc); err != nil {
		return fmt.Errorf("create service %s: %w", key.Name, err)
	}
	return nil
}

func (r *Reconciler) ensurePod(ctx context.Context, pen *v1alpha1.Pen, image, devUser string, res corev1.ResourceList, cfg config.Config) error {
	key := types.NamespacedName{Name: podName(pen.Name), Namespace: pen.Namespace}
	pod := &corev1.Pod{}
	if err := r.Reader.Get(ctx, key, pod); err == nil {
		return nil // create-or-leave
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	hostname := pen.Name
	if cfg.TailnetDomain != "" {
		hostname = pen.Name + "." + cfg.TailnetDomain
	}
	privileged := true
	// pens run no cluster-credentialed client, so the pod mounts no
	// ServiceAccount token.
	automountToken := false
	// the dev ConfigMap keys are optional: a pen whose ConfigMap carries
	// none of them still boots.
	devConfigOptional := true
	secKey := "herdr-dev-" + pen.Namespace
	cmKey := "herdr-dev-" + pen.Namespace
	nodeSelector := cfg.NodeSelector
	spec := corev1.PodSpec{
		RuntimeClassName:             runtimeClassPtr(cfg.RuntimeClass),
		RestartPolicy:                corev1.RestartPolicyAlways,
		NodeSelector:                 nodeSelector,
		AutomountServiceAccountToken: &automountToken,
		Containers: []corev1.Container{{
			Name:            "env",
			Image:           image,
			ImagePullPolicy: pullPolicy(image, cfg.ImagePullPolicy),
			SecurityContext: &corev1.SecurityContext{Privileged: &privileged},
			Env: []corev1.EnvVar{
				{Name: "DEV_USER", Value: devUser},
				{Name: "TAILNET_HOSTNAME", Value: hostname},
			},
			Resources: corev1.ResourceRequirements{Requests: res, Limits: res},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "home", MountPath: "/home"},
				{Name: "dev-secret", MountPath: "/run/herdr/secret", ReadOnly: true},
				{Name: "dev-config", MountPath: "/run/herdr/config", ReadOnly: true},
			},
		}},
		Volumes: []corev1.Volume{
			{
				Name: "home",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pen.Name + "-home",
					},
				},
			},
			{
				Name: "dev-secret",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: secKey,
						Items:      []corev1.KeyToPath{{Key: "authorized_keys", Path: "authorized_keys"}},
					},
				},
			},
			{
				Name: "dev-config",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: cmKey},
						Optional:             &devConfigOptional,
						Items: []corev1.KeyToPath{
							{Key: "models.json", Path: "models.json"},
							{Key: "settings.json", Path: "settings.json"},
							{Key: "litellm_key", Path: "litellm_key"},
						},
					},
				},
			},
		},
	}
	pod = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: pen.Namespace,
			Labels:    map[string]string{ownerLabel: pen.Name},
		},
		Spec: spec,
	}
	if err := controllerutil.SetControllerReference(pen, pod, r.Scheme); err != nil {
		return err
	}
	if err := r.Client.Create(ctx, pod); err != nil {
		return fmt.Errorf("create pod %s: %w", key.Name, err)
	}
	return nil
}

// serviceHostname reads the tailnet-assigned hostname (or IP) from the
// pen Service's loadBalancer ingress; "" while unassigned.
func (r *Reconciler) serviceHostname(ctx context.Context, pen *v1alpha1.Pen) string {
	svc := &corev1.Service{}
	if err := r.Reader.Get(ctx, types.NamespacedName{Name: pen.Name, Namespace: pen.Namespace}, svc); err != nil {
		return ""
	}
	ingress := svc.Status.LoadBalancer.Ingress
	if len(ingress) == 0 {
		return ""
	}
	// hostname first; a tailscale ingress may report a VIP IP with no
	// hostname — that is still directly reachable, so fall back to it.
	if ingress[0].Hostname != "" {
		return ingress[0].Hostname
	}
	return ingress[0].IP
}

func (r *Reconciler) status(ctx context.Context, pen *v1alpha1.Pen, phase v1alpha1.Phase, ready bool, hostname, node, msg string) (ctrl.Result, error) {
	pen.Status.ObservedGeneration = pen.Generation
	pen.Status.Phase = phase
	pen.Status.Ready = ready
	pen.Status.Hostname = hostname
	pen.Status.Node = node
	pen.Status.Message = msg
	if err := r.Client.Status().Update(ctx, pen); err != nil {
		return ctrl.Result{}, err
	}
	switch phase {
	case v1alpha1.PhaseReady:
		return ctrl.Result{RequeueAfter: requeueReady}, nil
	case v1alpha1.PhaseCreating:
		return ctrl.Result{RequeueAfter: requeueCreating}, nil
	case v1alpha1.PhasePending:
		return ctrl.Result{RequeueAfter: requeuePending}, nil
	default:
		return ctrl.Result{}, nil
	}
}

func (r *Reconciler) fail(ctx context.Context, pen *v1alpha1.Pen, msg string) (ctrl.Result, error) {
	return r.status(ctx, pen, v1alpha1.PhaseFailed, false, "", "", msg)
}

func (r *Reconciler) pending(ctx context.Context, pen *v1alpha1.Pen, msg string) (ctrl.Result, error) {
	return r.status(ctx, pen, v1alpha1.PhasePending, false, "", "", msg)
}

func podName(pen string) string { return "env-" + pen }

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func strPtr(s string) *string { return &s }

// runtimeClassPtr: an empty runtime class must be a nil pointer (no
// runtime class at all), not a pointer to "".
func runtimeClassPtr(s string) *string {
	if s == "" {
		return nil
	}
	return strPtr(s)
}

// pullPolicy: `latest` tags must pull (the registry image is rebuilt
// nightly); pinned tags follow the operator config.
func pullPolicy(image, cfg string) corev1.PullPolicy {
	tag := image
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		tag = image[i+1:]
	}
	if tag == "latest" {
		return corev1.PullAlways
	}
	return corev1.PullPolicy(cfg)
}

func podReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	cs := pod.Status.ContainerStatuses
	if len(cs) == 0 {
		return false
	}
	return cs[0].Ready
}

func podProgress(pod *corev1.Pod) string {
	phase := string(pod.Status.Phase)
	if cs := pod.Status.ContainerStatuses; len(cs) > 0 {
		if w := cs[0].State.Waiting; w != nil {
			phase = phase + ": " + w.Reason
		}
	}
	return string(phase)
}
