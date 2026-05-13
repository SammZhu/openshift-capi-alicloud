package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AlibabaCloudClusterTemplateSpec defines the desired state of AlibabaCloudClusterTemplate.
type AlibabaCloudClusterTemplateSpec struct {
	Template AlibabaCloudClusterTemplateResource `json:"template"`
}

// AlibabaCloudClusterTemplateResource describes the data needed to create an AlibabaCloudCluster from a template.
type AlibabaCloudClusterTemplateResource struct {
	Spec AlibabaCloudClusterSpec `json:"spec"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=alibabacloudclustertemplates,scope=Namespaced,categories=cluster-api
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// AlibabaCloudClusterTemplate is the Schema for the alibabacloudclustertemplates API.
type AlibabaCloudClusterTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec AlibabaCloudClusterTemplateSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// AlibabaCloudClusterTemplateList contains a list of AlibabaCloudClusterTemplate.
type AlibabaCloudClusterTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AlibabaCloudClusterTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AlibabaCloudClusterTemplate{}, &AlibabaCloudClusterTemplateList{})
}
