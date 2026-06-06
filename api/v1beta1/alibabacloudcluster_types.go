package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

const (
	// ClusterFinalizer allows AlibabaCloudClusterReconciler to clean up cloud resources
	// before removing the AlibabaCloudCluster object from the API server.
	ClusterFinalizer = "alibabacloudcluster.infrastructure.cluster.x-k8s.io"
)

// AlibabaCloudClusterSpec defines the desired state of AlibabaCloudCluster.
type AlibabaCloudClusterSpec struct {
	// Region is the Alibaba Cloud region where the cluster is deployed.
	// +kubebuilder:validation:Required
	Region string `json:"region"`

	// VpcID is the ID of an existing VPC to use for the cluster. If empty, a VPC
	// will be created.
	// +optional
	VpcID string `json:"vpcID,omitempty"`

	// BootImageID is the Alibaba Cloud custom image used to boot worker nodes so
	// they join THIS cluster. It is the imported OpenShift discovery/RHCOS image
	// (the same ecs_image_id used to install the cluster, or a day-2 add-hosts
	// infra-env image) and carries the embedded Ignition / infra-env config.
	// AlibabaCloudMachines that omit spec.imageID fall back to this value, so a
	// MachineDeployment template does not have to repeat it. Leaving both empty
	// makes machine provisioning fail with a terminal NoBootImage error.
	// +optional
	BootImageID string `json:"bootImageID,omitempty"`

	// ResourceGroupID is the ID of the resource group for all cluster resources.
	// +optional
	ResourceGroupID string `json:"resourceGroupID,omitempty"`

	// ControlPlaneEndpoint represents the endpoint used to communicate with the
	// control plane. Populated by the controller once a SLB is provisioned.
	// +optional
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// AdditionalTags are additional tags to apply to all cloud resources created
	// for this cluster.
	// +optional
	AdditionalTags []Tag `json:"additionalTags,omitempty"`

	// FailureDomains lists the availability zones available for worker node
	// placement. Each entry pairs a zone ID with the VSwitch to use in that zone.
	// When set, CAPI automatically distributes Machines across zones using
	// round-robin assignment via Machine.Spec.FailureDomain.
	// Leave ZoneID/VSwitchID in AlibabaCloudMachineTemplate empty to allow
	// this automatic placement; explicit values in the template take precedence.
	// +optional
	FailureDomains []FailureDomain `json:"failureDomains,omitempty"`
}

// AlibabaCloudClusterStatus defines the observed state of AlibabaCloudCluster.
type AlibabaCloudClusterStatus struct {
	// Ready indicates that the cluster infrastructure is ready.
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// VpcID is the ID of the VPC created or adopted for the cluster.
	// +optional
	VpcID string `json:"vpcID,omitempty"`

	// ControlPlaneEndpoint is the endpoint downstream consumers (Machine
	// controller, control-plane provider) use to reach the API server.
	// In BYO mode it mirrors Spec.ControlPlaneEndpoint; the CAPI infra-cluster
	// contract requires this to be populated before status.ready=true.
	// +optional
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// SLBInstanceID is the ID of the Server Load Balancer created for the
	// control plane endpoint.
	// +optional
	SLBInstanceID string `json:"slbInstanceID,omitempty"`

	// FailureDomains is the list of failure domains published to CAPI so it can
	// automatically spread Machines across availability zones.
	// Populated by the controller from Spec.FailureDomains.
	// +optional
	FailureDomains []clusterv1.FailureDomain `json:"failureDomains,omitempty"`

	// Conditions defines current service state of the AlibabaCloudCluster.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=alibabacloudclusters,scope=Namespaced,categories=cluster-api
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".metadata.labels['cluster\\.x-k8s\\.io/cluster-name']"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="Region",type="string",JSONPath=".spec.region"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// AlibabaCloudCluster is the Schema for the alibabacloudclusters API.
type AlibabaCloudCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AlibabaCloudClusterSpec   `json:"spec,omitempty"`
	Status AlibabaCloudClusterStatus `json:"status,omitempty"`
}

// GetConditions returns the conditions of the AlibabaCloudCluster.
func (c *AlibabaCloudCluster) GetConditions() []metav1.Condition {
	return c.Status.Conditions
}

// SetConditions sets the conditions of the AlibabaCloudCluster.
func (c *AlibabaCloudCluster) SetConditions(conditions []metav1.Condition) {
	c.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// AlibabaCloudClusterList contains a list of AlibabaCloudCluster.
type AlibabaCloudClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AlibabaCloudCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AlibabaCloudCluster{}, &AlibabaCloudClusterList{})
}
