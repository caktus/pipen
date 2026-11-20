package v1alpha1

import (
	"os"
	"path/filepath"
	"testing"

	"sigs.k8s.io/yaml"
)

// crdPath points at the generated CRD manifest in the operator chart.
func crdPath() string {
	return filepath.Join("..", "..", "..", "charts", "pipen-operator", "templates", "crd.yaml")
}

func loadCRD(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(crdPath())
	if err != nil {
		t.Fatalf("cannot read generated CRD %s: %v", crdPath(), err)
	}
	var crd map[string]any
	if err := yaml.Unmarshal(b, &crd); err != nil {
		t.Fatalf("generated CRD is not valid YAML: %v", err)
	}
	return crd
}

func firstVersion(t *testing.T, crd map[string]any) map[string]any {
	t.Helper()
	spec, _ := crd["spec"].(map[string]any)
	versions, _ := spec["versions"].([]any)
	if len(versions) != 1 {
		t.Fatalf("expected exactly 1 version, got %d", len(versions))
	}
	v, _ := versions[0].(map[string]any)
	if v["name"] != "v1alpha1" {
		t.Fatalf("expected version v1alpha1, got %v", v["name"])
	}
	return v
}

func TestCRDGroupKindAndScope(t *testing.T) {
	crd := loadCRD(t)
	if crd["apiVersion"] != "apiextensions.k8s.io/v1" {
		t.Errorf("apiVersion = %v, want apiextensions.k8s.io/v1", crd["apiVersion"])
	}
	if crd["kind"] != "CustomResourceDefinition" {
		t.Errorf("kind = %v, want CustomResourceDefinition", crd["kind"])
	}
	spec, _ := crd["spec"].(map[string]any)
	if spec["group"] != "pens.pipen.dev" {
		t.Errorf("group = %v, want pens.pipen.dev", spec["group"])
	}
	if spec["scope"] != "Namespaced" {
		t.Errorf("scope = %v, want Namespaced", spec["scope"])
	}
	if names, _ := spec["names"].(map[string]any); names["kind"] != "Pen" || names["plural"] != "pens" {
		t.Errorf("names = %v, want kind Pen / plural pens", names)
	}
}

func TestCRDStatusSubresourceAndFields(t *testing.T) {
	v := firstVersion(t, loadCRD(t))
	if subres, _ := v["subresources"].(map[string]any); subres == nil || subres["status"] == nil {
		t.Fatal("status subresource not declared")
	}
	schema, _ := v["schema"].(map[string]any)
	opens, _ := schema["openAPIV3Schema"].(map[string]any)
	props, _ := opens["properties"].(map[string]any)
	specProps, _ := props["spec"].(map[string]any)
	specFields, _ := specProps["properties"].(map[string]any)
	for _, f := range []string{"image", "homeSize", "cpu", "memory"} {
		if _, ok := specFields[f]; !ok {
			t.Errorf("spec field %q missing from CRD schema", f)
		}
	}
	statusProps, _ := props["status"].(map[string]any)
	statusFields, _ := statusProps["properties"].(map[string]any)
	for _, f := range []string{"observedGeneration", "phase", "ready", "hostname", "node", "message"} {
		if _, ok := statusFields[f]; !ok {
			t.Errorf("status field %q missing from CRD schema", f)
		}
	}
}

func TestCRDPrinterColumns(t *testing.T) {
	v := firstVersion(t, loadCRD(t))
	cols, _ := v["additionalPrinterColumns"].([]any)
	if len(cols) != 3 {
		t.Fatalf("expected 3 printer columns, got %d", len(cols))
	}
	want := map[string]string{"PHASE": ".status.phase", "HOSTNAME": ".status.hostname", "NODE": ".status.node"}
	seen := 0
	for _, c := range cols {
		cm, _ := c.(map[string]any)
		for name, jsonPath := range want {
			if cm["name"] == name && cm["jsonPath"] == jsonPath {
				seen++
			}
		}
	}
	if seen != 3 {
		t.Errorf("printer columns do not match PHASE/HOSTNAME/NODE on .status.*, got %v", cols)
	}
}
