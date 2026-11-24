// Package v1alpha1 contains the Pens API of the pipen operator.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Phase is the lifecycle state of a Pen.
type Phase string

const (
	// PhasePending: prerequisites (the herdr-dev-<ns> Secret/ConfigMap)
	// are missing; the reconciler retries with backoff.
	PhasePending Phase = "Pending"
	// PhaseCreating: the Pod and/or tailnet Service are coming up.
	PhaseCreating Phase = "Creating"
	// PhaseReady: the pod is running and the Service has a tailnet
	// hostname (status.hostname).
	PhaseReady Phase = "Ready"
	// PhaseFailed: reconciliation hit an error (status.message says why).
	PhaseFailed Phase = "Failed"
	// PhaseSuspended: the pen is paused (operatingMode = Suspended).
	PhaseSuspended Phase = "Suspended"
)

// PenSpec is the desired state of a pen (a kata dev environment).
type PenSpec struct {
	// Image is the full container image reference. Empty means the
	// operator's default image from its config.
	// +optional
	Image string `json:"image,omitempty"`
	// HomeSize is the size of the mirrored home PVC (default 10Gi).
	// +optional
	HomeSize string `json:"homeSize,omitempty"`
	// CPU is the pod cpu request and limit (default 4).
	// +optional
	CPU string `json:"cpu,omitempty"`
	// Memory is the pod memory request and limit (default 8Gi).
	// +optional
	Memory string `json:"memory,omitempty"`
	// Paused stops the pen's compute while keeping PVC and Service.
	// +optional
	Paused bool `json:"paused,omitempty"`
	// TTL is a duration like "8h" after which the pen shuts itself down.
	// +optional
	TTL string `json:"ttl,omitempty"`
	// Egress enables a NetworkPolicy that restricts egress to approved
	// destinations.
	// +optional
	Egress bool `json:"egress,omitempty"`
}

// PenStatus is the observed state of a pen.
type PenStatus struct {
	// ObservedGeneration is the .metadata.generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Phase is the lifecycle state.
	// +optional
	Phase Phase `json:"phase,omitempty"`
	// Image is the resolved container image, the same reference in the
	// pod spec; set on every status write.
	// +optional
	Image string `json:"image,omitempty"`
	// Ready is true when ssh via status.hostname works.
	Ready bool `json:"ready"`
	// Hostname is the tailnet-assigned hostname (or IP) of the pen.
	// +optional
	Hostname string `json:"hostname,omitempty"`
	// Node is the node the pod landed on.
	// +optional
	Node string `json:"node,omitempty"`
	// Message carries a human-readable explanation for non-Ready states.
	// +optional
	Message string `json:"message,omitempty"`
	// ShutdownAt is the time after which the pen auto-shuts down.
	// +optional
	ShutdownAt *metav1.Time `json:"shutdownAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="PHASE",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="HOSTNAME",type=string,JSONPath=`.status.hostname`
// +kubebuilder:printcolumn:name="NODE",type=string,JSONPath=`.status.node`

// Pen is the Schema for the pens API. It requests one kata dev
// environment in its namespace; the operator brings the PVC, pod and
// tailnet-exposed Service up and reports the result in status.
type Pen struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PenSpec   `json:"spec,omitempty"`
	Status PenStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PenList contains a list of Pen.
type PenList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Pen `json:"items"`
}
