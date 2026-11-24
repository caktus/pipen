// Package config loads the operator's cluster-shape settings from the
// pipen-operator-config ConfigMap (written by the pipen-operator chart).
package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ConfigMapName is the ConfigMap the operator reads for its config, in the
// operator's own namespace.
const ConfigMapName = "pipen-operator-config"

// Config holds the cluster-shape defaults the operator applies to Pens.
type Config struct {
	// Image is the default pod image (Pen spec.image overrides per pen).
	Image string
	// HomeSize is the default home PVC size.
	HomeSize string
	// CPU is the default pod cpu request and limit.
	CPU string
	// Memory is the default pod memory request and limit.
	Memory string
	// NodeSelector pins pods to nodes that can serve the volume.
	NodeSelector map[string]string
	// StorageClass is the home PVC storage class.
	StorageClass string
	// ServiceAnnotations are set on each pen Service (tailscale exposure).
	ServiceAnnotations map[string]string
	// TailnetDomain is informational (printed in logs, not required).
	TailnetDomain string
	// ImagePullPolicy applies unless the image tag is `latest` (then Always).
	ImagePullPolicy string
	// LoadBalancerClass is set on each pen Service (tailscale).
	LoadBalancerClass string
	// RuntimeClass is the pod runtime class. Empty means no runtime class.
	RuntimeClass string
	// DefaultTTL is the default pen TTL (duration like "8h"). Empty means no
	// auto-shutdown.
	DefaultTTL string
	// SandboxService controls whether the agent-sandbox controller creates
	// a Service for each sandbox (in addition to the tailscale Service).
	SandboxService bool
	// EgressHosts are allowed FQDNs for egress (comma-separated).
	EgressHosts []string
}

// FromData builds a Config from ConfigMap data keys, applying defaults.
func FromData(data map[string]string) (Config, error) {
	cfg := Config{
		HomeSize:           "10Gi",
		CPU:                "4",
		Memory:             "8Gi",
		NodeSelector:       map[string]string{},
		ServiceAnnotations: map[string]string{},
		ImagePullPolicy:    "IfNotPresent",
		LoadBalancerClass:  "tailscale",
		RuntimeClass:       "",
		DefaultTTL:         "",
		SandboxService:     false,
		EgressHosts:        []string{},
	}
	for k, v := range data {
		switch k {
		case "image":
			cfg.Image = v
		case "homeSize":
			cfg.HomeSize = v
		case "cpu":
			cfg.CPU = v
		case "memory":
			cfg.Memory = v
		case "tailnetDomain":
			cfg.TailnetDomain = v
		case "imagePullPolicy":
			cfg.ImagePullPolicy = v
		case "loadBalancerClass":
			cfg.LoadBalancerClass = v
		case "runtimeClass":
			cfg.RuntimeClass = v
		case "storageClass":
			cfg.StorageClass = v
		case "nodeSelector":
			if err := json.Unmarshal([]byte(v), &cfg.NodeSelector); err != nil {
				return cfg, fmt.Errorf("nodeSelector: %w", err)
			}
		case "serviceAnnotations":
			if err := json.Unmarshal([]byte(v), &cfg.ServiceAnnotations); err != nil {
				return cfg, fmt.Errorf("serviceAnnotations: %w", err)
			}
		case "defaultTTL":
			cfg.DefaultTTL = v
		case "sandboxService":
			b, err := strconv.ParseBool(v)
			if err != nil {
				return cfg, fmt.Errorf("sandboxService: %w", err)
			}
			cfg.SandboxService = b
		case "egressHosts":
			// Blank entries (trailing commas, whitespace-only values) must
			// not become empty ipBlock CIDRs in the egress NetworkPolicy.
			cfg.EgressHosts = []string{}
			for _, host := range strings.Split(v, ",") {
				if host = strings.TrimSpace(host); host != "" {
					cfg.EgressHosts = append(cfg.EgressHosts, host)
				}
			}
		default:
			// Unknown keys are ignored: the chart may add informational
			// values (e.g. tailnetDomain echoes) without breaking the
			// running operator.
		}
	}
	if cfg.Image == "" {
		return cfg, fmt.Errorf("config key \"image\" is required")
	}
	return cfg, nil
}
