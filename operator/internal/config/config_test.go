package config

import (
	"reflect"
	"testing"
)

func TestFromDataFull(t *testing.T) {
	cfg, err := FromData(map[string]string{
		"image":              "ghcr.io/caktus/pipen:20261121-pi1.6.0",
		"homeSize":           "20Gi",
		"cpu":                "2",
		"memory":             "4Gi",
		"nodeSelector":       `{"openebs.io/engine":"mayastor"}`,
		"storageClass":       "mayastor-2",
		"serviceAnnotations": `{"tailscale.com/expose":"true"}`,
		"tailnetDomain":      "ts.example.net",
		"imagePullPolicy":    "Always",
		"loadBalancerClass":  "tailscale",
		"runtimeClass":       "kata",
	})
	if err != nil {
		t.Fatalf("FromData: %v", err)
	}
	want := Config{
		Image:              "ghcr.io/caktus/pipen:20261121-pi1.6.0",
		HomeSize:           "20Gi",
		CPU:                "2",
		Memory:             "4Gi",
		NodeSelector:       map[string]string{"openebs.io/engine": "mayastor"},
		StorageClass:       "mayastor-2",
		ServiceAnnotations: map[string]string{"tailscale.com/expose": "true"},
		TailnetDomain:      "ts.example.net",
		ImagePullPolicy:    "Always",
		LoadBalancerClass:  "tailscale",
		RuntimeClass:       "kata",
		DefaultTTL:         "",
		SandboxService:     false,
		EgressHosts:        []string{},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("cfg = %+v, want %+v", cfg, want)
	}
}

func TestFromDataMinimal(t *testing.T) {
	cfg, err := FromData(map[string]string{"image": "ghcr.io/caktus/pipen:latest"})
	if err != nil {
		t.Fatalf("FromData: %v", err)
	}
	if cfg.HomeSize != "10Gi" || cfg.CPU != "4" || cfg.Memory != "8Gi" {
		t.Errorf("defaults not applied: homeSize=%q cpu=%q memory=%q", cfg.HomeSize, cfg.CPU, cfg.Memory)
	}
	if cfg.ImagePullPolicy != "IfNotPresent" {
		t.Errorf("imagePullPolicy = %q, want IfNotPresent", cfg.ImagePullPolicy)
	}
	if cfg.LoadBalancerClass != "tailscale" {
		t.Errorf("loadBalancerClass = %q, want tailscale", cfg.LoadBalancerClass)
	}
	if cfg.RuntimeClass != "" {
		t.Errorf("runtimeClass = %q, want empty", cfg.RuntimeClass)
	}
}

func TestFromDataCustomRuntimeClass(t *testing.T) {
	cfg, err := FromData(map[string]string{
		"image":        "ghcr.io/caktus/pipen:latest",
		"runtimeClass": "gvisor",
	})
	if err != nil {
		t.Fatalf("FromData: %v", err)
	}
	if cfg.RuntimeClass != "gvisor" {
		t.Errorf("runtimeClass = %q, want gvisor", cfg.RuntimeClass)
	}
}

func TestFromDataEmptyRuntimeClass(t *testing.T) {
	cfg, err := FromData(map[string]string{"image": "ghcr.io/caktus/pipen:latest", "runtimeClass": ""})
	if err != nil {
		t.Fatalf("FromData: %v", err)
	}
	if cfg.RuntimeClass != "" {
		t.Errorf("runtimeClass = %q, want empty (no runtime class)", cfg.RuntimeClass)
	}
}

func TestFromDataInvalidNodeSelector(t *testing.T) {
	if _, err := FromData(map[string]string{"image": "x", "nodeSelector": "{not json"}); err == nil {
		t.Fatal("expected error for invalid nodeSelector JSON")
	}
}

func TestFromDataInvalidAnnotations(t *testing.T) {
	if _, err := FromData(map[string]string{"image": "x", "serviceAnnotations": "[1,2"}); err == nil {
		t.Fatal("expected error for invalid serviceAnnotations JSON")
	}
}

func TestFromDataMissingImage(t *testing.T) {
	if _, err := FromData(map[string]string{}); err == nil {
		t.Fatal("expected error when image is missing")
	}
}

func TestFromDataWithNewKeys(t *testing.T) {
	cfg, err := FromData(map[string]string{
		"image":          "ghcr.io/caktus/pipen:latest",
		"defaultTTL":     "8h",
		"sandboxService": "true",
		"egressHosts":    "example.com,api.example.org",
	})
	if err != nil {
		t.Fatalf("FromData: %v", err)
	}
	if cfg.DefaultTTL != "8h" {
		t.Errorf("DefaultTTL = %q, want \"8h\"", cfg.DefaultTTL)
	}
	if !cfg.SandboxService {
		t.Errorf("SandboxService = false, want true")
	}
	if len(cfg.EgressHosts) != 2 || cfg.EgressHosts[0] != "example.com" || cfg.EgressHosts[1] != "api.example.org" {
		t.Errorf("EgressHosts = %v, want [\"example.com\" \"api.example.org\"]", cfg.EgressHosts)
	}
}

func TestFromDataNewKeysDefaults(t *testing.T) {
	cfg, err := FromData(map[string]string{
		"image": "ghcr.io/caktus/pipen:latest",
	})
	if err != nil {
		t.Fatalf("FromData: %v", err)
	}
	if cfg.DefaultTTL != "" {
		t.Errorf("DefaultTTL = %q, want empty (no auto-shutdown)", cfg.DefaultTTL)
	}
	if cfg.SandboxService {
		t.Errorf("SandboxService = true, want false (default)")
	}
	if len(cfg.EgressHosts) != 0 {
		t.Errorf("EgressHosts = %v, want empty slice", cfg.EgressHosts)
	}
}

func TestFromDataSandboxServiceFalse(t *testing.T) {
	cfg, err := FromData(map[string]string{
		"image":          "ghcr.io/caktus/pipen:latest",
		"sandboxService": "false",
	})
	if err != nil {
		t.Fatalf("FromData: %v", err)
	}
	if cfg.SandboxService {
		t.Errorf("SandboxService = true, want false")
	}
}

func TestFromDataSandboxServiceInvalid(t *testing.T) {
	if _, err := FromData(map[string]string{
		"image":          "ghcr.io/caktus/pipen:latest",
		"sandboxService": "invalid",
	}); err == nil {
		t.Fatal("expected error for invalid sandboxService value")
	}
}

func TestFromDataEgressHostsEmptyString(t *testing.T) {
	cfg, err := FromData(map[string]string{
		"image":       "ghcr.io/caktus/pipen:latest",
		"egressHosts": "",
	})
	if err != nil {
		t.Fatalf("FromData: %v", err)
	}
	if len(cfg.EgressHosts) != 0 {
		t.Errorf("EgressHosts = %v, want empty slice", cfg.EgressHosts)
	}
}
