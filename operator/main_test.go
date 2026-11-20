package main

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	discoveryfake "k8s.io/client-go/discovery/fake"
	clientgotesting "k8s.io/client-go/testing"
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
