package pen

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"

	v1alpha1 "github.com/caktus/pipen/operator/api/v1alpha1"
	"github.com/caktus/pipen/operator/internal/config"
)

const (
	devNS   = "tobias"
	penName = "smoke"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := v1beta1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func testConfig() config.Config {
	return config.Config{
		Image:        "ghcr.io/caktus/pipen:latest",
		HomeSize:     "10Gi",
		CPU:          "4",
		Memory:       "8Gi",
		NodeSelector: map[string]string{"openebs.io/engine": "mayastor"},
		StorageClass: "mayastor-2",
		ServiceAnnotations: map[string]string{
			"tailscale.com/expose": "true",
		},
		TailnetDomain:     "ts.example.net",
		ImagePullPolicy:   "IfNotPresent",
		LoadBalancerClass: "tailscale",
		RuntimeClass:      "",
	}
}

func ownedNS(t *testing.T, name string) *corev1.Namespace {
	t.Helper()
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"herdr.dev/owner": name},
		},
	}
}

func newClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.Pen{}).
		Build()
}

func newPen() *v1alpha1.Pen {
	return &v1alpha1.Pen{
		ObjectMeta: metav1.ObjectMeta{Name: penName, Namespace: devNS, Generation: 1},
		Spec:       v1alpha1.PenSpec{},
	}
}

func getPen(t *testing.T, c client.Client) *v1alpha1.Pen {
	t.Helper()
	p := &v1alpha1.Pen{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: penName, Namespace: devNS}, p); err != nil {
		t.Fatalf("get pen: %v", err)
	}
	return p
}

func devObjects(ns string) []client.Object {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "herdr-dev-" + ns, Namespace: ns},
		Data:       map[string]string{"models.json": "{}", "settings.json": "{}", "litellm_key": "sk-1"},
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "herdr-dev-" + ns, Namespace: ns},
		Data:       map[string][]byte{"authorized_keys": []byte("ssh-ed25519 AAAA")},
	}
	return []client.Object{cm, sec}
}

func readyPod() *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "env-" + penName, Namespace: devNS},
		Spec:       corev1.PodSpec{NodeName: "storage-1"},
	}
	p.Status.Phase = corev1.PodRunning
	p.Status.ContainerStatuses = []corev1.ContainerStatus{{Ready: true}}
	return p
}

func svcWithIngress(hostname, ip string) *corev1.Service {
	s := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: penName, Namespace: devNS},
	}
	if hostname != "" {
		s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{Hostname: hostname}}
	} else {
		s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: ip}}
	}
	return s
}

func reconcileOnce(t *testing.T, c client.Client) reconcile.Result {
	return reconcileOnceIn(t, c, devNS)
}

func reconcileOnceIn(t *testing.T, c client.Client, ns string) reconcile.Result {
	t.Helper()
	return reconcileOnceWith(t, c, ns, testConfig())
}

func reconcileOnceWith(t *testing.T, c client.Client, ns string, cfg config.Config) reconcile.Result {
	t.Helper()
	return reconcileOnceAt(t, c, ns, cfg, time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC))
}

func reconcileOnceAt(t *testing.T, c client.Client, ns string, cfg config.Config, now time.Time) reconcile.Result {
	t.Helper()
	r := &Reconciler{Client: c, Reader: c, Scheme: testScheme(t), Config: cfg, Now: func() time.Time { return now }}
	res, err := r.Reconcile(context.Background(),
		reconcile.Request{NamespacedName: types.NamespacedName{Name: penName, Namespace: ns}})
	if err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}
	return res
}

func TestUnprovisionedNamespaceFails(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: devNS}}
	c := newClient(t, ns, newPen())
	res := reconcileOnce(t, c)
	p := getPen(t, c)
	if p.Status.Phase != v1alpha1.PhaseFailed {
		t.Errorf("phase = %q, want Failed", p.Status.Phase)
	}
	if !strings.Contains(p.Status.Message, "namespace not provisioned") {
		t.Errorf("message = %q, want 'namespace not provisioned'", p.Status.Message)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want 0 (permanent failure)", res.RequeueAfter)
	}
}

// A present-but-empty herdr.dev/owner label is not provisioned: the guest
// user would be empty and the entrypoint would crash on ${DEV_USER:?}, so
// the pen fails and no pod is created.
func TestEmptyOwnerLabelFails(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: devNS, Labels: map[string]string{ownerLabelKey: ""}},
	}
	c := newClient(t, ns, newPen())
	res := reconcileOnce(t, c)
	p := getPen(t, c)
	if p.Status.Phase != v1alpha1.PhaseFailed {
		t.Errorf("phase = %q, want Failed", p.Status.Phase)
	}
	if !strings.Contains(p.Status.Message, "empty herdr.dev/owner label") {
		t.Errorf("message = %q, want 'empty herdr.dev/owner label'", p.Status.Message)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want 0 (permanent failure)", res.RequeueAfter)
	}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, &corev1.Pod{}); err == nil {
		t.Error("pod created, want none")
	}
}

func TestMissingDevConfigMapPends(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "herdr-dev-" + devNS, Namespace: devNS},
	}
	c := newClient(t, ownedNS(t, devNS), newPen(), sec)
	res := reconcileOnce(t, c)
	p := getPen(t, c)
	if p.Status.Phase != v1alpha1.PhasePending {
		t.Errorf("phase = %q, want Pending", p.Status.Phase)
	}
	if res.RequeueAfter == 0 {
		t.Error("expected requeue while pending")
	}
}

func TestMissingDevSecretPends(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "herdr-dev-" + devNS, Namespace: devNS},
	}
	c := newClient(t, ownedNS(t, devNS), newPen(), cm)
	res := reconcileOnce(t, c)
	p := getPen(t, c)
	if p.Status.Phase != v1alpha1.PhasePending {
		t.Errorf("phase = %q, want Pending", p.Status.Phase)
	}
	if res.RequeueAfter == 0 {
		t.Error("expected requeue while pending")
	}
}

func TestReconcileCreatesObjects(t *testing.T) {
	objs := append([]client.Object{ownedNS(t, devNS), newPen()}, devObjects(devNS)...)
	c := newClient(t, objs...)
	res := reconcileOnce(t, c)
	ctx := context.Background()

	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, types.NamespacedName{Name: penName + "-home", Namespace: devNS}, pvc); err != nil {
		t.Fatalf("pvc not created: %v", err)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "mayastor-2" {
		t.Errorf("pvc storageClass = %v, want mayastor-2", pvc.Spec.StorageClassName)
	}
	if pvc.Spec.Resources.Requests.Storage().String() != "10Gi" {
		t.Errorf("pvc size = %s, want 10Gi", pvc.Spec.Resources.Requests.Storage())
	}
	if pvc.Labels["herdr.dev/env"] != penName {
		t.Errorf("pvc label herdr.dev/env = %q, want %s", pvc.Labels["herdr.dev/env"], penName)
	}

	svc := &corev1.Service{}
	if err := c.Get(ctx, types.NamespacedName{Name: penName, Namespace: devNS}, svc); err != nil {
		t.Fatalf("service not created: %v", err)
	}
	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		t.Errorf("service type = %q, want LoadBalancer", svc.Spec.Type)
	}
	if svc.Spec.LoadBalancerClass == nil || *svc.Spec.LoadBalancerClass != "tailscale" {
		t.Errorf("service LB class = %v, want tailscale", svc.Spec.LoadBalancerClass)
	}
	if svc.Spec.Ports[0].Port != 22 || svc.Spec.Ports[0].Name != "ssh" {
		t.Errorf("service port = %+v, want ssh/22", svc.Spec.Ports[0])
	}
	if svc.Spec.Selector["herdr.dev/env"] != penName {
		t.Errorf("service selector = %v, want herdr.dev/env=%s", svc.Spec.Selector, penName)
	}
	if svc.Annotations["tailscale.com/expose"] != "true" {
		t.Errorf("service annotations = %v, want tailscale.com/expose=true", svc.Annotations)
	}
	if svc.Annotations["tailscale.com/hostname"] != penName {
		t.Errorf("service hostname annotation = %q, want %q (set per pen by the operator)",
			svc.Annotations["tailscale.com/hostname"], penName)
	}
	if len(svc.OwnerReferences) != 1 || svc.OwnerReferences[0].Kind != "Pen" {
		t.Errorf("service ownerRefs = %+v, want one Pen ownerRef", svc.OwnerReferences)
	}

	sandbox := &v1beta1.Sandbox{}
	if err := c.Get(ctx, types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, sandbox); err != nil {
		t.Fatalf("sandbox not created: %v", err)
	}
	if sandbox.Spec.PodTemplate.Spec.RuntimeClassName != nil {
		t.Errorf("sandbox podTemplate runtimeClass = %v, want nil for empty runtime class", sandbox.Spec.PodTemplate.Spec.RuntimeClassName)
	}
	sec := sandbox.Spec.PodTemplate.Spec.Containers[0].SecurityContext
	if sec == nil || sec.Privileged == nil || !*sec.Privileged {
		t.Error("sandbox container not privileged")
	}
	if sandbox.Spec.PodTemplate.Spec.NodeSelector["openebs.io/engine"] != "mayastor" {
		t.Errorf("sandbox nodeSelector = %v, want openebs.io/engine=mayastor", sandbox.Spec.PodTemplate.Spec.NodeSelector)
	}
	ctr := sandbox.Spec.PodTemplate.Spec.Containers[0]
	if ctr.Image != "ghcr.io/caktus/pipen:latest" {
		t.Errorf("sandbox image = %q, want cfg default", ctr.Image)
	}
	if ctr.ImagePullPolicy != corev1.PullAlways {
		t.Errorf("sandbox pullPolicy = %q, want Always for latest tag", ctr.ImagePullPolicy)
	}
	if ctr.Resources.Requests.Cpu().String() != "4" || ctr.Resources.Limits.Cpu().String() != "4" {
		t.Errorf("sandbox cpu req/lim wrong: %+v", ctr.Resources)
	}
	if ctr.Resources.Requests.Memory().String() != "8Gi" || ctr.Resources.Limits.Memory().String() != "8Gi" {
		t.Errorf("sandbox memory req/lim wrong: %+v", ctr.Resources)
	}
	envs := map[string]string{}
	for _, e := range ctr.Env {
		envs[e.Name] = e.Value
	}
	if envs["DEV_USER"] != devNS {
		t.Errorf("DEV_USER = %q, want %q", envs["DEV_USER"], devNS)
	}
	mounts := map[string]string{}
	for _, m := range ctr.VolumeMounts {
		mounts[m.Name] = m.MountPath
	}
	if mounts["home"] != "/home" || mounts["dev-secret"] != "/run/herdr/secret" || mounts["dev-config"] != "/run/herdr/config" {
		t.Errorf("volumeMounts = %v", mounts)
	}
	vols := map[string]bool{}
	for _, v := range sandbox.Spec.PodTemplate.Spec.Volumes {
		vols[v.Name] = true
	}
	if !vols["home"] || !vols["dev-secret"] || !vols["dev-config"] {
		t.Errorf("volumes = %v", vols)
	}
	if len(sandbox.OwnerReferences) != 1 || sandbox.OwnerReferences[0].Kind != "Pen" {
		t.Errorf("sandbox ownerRefs = %+v, want one Pen ownerRef", sandbox.OwnerReferences)
	}
	if sandbox.Labels["herdr.dev/env"] != penName {
		t.Errorf("sandbox label herdr.dev/env = %q", sandbox.Labels["herdr.dev/env"])
	}

	p := getPen(t, c)
	if p.Status.Phase != v1alpha1.PhaseCreating {
		t.Errorf("phase = %q, want Creating (sandbox not ready yet)", p.Status.Phase)
	}
	if p.Status.Ready {
		t.Error("ready = true, want false")
	}
	if res.RequeueAfter == 0 {
		t.Error("expected requeue while creating")
	}
}

// The dev ConfigMap keys are optional (a pen whose ConfigMap carries none
// of them still boots), so the dev-config volume mounts as optional. The
// pen pod runs no cluster-credentialed client, so it mounts no
// ServiceAccount token.
func TestDevConfigVolumeOptionalAndNoSAToken(t *testing.T) {
	objs := append([]client.Object{ownedNS(t, devNS), newPen()}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnce(t, c)
	sandbox := &v1beta1.Sandbox{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, sandbox); err != nil {
		t.Fatal(err)
	}
	if sandbox.Spec.PodTemplate.Spec.AutomountServiceAccountToken == nil || *sandbox.Spec.PodTemplate.Spec.AutomountServiceAccountToken {
		t.Errorf("automountServiceAccountToken = %v, want false", sandbox.Spec.PodTemplate.Spec.AutomountServiceAccountToken)
	}
	var cm *corev1.ConfigMapVolumeSource
	for _, v := range sandbox.Spec.PodTemplate.Spec.Volumes {
		if v.Name == "dev-config" {
			cm = v.ConfigMap
		}
	}
	if cm == nil {
		t.Fatal("dev-config volume missing")
	}
	if cm.Optional == nil || !*cm.Optional {
		t.Errorf("dev-config volume optional = %v, want true", cm.Optional)
	}
}

// An empty runtimeClass (the kind e2e cluster shape) must leave the pod
// with no runtime class: kind nodes run no kata runtime.
func TestReconcileNoRuntimeClass(t *testing.T) {
	cfg := testConfig()
	cfg.RuntimeClass = ""
	objs := append([]client.Object{ownedNS(t, devNS), newPen()}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnceWith(t, c, devNS, cfg)
	sandbox := &v1beta1.Sandbox{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, sandbox); err != nil {
		t.Fatal(err)
	}
	if sandbox.Spec.PodTemplate.Spec.RuntimeClassName != nil {
		t.Errorf("sandbox runtimeClass = %q, want none", *sandbox.Spec.PodTemplate.Spec.RuntimeClassName)
	}
}

// A non-default runtime class must be used as configured: the old code
// hardcoded kata, so only a non-default value proves the config is read.
func TestReconcileCustomRuntimeClass(t *testing.T) {
	cfg := testConfig()
	cfg.RuntimeClass = "gvisor"
	objs := append([]client.Object{ownedNS(t, devNS), newPen()}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnceWith(t, c, devNS, cfg)
	sandbox := &v1beta1.Sandbox{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, sandbox); err != nil {
		t.Fatal(err)
	}
	if sandbox.Spec.PodTemplate.Spec.RuntimeClassName == nil || *sandbox.Spec.PodTemplate.Spec.RuntimeClassName != "gvisor" {
		t.Errorf("sandbox runtimeClass = %v, want gvisor", sandbox.Spec.PodTemplate.Spec.RuntimeClassName)
	}
}

func TestReconcileReady(t *testing.T) {
	objs := append([]client.Object{
		ownedNS(t, devNS), newPen(), readySandbox(), svcWithIngress("tobias-smoke.ts.example.net", ""),
	}, devObjects(devNS)...)
	c := newClient(t, objs...)
	res := reconcileOnce(t, c)
	p := getPen(t, c)
	if p.Status.Phase != v1alpha1.PhaseReady || !p.Status.Ready {
		t.Errorf("phase = %q ready = %v, want Ready/true", p.Status.Phase, p.Status.Ready)
	}
	if p.Status.Hostname != "tobias-smoke.ts.example.net" {
		t.Errorf("hostname = %q, want ingress hostname", p.Status.Hostname)
	}
	if p.Status.Node != "storage-1" {
		t.Errorf("node = %q, want storage-1", p.Status.Node)
	}
	if p.Status.ObservedGeneration == 0 {
		t.Error("observedGeneration not set")
	}
	if res.RequeueAfter != requeueReady {
		t.Errorf("RequeueAfter = %v, want %v (periodic refresh keeps status truthful and self-heals)",
			res.RequeueAfter, requeueReady)
	}
}

func TestReconcileIPFallback(t *testing.T) {
	objs := append([]client.Object{
		ownedNS(t, devNS), newPen(), readySandbox(), svcWithIngress("", "100.64.0.9"),
	}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnce(t, c)
	p := getPen(t, c)
	if p.Status.Hostname != "100.64.0.9" {
		t.Errorf("hostname = %q, want ingress ip fallback", p.Status.Hostname)
	}
	if p.Status.Phase != v1alpha1.PhaseReady {
		t.Errorf("phase = %q, want Ready", p.Status.Phase)
	}
}

// The dev-namespace chart names the namespace <namespacePrefix><dev>
// (default "env-<dev>"). The operator derives every object name from the
// Pen's namespace; the herdr.dev/owner label keeps the bare dev identity.
func TestReconcilePrefixedNamespace(t *testing.T) {
	ns := "env-" + devNS
	owned := ownedNS(t, ns)
	owned.Labels[ownerLabelKey] = devNS
	pen := &v1alpha1.Pen{
		ObjectMeta: metav1.ObjectMeta{Name: penName, Namespace: ns, Generation: 1},
		Spec:       v1alpha1.PenSpec{},
	}
	objs := append([]client.Object{owned, pen}, devObjects(ns)...)
	c := newClient(t, objs...)
	res := reconcileOnceIn(t, c, ns)
	ctx := context.Background()

	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, types.NamespacedName{Name: penName + "-home", Namespace: ns}, pvc); err != nil {
		t.Fatalf("pvc not created in %s: %v", ns, err)
	}
	svc := &corev1.Service{}
	if err := c.Get(ctx, types.NamespacedName{Name: penName, Namespace: ns}, svc); err != nil {
		t.Fatalf("service not created in %s: %v", ns, err)
	}
	sandbox := &v1beta1.Sandbox{}
	if err := c.Get(ctx, types.NamespacedName{Name: "env-" + penName, Namespace: ns}, sandbox); err != nil {
		t.Fatalf("sandbox not created in %s: %v", ns, err)
	}

	envs := map[string]string{}
	for _, e := range sandbox.Spec.PodTemplate.Spec.Containers[0].Env {
		envs[e.Name] = e.Value
	}
	if envs["DEV_USER"] != devNS {
		t.Errorf("DEV_USER = %q, want the label value %q (not the namespace name %q)", envs["DEV_USER"], devNS, ns)
	}
	secVol := ""
	cmVol := ""
	for _, v := range sandbox.Spec.PodTemplate.Spec.Volumes {
		if v.Secret != nil {
			secVol = v.Secret.SecretName
		}
		if v.ConfigMap != nil {
			cmVol = v.ConfigMap.Name
		}
	}
	if secVol != "herdr-dev-"+ns {
		t.Errorf("dev-secret volume = %q, want herdr-dev-%s", secVol, ns)
	}
	if cmVol != "herdr-dev-"+ns {
		t.Errorf("dev-config volume = %q, want herdr-dev-%s", cmVol, ns)
	}

	p := &v1alpha1.Pen{}
	if err := c.Get(ctx, types.NamespacedName{Name: penName, Namespace: ns}, p); err != nil {
		t.Fatalf("get pen: %v", err)
	}
	if p.Status.Phase != v1alpha1.PhaseCreating {
		t.Errorf("phase = %q, want Creating (sandbox not ready yet)", p.Status.Phase)
	}
	if res.RequeueAfter == 0 {
		t.Error("expected requeue while creating")
	}
}

func TestReconcileDoesNotMutateExistingPod(t *testing.T) {
	changed := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "env-" + penName, Namespace: devNS},
		Spec: corev1.PodSpec{
			RuntimeClassName: strPtr("kata"),
			Containers: []corev1.Container{{
				Name:  "env",
				Image: "old/image:oldtag",
			}},
		},
	}
	objs := append([]client.Object{
		ownedNS(t, devNS), newPen(), changed, svcWithIngress("", ""),
	}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnce(t, c)
	pod := &corev1.Pod{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, pod); err != nil {
		t.Fatal(err)
	}
	if pod.Spec.Containers[0].Image != "old/image:oldtag" {
		t.Errorf("pod image mutated to %q, want unchanged", pod.Spec.Containers[0].Image)
	}
}

func TestReconcileCreateErrorFails(t *testing.T) {
	builder := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(append([]client.Object{ownedNS(t, devNS), newPen()}, devObjects(devNS)...)...).
		WithStatusSubresource(&v1alpha1.Pen{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*v1beta1.Sandbox); ok {
					return fmt.Errorf("forbidden: no sandbox create in this test")
				}
				return c.Create(ctx, obj, opts...)
			},
		})
	c := builder.Build()
	res := reconcileOnce(t, c)
	p := getPen(t, c)
	if p.Status.Phase != v1alpha1.PhaseFailed {
		t.Errorf("phase = %q, want Failed", p.Status.Phase)
	}
	if p.Status.Message == "" {
		t.Error("Failed pen has no message")
	}
	if res.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want 0 on failure", res.RequeueAfter)
	}
}

func TestReconcileInvalidQuantityFails(t *testing.T) {
	pen := newPen()
	pen.Spec = v1alpha1.PenSpec{CPU: "bogus"}
	objs := append([]client.Object{ownedNS(t, devNS), pen}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnce(t, c)
	p := getPen(t, c)
	if p.Status.Phase != v1alpha1.PhaseFailed {
		t.Errorf("phase = %q, want Failed for invalid quantity", p.Status.Phase)
	}
	if p.Status.Message == "" {
		t.Error("Failed pen has no message")
	}
}

func TestReconcileSpecDefaultsAndOverride(t *testing.T) {
	pen := newPen()
	pen.Spec = v1alpha1.PenSpec{CPU: "8", Memory: "16Gi", HomeSize: "20Gi"}
	objs := append([]client.Object{ownedNS(t, devNS), pen}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnce(t, c)
	sandbox := &v1beta1.Sandbox{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, sandbox); err != nil {
		t.Fatal(err)
	}
	ctr := sandbox.Spec.PodTemplate.Spec.Containers[0]
	if ctr.Resources.Requests.Cpu().String() != "8" {
		t.Errorf("cpu = %s, want spec override 8", ctr.Resources.Requests.Cpu())
	}
	if ctr.Resources.Requests.Memory().String() != "16Gi" {
		t.Errorf("memory = %s, want spec override 16Gi", ctr.Resources.Requests.Memory())
	}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: penName + "-home", Namespace: devNS}, pvc); err != nil {
		t.Fatal(err)
	}
	if pvc.Spec.Resources.Requests.Storage().String() != "20Gi" {
		t.Errorf("pvc size = %s, want spec override 20Gi", pvc.Spec.Resources.Requests.Storage())
	}
}

// TestReconcileCreatesSandbox tests that the reconciler creates a Sandbox
// instead of a Pod with the correct podTemplate.
func TestReconcileCreatesSandbox(t *testing.T) {
	objs := append([]client.Object{ownedNS(t, devNS), newPen()}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnce(t, c)
	ctx := context.Background()

	// Should create sandbox instead of pod
	sandbox := &v1beta1.Sandbox{}
	key := types.NamespacedName{Name: "env-" + penName, Namespace: devNS}
	if err := c.Get(ctx, key, sandbox); err != nil {
		t.Fatalf("sandbox not created: %v", err)
	}

	// Check podTemplate spec matches what ensurePod would have created
	if sandbox.Spec.PodTemplate.Spec.RuntimeClassName != nil {
		t.Errorf("podTemplate.spec.runtimeClassName = %v, want nil for empty runtime class", sandbox.Spec.PodTemplate.Spec.RuntimeClassName)
	}
	ctr := sandbox.Spec.PodTemplate.Spec.Containers[0]
	if ctr.Image != "ghcr.io/caktus/pipen:latest" {
		t.Errorf("podTemplate container image = %q, want cfg default", ctr.Image)
	}
	if ctr.Resources.Requests.Cpu().String() != "4" {
		t.Errorf("podTemplate cpu = %s, want 4", ctr.Resources.Requests.Cpu())
	}
	envs := map[string]string{}
	for _, e := range ctr.Env {
		envs[e.Name] = e.Value
	}
	if envs["DEV_USER"] != devNS {
		t.Errorf("DEV_USER = %q, want %q", envs["DEV_USER"], devNS)
	}

	// Check service is false by default
	if sandbox.Spec.Service != nil && *sandbox.Spec.Service {
		t.Errorf("spec.service = %v, want false or nil", sandbox.Spec.Service)
	}

	// Check operatingMode defaults to Running
	if sandbox.Spec.OperatingMode != v1beta1.SandboxOperatingModeRunning {
		t.Errorf("operatingMode = %q, want Running", sandbox.Spec.OperatingMode)
	}

	// Check no volumeClaimTemplates
	if len(sandbox.Spec.VolumeClaimTemplates) != 0 {
		t.Errorf("volumeClaimTemplates = %v, want empty", sandbox.Spec.VolumeClaimTemplates)
	}

	// Check controller reference
	if len(sandbox.OwnerReferences) != 1 || sandbox.OwnerReferences[0].Kind != "Pen" {
		t.Errorf("sandbox ownerRefs = %+v, want one Pen ownerRef", sandbox.OwnerReferences)
	}
}

// TestReconcileSandboxServiceTrue tests sandbox creation with config.sandboxService = true.
func TestReconcileSandboxServiceTrue(t *testing.T) {
	cfg := testConfig()
	cfg.SandboxService = true
	objs := append([]client.Object{ownedNS(t, devNS), newPen()}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnceWith(t, c, devNS, cfg)
	ctx := context.Background()

	sandbox := &v1beta1.Sandbox{}
	if err := c.Get(ctx, types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, sandbox); err != nil {
		t.Fatalf("sandbox not created: %v", err)
	}

	if sandbox.Spec.Service == nil || !*sandbox.Spec.Service {
		t.Errorf("spec.service = %v, want true", sandbox.Spec.Service)
	}
}

// Helper to create a Sandbox with Ready condition
func readySandbox() *v1beta1.Sandbox {
	sb := &v1beta1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "env-" + penName, Namespace: devNS},
		Spec: v1beta1.SandboxSpec{
			OperatingMode: v1beta1.SandboxOperatingModeRunning,
			SandboxBlueprint: v1beta1.SandboxBlueprint{
				PodTemplate: v1beta1.PodTemplate{
					Spec: corev1.PodSpec{NodeName: "storage-1"},
				},
			},
		},
		Status: v1beta1.SandboxStatus{
			NodeName: "storage-1",
			Conditions: []metav1.Condition{{
				Type:   string(v1beta1.SandboxConditionReady),
				Status: metav1.ConditionTrue,
				Reason: v1beta1.SandboxReasonDependenciesReady,
			}},
		},
	}
	return sb
}

// Helper to create a Sandbox with PodScheduled=False
func unscheduledSandbox() *v1beta1.Sandbox {
	sb := &v1beta1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "env-" + penName, Namespace: devNS},
		Status: v1beta1.SandboxStatus{
			Conditions: []metav1.Condition{{
				Type:    string(v1beta1.SandboxConditionPodScheduled),
				Status:  metav1.ConditionFalse,
				Reason:  "Unschedulable",
				Message: "0/1 nodes are available",
			}},
		},
	}
	return sb
}

// Helper to create a Sandbox with Suspended condition
func suspendedSandbox() *v1beta1.Sandbox {
	sb := &v1beta1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "env-" + penName, Namespace: devNS},
		Spec: v1beta1.SandboxSpec{
			OperatingMode: v1beta1.SandboxOperatingModeSuspended,
		},
		Status: v1beta1.SandboxStatus{
			Conditions: []metav1.Condition{{
				Type:   string(v1beta1.SandboxConditionSuspended),
				Status: metav1.ConditionTrue,
				Reason: v1beta1.SandboxReasonSuspendedPodTerminated,
			}},
		},
	}
	return sb
}

// TestReconcilePausedMapsToSuspended tests that pen.Spec.Paused maps to operatingMode Suspended.
func TestReconcilePausedMapsToSuspended(t *testing.T) {
	pen := newPen()
	pen.Spec.Paused = true
	objs := append([]client.Object{ownedNS(t, devNS), pen}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnce(t, c)
	ctx := context.Background()

	sandbox := &v1beta1.Sandbox{}
	if err := c.Get(ctx, types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, sandbox); err != nil {
		t.Fatalf("sandbox not created: %v", err)
	}

	if sandbox.Spec.OperatingMode != v1beta1.SandboxOperatingModeSuspended {
		t.Errorf("operatingMode = %q, want Suspended", sandbox.Spec.OperatingMode)
	}
}

// TestReconcileTTL tests that pen.Spec.TTL sets sandbox lifecycle.
func TestReconcileTTL(t *testing.T) {
	pen := newPen()
	pen.Spec.TTL = "1h"
	objs := append([]client.Object{ownedNS(t, devNS), pen}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnce(t, c)
	ctx := context.Background()

	sandbox := &v1beta1.Sandbox{}
	if err := c.Get(ctx, types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, sandbox); err != nil {
		t.Fatalf("sandbox not created: %v", err)
	}

	if sandbox.Spec.ShutdownTime == nil {
		t.Error("shutdownTime not set")
	}
	if sandbox.Spec.ShutdownPolicy == nil || *sandbox.Spec.ShutdownPolicy != v1beta1.ShutdownPolicyRetain {
		t.Errorf("shutdownPolicy = %v, want Retain", sandbox.Spec.ShutdownPolicy)
	}
}

// TestReconcileDefaultTTL tests that config.defaultTTL is used when pen.Spec.TTL empty.
func TestReconcileDefaultTTL(t *testing.T) {
	cfg := testConfig()
	cfg.DefaultTTL = "2h"
	pen := newPen()
	objs := append([]client.Object{ownedNS(t, devNS), pen}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnceWith(t, c, devNS, cfg)
	ctx := context.Background()

	sandbox := &v1beta1.Sandbox{}
	if err := c.Get(ctx, types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, sandbox); err != nil {
		t.Fatalf("sandbox not created: %v", err)
	}

	if sandbox.Spec.ShutdownTime == nil {
		t.Error("shutdownTime not set from defaultTTL")
	}
}

// TestReconcilePenTTLOverridesDefault tests pen TTL overrides config default.
func TestReconcilePenTTLOverridesDefault(t *testing.T) {
	cfg := testConfig()
	cfg.DefaultTTL = "2h"
	pen := newPen()
	pen.Spec.TTL = "30m"
	objs := append([]client.Object{ownedNS(t, devNS), pen}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnceWith(t, c, devNS, cfg)
	ctx := context.Background()

	sandbox := &v1beta1.Sandbox{}
	if err := c.Get(ctx, types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, sandbox); err != nil {
		t.Fatalf("sandbox not created: %v", err)
	}

	if sandbox.Spec.ShutdownTime == nil {
		t.Error("shutdownTime not set")
	}
	// Note: can't verify exact duration in unit test without mocking time
}

// TestReconcileInvalidTTLFails tests invalid TTL duration fails.
func TestReconcileInvalidTTLFails(t *testing.T) {
	pen := newPen()
	pen.Spec.TTL = "not-a-duration"
	objs := append([]client.Object{ownedNS(t, devNS), pen}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnce(t, c)
	p := getPen(t, c)
	if p.Status.Phase != v1alpha1.PhaseFailed {
		t.Errorf("phase = %q, want Failed for invalid TTL", p.Status.Phase)
	}
	if !strings.Contains(p.Status.Message, "invalid TTL") {
		t.Errorf("message = %q, want 'invalid TTL'", p.Status.Message)
	}
}

// TestReconcileSuspendedSandboxStatus tests that a suspended sandbox
// reports PhaseSuspended in pen status.
func TestReconcileSuspendedSandboxStatus(t *testing.T) {
	pen := newPen()
	pen.Spec.Paused = true
	suspendedSB := suspendedSandbox()
	objs := append([]client.Object{ownedNS(t, devNS), pen, suspendedSB, svcWithIngress("", "")}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnce(t, c)
	p := getPen(t, c)
	if p.Status.Phase != v1alpha1.PhaseSuspended {
		t.Errorf("phase = %q, want Suspended", p.Status.Phase)
	}
}

// TestReconcileSandboxDeleteRecreatesPod tests that deleting a sandbox
// recreates it with the home PVC reattached.
func TestReconcileSandboxReconcilerErrorFails(t *testing.T) {
	sandbox := &v1beta1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "env-" + penName, Namespace: devNS},
		Status: v1beta1.SandboxStatus{Conditions: []metav1.Condition{{
			Type:    string(v1beta1.SandboxConditionReady),
			Status:  metav1.ConditionFalse,
			Reason:  v1beta1.SandboxReasonReconcilerError,
			Message: "sandbox pod creation failed",
		}}},
	}
	objs := append([]client.Object{ownedNS(t, devNS), newPen(), sandbox}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnce(t, c)
	p := getPen(t, c)
	if p.Status.Phase != v1alpha1.PhaseFailed {
		t.Errorf("phase = %q, want Failed", p.Status.Phase)
	}
	if p.Status.Message != "sandbox pod creation failed" {
		t.Errorf("message = %q, want sandbox error", p.Status.Message)
	}
}

func TestReconcileUnscheduledSandboxCreates(t *testing.T) {
	objs := append([]client.Object{ownedNS(t, devNS), newPen(), unscheduledSandbox()}, devObjects(devNS)...)
	c := newClient(t, objs...)
	res := reconcileOnce(t, c)
	p := getPen(t, c)
	if p.Status.Phase != v1alpha1.PhaseCreating {
		t.Errorf("phase = %q, want Creating", p.Status.Phase)
	}
	if p.Status.Message != "0/1 nodes are available" {
		t.Errorf("message = %q, want scheduler message", p.Status.Message)
	}
	if res.RequeueAfter != requeueCreating {
		t.Errorf("RequeueAfter = %v, want %v", res.RequeueAfter, requeueCreating)
	}
}

func TestReconcileTTLUsesFixedClock(t *testing.T) {
	pen := newPen()
	pen.Spec.TTL = "1h"
	objs := append([]client.Object{ownedNS(t, devNS), pen}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnceWith(t, c, devNS, testConfig())
	sandbox := getSandbox(t, c)
	want := "2026-11-24T09:00:00Z"
	if got := sandbox.Spec.ShutdownTime.UTC().Format(time.RFC3339); got != want {
		t.Errorf("shutdownTime = %q, want %q", got, want)
	}
	if got := sandbox.Annotations[effectiveTTLAnnotation]; got != "1h" {
		t.Errorf("effective TTL = %q, want 1h", got)
	}
}

func TestReconcileTTLChangeResetsShutdownTime(t *testing.T) {
	pen := newPen()
	pen.Spec.TTL = "2h"
	oldTime := metav1.NewTime(time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC))
	sandbox := sandboxWithTTL("1h", &oldTime)
	objs := append([]client.Object{ownedNS(t, devNS), pen, sandbox}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnceWith(t, c, devNS, testConfig())
	got := getSandbox(t, c)
	want := "2026-11-24T10:00:00Z"
	if got.Spec.ShutdownTime.UTC().Format(time.RFC3339) != want {
		t.Errorf("shutdownTime = %v, want %s", got.Spec.ShutdownTime, want)
	}
	if got.Annotations[effectiveTTLAnnotation] != "2h" {
		t.Errorf("effective TTL = %q, want 2h", got.Annotations[effectiveTTLAnnotation])
	}
}

func TestReconcileUnchangedTTLKeepsShutdownTime(t *testing.T) {
	pen := newPen()
	pen.Spec.TTL = "1h"
	oldTime := metav1.NewTime(time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC))
	sandbox := sandboxWithTTL("1h", &oldTime)
	objs := append([]client.Object{ownedNS(t, devNS), pen, sandbox}, devObjects(devNS)...)
	c := newClient(t, objs...)
	for i := 0; i < 2; i++ {
		reconcileOnceAt(t, c, devNS, testConfig(), time.Date(2026, 11, 24, 8+i, 0, 0, 0, time.UTC))
	}
	got := getSandbox(t, c)
	if !got.Spec.ShutdownTime.Equal(&oldTime) {
		t.Errorf("shutdownTime = %v, want unchanged %v", got.Spec.ShutdownTime, oldTime)
	}
	if got.ResourceVersion != sandbox.ResourceVersion {
		t.Errorf("sandbox updated on unchanged TTL: resourceVersion %q, want %q", got.ResourceVersion, sandbox.ResourceVersion)
	}
}

func TestReconcileRemovedTTLClearsLifecycle(t *testing.T) {
	oldTime := metav1.NewTime(time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC))
	sandbox := sandboxWithTTL("1h", &oldTime)
	retain := v1beta1.ShutdownPolicyRetain
	sandbox.Spec.ShutdownPolicy = &retain
	objs := append([]client.Object{ownedNS(t, devNS), newPen(), sandbox}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnceWith(t, c, devNS, testConfig())
	got := getSandbox(t, c)
	if got.Spec.ShutdownTime != nil || got.Spec.ShutdownPolicy != nil {
		t.Errorf("lifecycle = %+v, want empty", got.Spec.Lifecycle)
	}
	if got.Annotations[effectiveTTLAnnotation] != "" {
		t.Errorf("effective TTL = %q, want empty", got.Annotations[effectiveTTLAnnotation])
	}
}

func TestReconcileExpiredSandboxResetsShutdownTimeAtFixedClock(t *testing.T) {
	pen := newPen()
	pen.Spec.TTL = "1h"
	oldTime := metav1.NewTime(time.Date(2026, 11, 24, 7, 0, 0, 0, time.UTC))
	sandbox := sandboxWithTTL("1h", &oldTime)
	sandbox.Status.Conditions = []metav1.Condition{{
		Type: string(v1beta1.SandboxConditionReady), Status: metav1.ConditionFalse, Reason: v1beta1.SandboxReasonExpired,
	}}
	objs := append([]client.Object{ownedNS(t, devNS), pen, sandbox}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnceWith(t, c, devNS, testConfig())
	got := getSandbox(t, c)
	want := "2026-11-24T09:00:00Z"
	if got.Spec.ShutdownTime.UTC().Format(time.RFC3339) != want {
		t.Errorf("shutdownTime = %v, want %s", got.Spec.ShutdownTime, want)
	}
}

func getSandbox(t *testing.T, c client.Client) *v1beta1.Sandbox {
	t.Helper()
	sandbox := &v1beta1.Sandbox{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, sandbox); err != nil {
		t.Fatal(err)
	}
	return sandbox
}

func sandboxWithTTL(ttl string, shutdownTime *metav1.Time) *v1beta1.Sandbox {
	service := false
	return &v1beta1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "env-" + penName, Namespace: devNS,
			Annotations: map[string]string{effectiveTTLAnnotation: ttl},
		},
		Spec: v1beta1.SandboxSpec{
			SandboxBlueprint: v1beta1.SandboxBlueprint{Service: &service},
			OperatingMode:    v1beta1.SandboxOperatingModeRunning,
			Lifecycle:        v1beta1.Lifecycle{ShutdownTime: shutdownTime},
		},
	}
}

func egressPolicy(t *testing.T, c client.Client) *networkingv1.NetworkPolicy {
	t.Helper()
	policy := &networkingv1.NetworkPolicy{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: penName + "-egress", Namespace: devNS}, policy); err != nil {
		t.Fatalf("egress NetworkPolicy not created: %v", err)
	}
	return policy
}

// A pen with spec.egress and configured hosts gets an egress policy that
// selects its pod, allows only DNS and the configured hosts, and is owned
// by the Pen.
func TestReconcileEgressNetworkPolicy(t *testing.T) {
	pen := newPen()
	pen.Spec.Egress = true
	cfg := testConfig()
	cfg.EgressHosts = []string{"llm.example.net", "proxy.golang.org"}
	objs := append([]client.Object{ownedNS(t, devNS), pen}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnceWith(t, c, devNS, cfg)

	policy := egressPolicy(t, c)
	if len(policy.OwnerReferences) != 1 || policy.OwnerReferences[0].Kind != "Pen" || policy.OwnerReferences[0].Name != penName {
		t.Errorf("ownerRefs = %+v, want one Pen controller ref", policy.OwnerReferences)
	}
	if policy.OwnerReferences[0].Controller == nil || !*policy.OwnerReferences[0].Controller {
		t.Error("ownerRef is not a controller ref")
	}
	if got := policy.Spec.PodSelector.MatchLabels; got[ownerLabel] != penName || len(got) != 1 {
		t.Errorf("podSelector = %v, want %s=%s only", got, ownerLabel, penName)
	}
	if len(policy.Spec.PolicyTypes) != 1 || policy.Spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Errorf("policyTypes = %v, want [Egress]", policy.Spec.PolicyTypes)
	}
	if len(policy.Spec.Egress) != 3 {
		t.Fatalf("egress rules = %d, want 3 (DNS + 2 hosts)", len(policy.Spec.Egress))
	}

	// Rule 1 is the kube-dns rule from the dev-namespace chart.
	dns := policy.Spec.Egress[0]
	if len(dns.To) != 1 {
		t.Fatalf("DNS rule peers = %d, want 1", len(dns.To))
	}
	peer := dns.To[0]
	if peer.IPBlock != nil {
		t.Error("DNS rule must select kube-dns pods, not an ipBlock")
	}
	if peer.NamespaceSelector == nil || peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "kube-system" {
		t.Errorf("DNS namespaceSelector = %v, want kubernetes.io/metadata.name=kube-system", peer.NamespaceSelector)
	}
	if peer.PodSelector == nil || peer.PodSelector.MatchLabels["k8s-app"] != "kube-dns" {
		t.Errorf("DNS podSelector = %v, want k8s-app=kube-dns", peer.PodSelector)
	}
	if len(dns.Ports) != 2 {
		t.Fatalf("DNS ports = %d, want 2 (TCP and UDP 53)", len(dns.Ports))
	}
	for i, protocol := range []corev1.Protocol{corev1.ProtocolTCP, corev1.ProtocolUDP} {
		if dns.Ports[i].Protocol == nil || *dns.Ports[i].Protocol != protocol {
			t.Errorf("DNS port %d protocol = %v, want %s", i, dns.Ports[i].Protocol, protocol)
		}
		if dns.Ports[i].Port == nil || dns.Ports[i].Port.IntValue() != 53 {
			t.Errorf("DNS port %d = %v, want 53", i, dns.Ports[i].Port)
		}
	}

	// One ipBlock rule per configured host, in config order.
	for i, host := range []string{"llm.example.net", "proxy.golang.org"} {
		rule := policy.Spec.Egress[i+1]
		if len(rule.To) != 1 || rule.To[0].IPBlock == nil {
			t.Fatalf("rule %d = %+v, want one ipBlock peer", i+1, rule)
		}
		if rule.To[0].IPBlock.CIDR != host {
			t.Errorf("rule %d cidr = %q, want %q", i+1, rule.To[0].IPBlock.CIDR, host)
		}
		if len(rule.Ports) != 0 {
			t.Errorf("rule %d ports = %v, want all ports", i+1, rule.Ports)
		}
	}
}

// A changed host list must replace the old allow rules. An unchanged list
// must not update the policy again.
func TestReconcileEgressPolicyConvergesHostChanges(t *testing.T) {
	pen := newPen()
	pen.Spec.Egress = true
	cfg := testConfig()
	cfg.EgressHosts = []string{"a.example"}
	objs := append([]client.Object{ownedNS(t, devNS), pen}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnceWith(t, c, devNS, cfg)

	created := egressPolicy(t, c)
	if got := created.Spec.Egress[1].To[0].IPBlock.CIDR; got != "a.example" {
		t.Fatalf("initial host cidr = %q, want a.example", got)
	}
	reconcileOnceWith(t, c, devNS, cfg)
	stable := egressPolicy(t, c)
	if stable.ResourceVersion != created.ResourceVersion {
		t.Errorf("unchanged hosts updated policy: resourceVersion %q, want %q", stable.ResourceVersion, created.ResourceVersion)
	}

	cfg.EgressHosts = []string{"b.example"}
	reconcileOnceWith(t, c, devNS, cfg)
	updated := egressPolicy(t, c)
	if len(updated.Spec.Egress) != 2 {
		t.Fatalf("egress rules = %d, want DNS and one host", len(updated.Spec.Egress))
	}
	if got := updated.Spec.Egress[1].To[0].IPBlock.CIDR; got != "b.example" {
		t.Errorf("replacement host cidr = %q, want b.example", got)
	}
	if updated.ResourceVersion == created.ResourceVersion {
		t.Error("changed hosts did not update policy resourceVersion")
	}
}

func TestReconcileNoEgressNetworkPolicyByDefault(t *testing.T) {
	pen := newPen()
	cfg := testConfig()
	cfg.EgressHosts = []string{"llm.example.net"}
	objs := append([]client.Object{ownedNS(t, devNS), pen}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnceWith(t, c, devNS, cfg)
	policy := &networkingv1.NetworkPolicy{}
	err := c.Get(context.Background(), types.NamespacedName{Name: penName + "-egress", Namespace: devNS}, policy)
	if !apierrors.IsNotFound(err) {
		t.Errorf("egress policy with spec.egress off: err = %v, want NotFound", err)
	}
}

func TestReconcileNoEgressNetworkPolicyWithoutHosts(t *testing.T) {
	pen := newPen()
	pen.Spec.Egress = true
	objs := append([]client.Object{ownedNS(t, devNS), pen}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnce(t, c)
	policy := &networkingv1.NetworkPolicy{}
	err := c.Get(context.Background(), types.NamespacedName{Name: penName + "-egress", Namespace: devNS}, policy)
	if !apierrors.IsNotFound(err) {
		t.Errorf("egress policy with empty egressHosts: err = %v, want NotFound", err)
	}
}

func TestReconcileEgressPolicyDeletedWhenFlagOff(t *testing.T) {
	pen := newPen()
	existing := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: penName + "-egress", Namespace: devNS},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{ownerLabel: penName}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
		},
	}
	cfg := testConfig()
	cfg.EgressHosts = []string{"llm.example.net"}
	objs := append([]client.Object{ownedNS(t, devNS), pen, existing}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnceWith(t, c, devNS, cfg)
	policy := &networkingv1.NetworkPolicy{}
	err := c.Get(context.Background(), types.NamespacedName{Name: penName + "-egress", Namespace: devNS}, policy)
	if !apierrors.IsNotFound(err) {
		t.Errorf("egress policy after spec.egress off: err = %v, want NotFound (deleted)", err)
	}
}

func TestReconcileEgressPolicyDeletedWhenHostsEmpty(t *testing.T) {
	pen := newPen()
	pen.Spec.Egress = true
	existing := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: penName + "-egress", Namespace: devNS},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{ownerLabel: penName}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
		},
	}
	objs := append([]client.Object{ownedNS(t, devNS), pen, existing}, devObjects(devNS)...)
	c := newClient(t, objs...)
	reconcileOnce(t, c)
	policy := &networkingv1.NetworkPolicy{}
	err := c.Get(context.Background(), types.NamespacedName{Name: penName + "-egress", Namespace: devNS}, policy)
	if !apierrors.IsNotFound(err) {
		t.Errorf("egress policy after egressHosts emptied: err = %v, want NotFound (deleted)", err)
	}
}

func TestReconcileSandboxDeleteRecreatesPod(t *testing.T) {
	pen := newPen()
	pen.Spec.TTL = "1h"
	// Create a sandbox that will be deleted
	sandbox := &v1beta1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "env-" + penName, Namespace: devNS},
		Spec: v1beta1.SandboxSpec{
			OperatingMode: v1beta1.SandboxOperatingModeRunning,
			SandboxBlueprint: v1beta1.SandboxBlueprint{
				PodTemplate: v1beta1.PodTemplate{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "env", Image: "old/image:old"}},
					},
				},
			},
		},
	}
	objs := append([]client.Object{ownedNS(t, devNS), pen, sandbox}, devObjects(devNS)...)
	c := newClient(t, objs...)

	// Delete the sandbox
	if err := c.Delete(context.Background(), sandbox); err != nil {
		t.Fatalf("delete sandbox: %v", err)
	}

	// Reconcile should recreate it
	reconcileOnce(t, c)

	// Verify sandbox was recreated with home PVC volume
	newSandbox := &v1beta1.Sandbox{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, newSandbox); err != nil {
		t.Fatalf("sandbox not recreated: %v", err)
	}

	// Check that home volume references the home PVC
	hasHomeVolume := false
	for _, vol := range newSandbox.Spec.PodTemplate.Spec.Volumes {
		if vol.Name == "home" && vol.PersistentVolumeClaim != nil && vol.PersistentVolumeClaim.ClaimName == penName+"-home" {
			hasHomeVolume = true
			break
		}
	}
	if !hasHomeVolume {
		t.Error("recreated sandbox missing home PVC volume")
	}

	// Check that home PVC still exists (wasn't deleted)
	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: penName + "-home", Namespace: devNS}, pvc); err != nil {
		t.Errorf("home PVC missing after sandbox recreation: %v", err)
	}
}
