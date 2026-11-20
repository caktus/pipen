package pen

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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
		RuntimeClass:      "kata",
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
	r := &Reconciler{Client: c, Reader: c, Scheme: testScheme(t), Config: cfg}
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

	pod := &corev1.Pod{}
	if err := c.Get(ctx, types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, pod); err != nil {
		t.Fatalf("pod not created: %v", err)
	}
	if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != "kata" {
		t.Errorf("pod runtimeClass = %v, want kata", pod.Spec.RuntimeClassName)
	}
	sec := pod.Spec.Containers[0].SecurityContext
	if sec == nil || sec.Privileged == nil || !*sec.Privileged {
		t.Error("pod container not privileged")
	}
	if pod.Spec.NodeSelector["openebs.io/engine"] != "mayastor" {
		t.Errorf("pod nodeSelector = %v, want openebs.io/engine=mayastor", pod.Spec.NodeSelector)
	}
	ctr := pod.Spec.Containers[0]
	if ctr.Image != "ghcr.io/caktus/pipen:latest" {
		t.Errorf("pod image = %q, want cfg default", ctr.Image)
	}
	if ctr.ImagePullPolicy != corev1.PullAlways {
		t.Errorf("pod pullPolicy = %q, want Always for latest tag", ctr.ImagePullPolicy)
	}
	if ctr.Resources.Requests.Cpu().String() != "4" || ctr.Resources.Limits.Cpu().String() != "4" {
		t.Errorf("pod cpu req/lim wrong: %+v", ctr.Resources)
	}
	if ctr.Resources.Requests.Memory().String() != "8Gi" || ctr.Resources.Limits.Memory().String() != "8Gi" {
		t.Errorf("pod memory req/lim wrong: %+v", ctr.Resources)
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
	for _, v := range pod.Spec.Volumes {
		vols[v.Name] = true
	}
	if !vols["home"] || !vols["dev-secret"] || !vols["dev-config"] {
		t.Errorf("volumes = %v", vols)
	}
	if len(pod.OwnerReferences) != 1 || pod.OwnerReferences[0].Kind != "Pen" {
		t.Errorf("pod ownerRefs = %+v, want one Pen ownerRef", pod.OwnerReferences)
	}
	if pod.Labels["herdr.dev/env"] != penName {
		t.Errorf("pod label herdr.dev/env = %q", pod.Labels["herdr.dev/env"])
	}

	p := getPen(t, c)
	if p.Status.Phase != v1alpha1.PhaseCreating {
		t.Errorf("phase = %q, want Creating (pod not ready yet)", p.Status.Phase)
	}
	if p.Status.Ready {
		t.Error("ready = true, want false")
	}
	if res.RequeueAfter == 0 {
		t.Error("expected requeue while creating")
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
	pod := &corev1.Pod{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, pod); err != nil {
		t.Fatal(err)
	}
	if pod.Spec.RuntimeClassName != nil {
		t.Errorf("pod runtimeClass = %q, want none", *pod.Spec.RuntimeClassName)
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
	pod := &corev1.Pod{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, pod); err != nil {
		t.Fatal(err)
	}
	if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != "gvisor" {
		t.Errorf("pod runtimeClass = %v, want gvisor", pod.Spec.RuntimeClassName)
	}
}

func TestReconcileReady(t *testing.T) {
	objs := append([]client.Object{
		ownedNS(t, devNS), newPen(), readyPod(), svcWithIngress("tobias-smoke.ts.example.net", ""),
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
		ownedNS(t, devNS), newPen(), readyPod(), svcWithIngress("", "100.64.0.9"),
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
	pod := &corev1.Pod{}
	if err := c.Get(ctx, types.NamespacedName{Name: "env-" + penName, Namespace: ns}, pod); err != nil {
		t.Fatalf("pod not created in %s: %v", ns, err)
	}

	envs := map[string]string{}
	for _, e := range pod.Spec.Containers[0].Env {
		envs[e.Name] = e.Value
	}
	if envs["DEV_USER"] != ns {
		t.Errorf("DEV_USER = %q, want the namespace name %q", envs["DEV_USER"], ns)
	}
	secVol := ""
	cmVol := ""
	for _, v := range pod.Spec.Volumes {
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
		t.Errorf("phase = %q, want Creating (pod not ready yet)", p.Status.Phase)
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
				if _, ok := obj.(*corev1.Pod); ok {
					return fmt.Errorf("forbidden: no raw pod create in this test")
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
	pod := &corev1.Pod{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "env-" + penName, Namespace: devNS}, pod); err != nil {
		t.Fatal(err)
	}
	ctr := pod.Spec.Containers[0]
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

// helper to avoid importing interceptor at file top in case of version drift
