// Package pen reconciles Pen objects. For each Pen it ensures the home
// PVC, the Sandbox and the tailnet-exposed Service, and reports the
// result on Pen status. Reconcile converges the Sandbox (operatingMode,
// shutdownTime, service, env container resources), the egress
// NetworkPolicy, and the home PVC size (grow only). The PVC is never
// deleted by the operator.
package pen

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"

	v1alpha1 "github.com/caktus/pipen/operator/api/v1alpha1"
	"github.com/caktus/pipen/operator/internal/config"
)

const (
	// ownerLabel marks every object the operator creates for a pen.
	ownerLabel = "herdr.dev/env"
	// ownerLabelKey is the namespace label a provisioned dev namespace has.
	ownerLabelKey = "herdr.dev/owner"
	// effectiveTTLAnnotation records the TTL used for the current shutdown time.
	effectiveTTLAnnotation = "pipen.dev/effective-ttl"
	// homeExpandAnnotation records when the current home expansion started
	// (RFC3339, from the injected clock). It is set on the size patch and
	// removed when the provisioned capacity reaches the desired size.
	homeExpandAnnotation = "pipen.dev/home-expand-at"
	// credentialsAnnotation is the pen annotation that names the
	// credentials Secret in the pen's namespace.
	credentialsAnnotation = "pens.pipen.dev/credentials"

	requeueCreating = 10 * time.Second
	requeuePending  = 30 * time.Second
	// requeueReady keeps a Ready pen periodically reconciled: status stays
	// truthful (hostname changes, node moves) and a deleted Service is
	// recreated by the next pass. The sandbox controller recreates the
	// pod from the stored podTemplate.
	requeueReady = 30 * time.Second
	// homeExpandStallTimeout is how long a home expansion may run before
	// the pen fails. A fixed constant, not user-configurable.
	homeExpandStallTimeout = 5 * time.Minute
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
	// Now returns the current time. Tests use a fixed time for TTL math.
	Now func() time.Time
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
	if r.Now == nil {
		r.Now = time.Now
	}
	cfg := r.Config
	image := pen.Spec.Image
	if image == "" {
		image = cfg.Image
	}
	// Record the resolved image before the first status write, so every
	// phase, fail and pending write carries the same reference as the
	// pod spec.
	pen.Status.Image = image

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

	growing, err := r.ensurePVC(ctx, pen, homeQty, cfg)
	if err != nil {
		return r.fail(ctx, pen, err.Error())
	}
	if err := r.ensureService(ctx, pen, cfg); err != nil {
		return r.fail(ctx, pen, err.Error())
	}
	res := corev1.ResourceList{
		corev1.ResourceCPU:    cpuQty,
		corev1.ResourceMemory: memQty,
	}
	if err := r.ensureSandbox(ctx, pen, image, devUser, res, cfg); err != nil {
		return r.fail(ctx, pen, err.Error())
	}
	if err := r.ensureNetworkPolicy(ctx, pen, cfg); err != nil {
		return r.fail(ctx, pen, err.Error())
	}

	// Read sandbox status
	sandbox := &v1beta1.Sandbox{}
	if err := r.Reader.Get(ctx, types.NamespacedName{Name: podName(pen.Name), Namespace: pen.Namespace}, sandbox); err != nil {
		return r.fail(ctx, pen, fmt.Sprintf("cannot read sandbox: %v", err))
	}

	// Always mirror shutdownTime to pen status so CLI can render remaining TTL
	if sandbox.Spec.ShutdownTime != nil {
		pen.Status.ShutdownAt = sandbox.Spec.ShutdownTime
	} else {
		pen.Status.ShutdownAt = nil
	}

	// Map sandbox conditions to pen status
	for _, cond := range sandbox.Status.Conditions {
		switch cond.Type {
		case string(v1beta1.SandboxConditionReady):
			if cond.Status == metav1.ConditionTrue && cond.Reason == v1beta1.SandboxReasonDependenciesReady {
				hostname := r.serviceHostname(ctx, pen)
				if hostname == "" {
					return r.status(ctx, pen, v1alpha1.PhaseCreating, false, "", "",
						"sandbox ready, waiting for tailnet hostname on service "+pen.Name)
				}
				// Sandbox status may have node name
				node := sandbox.Status.NodeName
				if node == "" {
					// Fall back to reading pod for node (for pipen logs)
					pod := &corev1.Pod{}
					if err := r.Reader.Get(ctx, types.NamespacedName{Name: podName(pen.Name), Namespace: pen.Namespace}, pod); err == nil {
						node = pod.Spec.NodeName
					}
				}
				msg := "ready"
				if growing {
					// The home grow runs in the background; the pen stays Ready.
					msg = fmt.Sprintf("growing home to %s", homeQty.String())
				}
				return r.status(ctx, pen, v1alpha1.PhaseReady, true, hostname, node, msg)
			} else if cond.Reason == v1beta1.SandboxReasonSuspended {
				return r.status(ctx, pen, v1alpha1.PhaseSuspended, false, "", "", "suspended")
			} else if cond.Reason == v1beta1.SandboxReasonExpired {
				// Expired sandbox is not ready
				return r.status(ctx, pen, v1alpha1.PhaseCreating, false, "", "", "sandbox expired, will resume on next reconcile")
			} else if cond.Reason == v1beta1.SandboxReasonReconcilerError {
				return r.fail(ctx, pen, cond.Message)
			} else if cond.Reason == v1beta1.SandboxReasonInvalidConfiguration {
				// Permanent (bad podTemplate): retrying cannot fix it, so
				// the pen fails instead of looping on Creating.
				return r.fail(ctx, pen, cond.Message)
			} else {
				// DependenciesNotReady or other reasons
				return r.status(ctx, pen, v1alpha1.PhaseCreating, false, "", "", cond.Message)
			}
		case string(v1beta1.SandboxConditionSuspended):
			if cond.Status == metav1.ConditionTrue {
				return r.status(ctx, pen, v1alpha1.PhaseSuspended, false, "", "", "suspended")
			}
		case string(v1beta1.SandboxConditionPodScheduled):
			if cond.Status == metav1.ConditionFalse {
				return r.status(ctx, pen, v1alpha1.PhaseCreating, false, "", "", cond.Message)
			}
		}
	}

	// No Ready condition found
	return r.status(ctx, pen, v1alpha1.PhaseCreating, false, "", "", "sandbox not ready, no conditions yet")
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

func (r *Reconciler) ensurePVC(ctx context.Context, pen *v1alpha1.Pen, size resource.Quantity, cfg config.Config) (bool, error) {
	key := types.NamespacedName{Name: pen.Name + "-home", Namespace: pen.Namespace}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.Reader.Get(ctx, key, pvc); err == nil {
		// A pen that pins no home size keeps the provisioned size. The
		// config default feeds pens created afterwards only.
		if pen.Spec.HomeSize == "" {
			return false, nil
		}
		return r.convergePVCSize(ctx, pvc, size)
	} else if !apierrors.IsNotFound(err) {
		return false, err
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
		return false, fmt.Errorf("create pvc %s: %w", key.Name, err)
	}
	return false, nil
}

// convergePVCSize moves an existing home PVC to the desired size and
// reports whether a grow is in flight. A shrink fails (home size can
// only grow). A grow patches the request once and is watched by the
// stamp on the claim. When the provisioned capacity reaches the desired
// size the stamp is removed.
func (r *Reconciler) convergePVCSize(ctx context.Context, pvc *corev1.PersistentVolumeClaim, size resource.Quantity) (bool, error) {
	switch size.Cmp(pvcCurrentSize(pvc)) {
	case -1:
		return false, fmt.Errorf("home size can only grow")
	case 1:
		return r.growPVC(ctx, pvc, size)
	}
	if _, ok := pvc.Annotations[homeExpandAnnotation]; !ok {
		return false, nil // unchanged size: no patch, no annotation
	}
	if q := pvc.Status.Capacity[corev1.ResourceStorage]; q.IsZero() {
		// No provisioned capacity yet: the claim is not bound, or the
		// volume was lost. The grow is still in flight; growPVC watches
		// the stamp and fails on a stall.
		return r.growPVC(ctx, pvc, size)
	}
	// The provisioned capacity reached the desired size: the grow completed.
	delete(pvc.Annotations, homeExpandAnnotation)
	if err := r.Client.Update(ctx, pvc); err != nil {
		return false, fmt.Errorf("update pvc %s: %w", pvc.Name, err)
	}
	return false, nil
}

// growPVC starts or watches a home grow. The first pass checks that the
// storage class allows expansion, patches the request to the desired
// size, and stamps when this grow started. A claim without a
// StorageClassName binds to the cluster default, which this check cannot
// read (get only, no list); the stall timeout covers that case. Later
// passes wait for the capacity to catch up and fail when the stamp is
// older than homeExpandStallTimeout.
func (r *Reconciler) growPVC(ctx context.Context, pvc *corev1.PersistentVolumeClaim, size resource.Quantity) (bool, error) {
	specSize := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if specSize.Cmp(size) == 0 {
		// The size patch is in flight: watch the stamp.
		if started, err := time.Parse(time.RFC3339, pvc.Annotations[homeExpandAnnotation]); err == nil {
			if r.Now().Sub(started) > homeExpandStallTimeout {
				return false, fmt.Errorf("home expansion stalled: %s", storageClassHint(pvc))
			}
			return true, nil
		}
	}
	// First pass (or a lost stamp): check the storage class, patch the
	// request, and stamp.
	if name := pvc.Spec.StorageClassName; name != nil {
		sc := &storagev1.StorageClass{}
		if err := r.Reader.Get(ctx, types.NamespacedName{Name: *name}, sc); err != nil {
			return false, fmt.Errorf("cannot read storage class %s", *name)
		}
		if sc.AllowVolumeExpansion == nil || !*sc.AllowVolumeExpansion {
			return false, fmt.Errorf("storage class %s does not allow volume expansion", *name)
		}
	}
	pvc.Spec.Resources.Requests[corev1.ResourceStorage] = size
	if pvc.Annotations == nil {
		pvc.Annotations = map[string]string{}
	}
	pvc.Annotations[homeExpandAnnotation] = r.Now().UTC().Format(time.RFC3339)
	if err := r.Client.Update(ctx, pvc); err != nil {
		return false, fmt.Errorf("update pvc %s: %w", pvc.Name, err)
	}
	return true, nil
}

// pvcCurrentSize is the size a home resize compares against: the
// provisioned capacity when set, else the spec request (a claim that is
// not provisioned yet).
func pvcCurrentSize(pvc *corev1.PersistentVolumeClaim) resource.Quantity {
	if q := pvc.Status.Capacity[corev1.ResourceStorage]; !q.IsZero() {
		return q
	}
	return pvc.Spec.Resources.Requests[corev1.ResourceStorage]
}

// storageClassHint names the likely cause of a stalled home grow: the
// storage class named on the PVC, or the cluster default when the PVC
// names none.
func storageClassHint(pvc *corev1.PersistentVolumeClaim) string {
	if name := pvc.Spec.StorageClassName; name != nil {
		return fmt.Sprintf("storage class %s must allow volume expansion", *name)
	}
	return "the default storage class must allow volume expansion"
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

func (r *Reconciler) ensureSandbox(ctx context.Context, pen *v1alpha1.Pen, image, devUser string, res corev1.ResourceList, cfg config.Config) error {
	key := types.NamespacedName{Name: podName(pen.Name), Namespace: pen.Namespace}
	sandbox := &v1beta1.Sandbox{}
	if err := r.Reader.Get(ctx, key, sandbox); err == nil {
		// Convergence: check if spec needs update
		needsUpdate := false

		// Resume detection, before the mode below is flipped: the pen is
		// not paused, the sandbox is still in Suspended mode, and the pod
		// is gone. The stamp is one-shot per transition because the flip
		// lands in the same update, so the next pass already sees
		// Running mode.
		terminated := podTerminated(sandbox)
		resumed := !pen.Spec.Paused && sandbox.Spec.OperatingMode == v1beta1.SandboxOperatingModeSuspended && terminated

		// 1. operatingMode: Suspended while the pen is paused, or while
		// the resume flip waits for the pod to go (terminated above).
		desiredMode := v1beta1.SandboxOperatingModeRunning
		if pen.Spec.Paused || (sandbox.Spec.OperatingMode == v1beta1.SandboxOperatingModeSuspended && !terminated) {
			desiredMode = v1beta1.SandboxOperatingModeSuspended
		}
		if sandbox.Spec.OperatingMode != desiredMode {
			sandbox.Spec.OperatingMode = desiredMode
			needsUpdate = true
		}

		// 2. Reset the shutdown time when the TTL changes, the sandbox
		// expires, or the sandbox resumes from Suspended mode. A resume
		// must get a fresh TTL, or the agent-sandbox controller expires
		// the pod right after resume when the old time is already past.
		ttl := orDefault(pen.Spec.TTL, cfg.DefaultTTL)
		storedTTL, hasStoredTTL := sandbox.Annotations[effectiveTTLAnnotation]
		expired := false
		for _, cond := range sandbox.Status.Conditions {
			if cond.Type == string(v1beta1.SandboxConditionReady) &&
				cond.Status == metav1.ConditionFalse &&
				cond.Reason == v1beta1.SandboxReasonExpired &&
				!pen.Spec.Paused {
				expired = true
				break
			}
		}
		if !hasStoredTTL || storedTTL != ttl || expired || resumed {
			sandbox.Spec.Lifecycle, err = newLifecycle(ttl, r.Now())
			if err != nil {
				return err
			}
			if sandbox.Annotations == nil {
				sandbox.Annotations = map[string]string{}
			}
			sandbox.Annotations[effectiveTTLAnnotation] = ttl
			needsUpdate = true
		}

		// 4. Update service if config changed
		if sandbox.Spec.Service == nil || *sandbox.Spec.Service != cfg.SandboxService {
			sandbox.Spec.Service = &cfg.SandboxService
			needsUpdate = true
		}

		// 5. Convergence of the env container resources: a resize sets
		// spec.cpu or spec.memory, and the stored podTemplate must
		// follow. A non-paused pen also goes Suspended, so the sandbox
		// controller deletes the pod and starts one from the new
		// template. A paused pen gets the template update only.
		ctr := envContainer(&sandbox.Spec.PodTemplate.Spec)
		if ctr != nil && resourcesDiverge(ctr.Resources, res, pen.Spec.CPU, pen.Spec.Memory) {
			ctr.Resources = corev1.ResourceRequirements{Requests: res, Limits: res}
			needsUpdate = true
			if !pen.Spec.Paused {
				sandbox.Spec.OperatingMode = v1beta1.SandboxOperatingModeSuspended
			}
		}

		// 6. Convergence of the credentials volume: an annotation added,
		// removed, or renamed patches the stored podTemplate (volume and
		// mount follow). The non-paused pen goes Suspended through the
		// same cycle as a resize. A missing Secret means an empty
		// projection, so the operator never reads the Secret.
		if applyCredentials(&sandbox.Spec.PodTemplate.Spec, pen.Annotations[credentialsAnnotation]) {
			needsUpdate = true
			if !pen.Spec.Paused {
				sandbox.Spec.OperatingMode = v1beta1.SandboxOperatingModeSuspended
			}
		}

		if needsUpdate {
			if err := r.Client.Update(ctx, sandbox); err != nil {
				return fmt.Errorf("update sandbox %s: %w", key.Name, err)
			}
		}
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}

	// Create new sandbox
	hostname := pen.Name
	if cfg.TailnetDomain != "" {
		hostname = pen.Name + "." + cfg.TailnetDomain
	}
	privileged := true
	automountToken := false
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

	// The credentials annotation adds an optional Secret volume (all
	// keys) and its read-only mount to the pod spec.
	applyCredentials(&spec, pen.Annotations[credentialsAnnotation])

	// Set operatingMode based on pen.Spec.Paused
	operatingMode := v1beta1.SandboxOperatingModeRunning
	if pen.Spec.Paused {
		operatingMode = v1beta1.SandboxOperatingModeSuspended
	}

	ttl := orDefault(pen.Spec.TTL, cfg.DefaultTTL)
	lifecycle, err := newLifecycle(ttl, r.Now())
	if err != nil {
		return err
	}

	sandbox = &v1beta1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:        key.Name,
			Namespace:   pen.Namespace,
			Labels:      map[string]string{ownerLabel: pen.Name},
			Annotations: map[string]string{effectiveTTLAnnotation: ttl},
		},
		Spec: v1beta1.SandboxSpec{
			SandboxBlueprint: v1beta1.SandboxBlueprint{
				PodTemplate: v1beta1.PodTemplate{
					Spec: spec,
					ObjectMeta: v1beta1.PodMetadata{
						Labels: map[string]string{ownerLabel: pen.Name},
					},
				},
				Service:              &cfg.SandboxService,
				FailedPodPolicy:      v1beta1.FailedPodPolicyRetain,
				VolumeClaimTemplates: []v1beta1.PersistentVolumeClaimTemplate{},
			},
			Lifecycle:     lifecycle,
			OperatingMode: operatingMode,
		},
	}
	if err := controllerutil.SetControllerReference(pen, sandbox, r.Scheme); err != nil {
		return err
	}
	if err := r.Client.Create(ctx, sandbox); err != nil {
		return fmt.Errorf("create sandbox %s: %w", key.Name, err)
	}
	return nil
}

// ensureNetworkPolicy manages the per-pen egress allow list. It exists
// only when spec.egress is on and the operator config names hosts. The
// namespace default-deny-egress stays in place, so this policy can only
// narrow egress further (policies intersect).
func (r *Reconciler) ensureNetworkPolicy(ctx context.Context, pen *v1alpha1.Pen, cfg config.Config) error {
	key := types.NamespacedName{Name: pen.Name + "-egress", Namespace: pen.Namespace}
	policy := &networkingv1.NetworkPolicy{}
	err := r.Reader.Get(ctx, key, policy)
	exists := err == nil
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if !pen.Spec.Egress || len(cfg.EgressHosts) == 0 {
		if exists {
			if err := r.Client.Delete(ctx, policy); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete egress network policy %s: %w", key.Name, err)
			}
		}
		return nil
	}

	dnsPort := intstr.FromInt32(53)
	tcp := corev1.ProtocolTCP
	udp := corev1.ProtocolUDP
	// The DNS rule mirrors the dev-namespace chart: kube-dns in kube-system
	// on port 53, TCP and UDP.
	rules := []networkingv1.NetworkPolicyEgressRule{
		{
			To: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"},
				},
				PodSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"k8s-app": "kube-dns"},
				},
			}},
			Ports: []networkingv1.NetworkPolicyPort{
				{Protocol: &tcp, Port: &dnsPort},
				{Protocol: &udp, Port: &dnsPort},
			},
		},
	}
	for _, host := range cfg.EgressHosts {
		rules = append(rules, networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{{
				IPBlock: &networkingv1.IPBlock{CIDR: host},
			}},
		})
	}
	desired := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: pen.Namespace,
			Labels:    map[string]string{ownerLabel: pen.Name},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{ownerLabel: pen.Name}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress:      rules,
		},
	}
	if exists {
		if reflect.DeepEqual(policy.Labels, desired.Labels) && reflect.DeepEqual(policy.Spec, desired.Spec) {
			return nil
		}
		policy.Labels = desired.Labels
		policy.Spec = desired.Spec
		if err := r.Client.Update(ctx, policy); err != nil {
			return fmt.Errorf("update egress network policy %s: %w", key.Name, err)
		}
		return nil
	}
	if err := controllerutil.SetControllerReference(pen, desired, r.Scheme); err != nil {
		return err
	}
	if err := r.Client.Create(ctx, desired); err != nil {
		return fmt.Errorf("create egress network policy %s: %w", key.Name, err)
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
	case v1alpha1.PhaseSuspended:
		// Suspended is transient after a resize flip, and the operator
		// watches no Sandbox, so a requeue observes the flip back to
		// Running. A paused pen paces here and writes no spec.
		return ctrl.Result{RequeueAfter: requeueCreating}, nil
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

// envContainer returns the env container of the pod spec, or nil when
// the spec has no env container.
func envContainer(spec *corev1.PodSpec) *corev1.Container {
	for i := range spec.Containers {
		if spec.Containers[i].Name == "env" {
			return &spec.Containers[i]
		}
	}
	return nil
}

// applyCredentials makes the dev-credentials volume and the env
// container mount of the pod spec match secretName (the value of the
// credentials annotation; empty means absent) and reports whether it
// changed the spec. The volume projects all keys and is optional, so a
// missing Secret leaves an empty projection and the pod still starts.
func applyCredentials(spec *corev1.PodSpec, secretName string) bool {
	changed := false
	var wantVol *corev1.Volume
	if secretName != "" {
		optional := true
		wantVol = &corev1.Volume{
			Name: "dev-credentials",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
					Optional:   &optional,
				},
			},
		}
	}
	volIdx := -1
	for i := range spec.Volumes {
		if spec.Volumes[i].Name == "dev-credentials" {
			volIdx = i
			break
		}
	}
	switch {
	case wantVol == nil && volIdx >= 0:
		spec.Volumes = append(spec.Volumes[:volIdx], spec.Volumes[volIdx+1:]...)
		changed = true
	case wantVol != nil && volIdx >= 0:
		if !reflect.DeepEqual(spec.Volumes[volIdx], *wantVol) {
			spec.Volumes[volIdx] = *wantVol
			changed = true
		}
	case wantVol != nil:
		spec.Volumes = append(spec.Volumes, *wantVol)
		changed = true
	}
	ctr := envContainer(spec)
	if ctr == nil {
		return changed
	}
	var wantMount *corev1.VolumeMount
	if secretName != "" {
		wantMount = &corev1.VolumeMount{
			Name: "dev-credentials", MountPath: "/run/herdr/credentials", ReadOnly: true,
		}
	}
	mountIdx := -1
	for i := range ctr.VolumeMounts {
		if ctr.VolumeMounts[i].Name == "dev-credentials" {
			mountIdx = i
			break
		}
	}
	switch {
	case wantMount == nil && mountIdx >= 0:
		ctr.VolumeMounts = append(ctr.VolumeMounts[:mountIdx], ctr.VolumeMounts[mountIdx+1:]...)
		changed = true
	case wantMount != nil && mountIdx >= 0:
		if !reflect.DeepEqual(ctr.VolumeMounts[mountIdx], *wantMount) {
			ctr.VolumeMounts[mountIdx] = *wantMount
			changed = true
		}
	case wantMount != nil:
		ctr.VolumeMounts = append(ctr.VolumeMounts, *wantMount)
		changed = true
	}
	return changed
}

// podTerminated reports whether the Suspended condition says the pod is
// gone (Status True, reason PodTerminated). The resume flip waits for
// this state. An earlier flip keeps the old pod, so the new size never
// applies.
func podTerminated(sandbox *v1beta1.Sandbox) bool {
	for _, cond := range sandbox.Status.Conditions {
		if cond.Type == string(v1beta1.SandboxConditionSuspended) {
			return cond.Status == metav1.ConditionTrue &&
				cond.Reason == v1beta1.SandboxReasonSuspendedPodTerminated
		}
	}
	return false
}

// resourcesDiverge reports whether the stored env container cpu or
// memory differs from the desired value for a field the pen sets
// explicitly. An empty spec field follows the config default at create
// time only; later config changes do not move an existing pen.
func resourcesDiverge(stored corev1.ResourceRequirements, desired corev1.ResourceList, cpu, memory string) bool {
	if cpu != "" && (stored.Requests.Cpu().Cmp(*desired.Cpu()) != 0 ||
		stored.Limits.Cpu().Cmp(*desired.Cpu()) != 0) {
		return true
	}
	return memory != "" && (stored.Requests.Memory().Cmp(*desired.Memory()) != 0 ||
		stored.Limits.Memory().Cmp(*desired.Memory()) != 0)
}

// newLifecycle returns a retained Sandbox shutdown time for a nonempty TTL.
func newLifecycle(ttl string, now time.Time) (v1beta1.Lifecycle, error) {
	if ttl == "" {
		return v1beta1.Lifecycle{}, nil
	}
	duration, err := time.ParseDuration(ttl)
	if err != nil {
		return v1beta1.Lifecycle{}, fmt.Errorf("invalid TTL %q: %w", ttl, err)
	}
	shutdownTime := metav1.NewTime(now.Add(duration))
	retainPolicy := v1beta1.ShutdownPolicyRetain
	return v1beta1.Lifecycle{ShutdownTime: &shutdownTime, ShutdownPolicy: &retainPolicy}, nil
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
