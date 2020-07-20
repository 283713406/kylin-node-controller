package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KylinNode describes a Network resource
type KylinNode struct {
	// TypeMeta is the metadata for the resource, like kind and apiversion
	metav1.TypeMeta `json:",inline"`
	// ObjectMeta contains the metadata for the particular object, including
	// things like...
	//  - name
	//  - namespace
	//  - self link
	//  - labels
	//  - ... etc ...
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// KylinNodeSpec is the custom resource spec
	Spec KylinNodeSpec `json:"spec"`

	// KylinNodeStatus is the custom resource status
	Status KylinNodeStatus `json:"status,omitempty"`
}

// KylinNodeSpec is the spec for a Network resource
type KylinNodeSpec struct {
	// Name Address User and Password are example custom spec fields
	//
	// this is where you would put your custom resource data
	Name     string `json:"name"`
	Address  string `json:"address"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// KylinNodeStatus is the spec for a Network resource
type KylinNodeStatus struct {
	// Address User and Password are example custom spec fields
	//
	// this is where you would put your custom resource data
	Phase    KylinNodePhase `json:"phase,omitempty"`
	Message  string `json:"message,omitempty"`
}

// KylinNodePhase is a label for the condition of a kylinnode at the current time.
type KylinNodePhase string

// These are the valid statuses of pods.
const (
	// KylinNodePending means the node has been created/added by the system, but not configured.
	KylinNodePending KylinNodePhase = "Pending"
	// KylinNodeRunning means the node has been configured and has Kubernetes components running.
	KylinNodeSuccess KylinNodePhase = "Success"
	// KylinNodeFailed means the node has been created/added by the system, but failed configure.
	KylinNodeFailed KylinNodePhase = "Failed"
	// KylinNodeUnknown means that for some reason the state of the node could not be configured
	KylinNodeUnknown KylinNodePhase = "Unknown"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KylinNodeList is a list of KylinNode resources
type KylinNodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []KylinNode `json:"items"`
}
