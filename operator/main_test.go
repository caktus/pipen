package main

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discoveryfake "k8s.io/client-go/discovery/fake"
	clientgotesting "k8s.io/client-go/testing"

	v1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"

	v1alpha1 "github.com/caktus/pipen/operator/api/v1alpha1"
)

func TestCRDGroupAvailablePresent(t *testing.T) {
	d := &discoveryfake.FakeDiscovery{
		Fake: &clientgotesting.Fake{
			Resources: []*metav1.APIResourceList{{GroupVersion: "pens.pipen.dev/v1alpha1"}},
		},
	}
	ok, err := crdGroupAvailable(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected pens.pipen.dev to be present")
	}
}

func TestCRDGroupAvailableMissing(t *testing.T) {
	d := &discoveryfake.FakeDiscovery{Fake: &clientgotesting.Fake{}}
	ok, err := crdGroupAvailable(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected pens.pipen.dev to be absent")
	}
}

func TestNewSchemeRegistersPenAndSandbox(t *testing.T) {
	s, err := newScheme()
	if err != nil {
		t.Fatal(err)
	}
	gvks := []schema.GroupVersionKind{
		v1alpha1.GroupVersion.WithKind("Pen"),
		v1beta1.GroupVersion.WithKind("Sandbox"),
	}
	for _, gvk := range gvks {
		if !s.Recognizes(gvk) {
			t.Errorf("scheme does not recognise %v", gvk)
		}
	}
}
