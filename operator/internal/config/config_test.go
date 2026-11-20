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
