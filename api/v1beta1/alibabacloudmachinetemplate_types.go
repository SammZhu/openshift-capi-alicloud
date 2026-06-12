package v1beta1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AlibabaCloudMachineTemplateSpec defines the desired state of AlibabaCloudMachineTemplate.
type AlibabaCloudMachineTemplateSpec struct {
	Template AlibabaCloudMachineTemplateResource `json:"template"`
}

// AlibabaCloudMachineTemplateResource describes the data needed to create an AlibabaCloudMachine from a template.
type AlibabaCloudMachineTemplateResource struct {
	Spec AlibabaCloudMachineSpec `json:"spec"`
}

// AlibabaCloudMachineTemplateStatus defines the observed state of AlibabaCloudMachineTemplate.
type AlibabaCloudMachineTemplateStatus struct {
	// Capacity is the node capacity a Machine created from this template provides
	// (cpu, memory, pods, ephemeral-storage), resolved from the template's
	// spec.template.spec.instanceType. Cluster Autoscaler's clusterapi provider
	// reads it to size a pool when scaling UP FROM ZERO (the CAPI scale-from-zero
	// contract) — there is no live Node to copy from. Populated by the controller.
	// +optional
	Capacity corev1.ResourceList `json:"capacity,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=alibabacloudmachinetemplates,scope=Namespaced,categories=cluster-api
// +kubebuilder:metadata:labels=cluster.x-k8s.io/v1beta2=v1beta1
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// AlibabaCloudMachineTemplate is the Schema for the alibabacloudmachinetemplates API.
type AlibabaCloudMachineTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AlibabaCloudMachineTemplateSpec   `json:"spec,omitempty"`
	Status AlibabaCloudMachineTemplateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AlibabaCloudMachineTemplateList contains a list of AlibabaCloudMachineTemplate.
type AlibabaCloudMachineTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AlibabaCloudMachineTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AlibabaCloudMachineTemplate{}, &AlibabaCloudMachineTemplateList{})
}
