package v1beta1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

const (
	// MachineFinalizer allows AlibabaCloudMachineReconciler to clean up ECS instances
	// before removing the AlibabaCloudMachine object from the API server.
	MachineFinalizer = "alibabacloudmachine.infrastructure.cluster.x-k8s.io"
)

// AlibabaCloudMachineSpec defines the desired state of AlibabaCloudMachine.
type AlibabaCloudMachineSpec struct {
	// ProviderID is the unique identifier as specified by the cloud provider.
	// Populated by the controller after ECS instance creation.
	// +optional
	ProviderID *string `json:"providerID,omitempty"`

	// InstanceType is the ECS instance type (e.g. ecs.c6.large).
	// +kubebuilder:validation:Required
	InstanceType string `json:"instanceType"`

	// ImageID is the ID of the ECS image to use for the instance. When empty, the
	// owning AlibabaCloudCluster's spec.bootImageID is used instead (the cluster
	// RHCOS/discovery boot image). At least one of the two must be set, otherwise
	// provisioning fails with a terminal NoBootImage error.
	// +optional
	ImageID string `json:"imageID,omitempty"`

	// RegionID is the Alibaba Cloud region. Defaults to the cluster region.
	// +optional
	RegionID string `json:"regionID,omitempty"`

	// ZoneID is the availability zone within the region.
	// +optional
	ZoneID string `json:"zoneID,omitempty"`

	// SecurityGroupIDs is the list of security group IDs to associate with the instance.
	// +optional
	SecurityGroupIDs []string `json:"securityGroupIDs,omitempty"`

	// VSwitchID is the ID of the vSwitch to use for the primary network interface.
	// +optional
	VSwitchID string `json:"vSwitchID,omitempty"`

	// SystemDisk describes the system disk configuration.
	// +optional
	SystemDisk *SystemDisk `json:"systemDisk,omitempty"`

	// DataDisks describes additional data disks.
	// +optional
	DataDisks []DataDisk `json:"dataDisks,omitempty"`

	// RAMRoleName is the RAM role name to attach to the ECS instance.
	// +optional
	RAMRoleName string `json:"ramRoleName,omitempty"`

	// SpotStrategy is the spot-instance bidding strategy:
	//   - NoSpot (or empty): regular pay-as-you-go instance.
	//   - SpotWithPriceLimit: spot instance with a user-set hourly price ceiling
	//     (SpotPriceLimit is required).
	//   - SpotAsPriceGo: spot instance priced at the current market rate.
	// +kubebuilder:validation:Enum=NoSpot;SpotWithPriceLimit;SpotAsPriceGo
	// +optional
	SpotStrategy string `json:"spotStrategy,omitempty"`

	// SpotPriceLimit is the maximum hourly price (in the region's currency) for a
	// spot instance. Required when SpotStrategy is SpotWithPriceLimit; ignored
	// otherwise.
	// +optional
	SpotPriceLimit *float64 `json:"spotPriceLimit,omitempty"`

	// Tags are additional tags to apply to the ECS instance.
	// +optional
	Tags []Tag `json:"tags,omitempty"`

	// MetadataOptions configures the instance metadata service (IMDS). When
	// omitted, the controller applies a secure baseline: the metadata endpoint is
	// enabled and a session token is required (IMDSv2-equivalent,
	// httpTokens=required), which mitigates SSRF. Set explicitly to relax this.
	// +optional
	MetadataOptions *MetadataOptions `json:"metadataOptions,omitempty"`

	// UserDataSecret refers to a Secret containing the base64-encoded user data
	// script to execute on instance startup.
	// +optional
	UserDataSecret *corev1.LocalObjectReference `json:"userDataSecret,omitempty"`

	// CredentialsSecret refers to a Secret containing Alibaba Cloud credentials.
	// If not specified, the controller uses the ambient credentials from the
	// cluster credential infrastructure.
	// +optional
	CredentialsSecret *corev1.LocalObjectReference `json:"credentialsSecret,omitempty"`
}

// MetadataOptions configures the ECS instance metadata service (IMDS) hardening.
type MetadataOptions struct {
	// HttpEndpoint enables or disables the metadata endpoint. Defaults to
	// "enabled" when the whole MetadataOptions block is omitted.
	// +kubebuilder:validation:Enum=enabled;disabled
	// +optional
	HttpEndpoint string `json:"httpEndpoint,omitempty"`

	// HttpTokens controls whether a session token is required to read metadata.
	// "required" enforces IMDSv2-style token auth (the secure default);
	// "optional" allows tokenless access.
	// +kubebuilder:validation:Enum=optional;required
	// +optional
	HttpTokens string `json:"httpTokens,omitempty"`

	// HttpPutResponseHopLimit is the maximum number of network hops the metadata
	// token may travel (1-64). 0 leaves the Alibaba Cloud API default.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=64
	// +optional
	HttpPutResponseHopLimit int `json:"httpPutResponseHopLimit,omitempty"`
}

// AlibabaCloudMachineStatus defines the observed state of AlibabaCloudMachine.
type AlibabaCloudMachineStatus struct {
	// Ready indicates that the machine is ready to receive workloads.
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// InstanceID is the ECS instance ID of the provisioned machine.
	// +optional
	InstanceID *string `json:"instanceID,omitempty"`

	// InstanceState reflects the current state of the ECS instance.
	// +optional
	InstanceState *InstanceState `json:"instanceState,omitempty"`

	// Addresses contains the associated addresses for the machine.
	// +optional
	Addresses []clusterv1.MachineAddress `json:"addresses,omitempty"`

	// Conditions defines current service state of the AlibabaCloudMachine.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// FailureReason will be set in the event that there is a terminal problem
	// reconciling the Machine and will contain a succinct value suitable for
	// machine interpretation.
	// +optional
	FailureReason *string `json:"failureReason,omitempty"`

	// FailureMessage will be set in the event that there is a terminal problem
	// reconciling the Machine and will contain a more verbose string suitable
	// for logging and human consumption.
	// +optional
	FailureMessage *string `json:"failureMessage,omitempty"`

	// IgnitionOSS records the OSS object holding this machine's offloaded
	// Ignition (set only when user-data exceeded the ECS limit and was offloaded),
	// so the controller can delete it when the machine is removed.
	// +optional
	IgnitionOSS *IgnitionOSSRef `json:"ignitionOSS,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=alibabacloudmachines,scope=Namespaced,categories=cluster-api
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".metadata.labels['cluster\\.x-k8s\\.io/cluster-name']"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.instanceState"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="InstanceID",type="string",JSONPath=".status.instanceID"
// +kubebuilder:printcolumn:name="Machine",type="string",JSONPath=".metadata.ownerReferences[?(@.kind==\"Machine\")].name"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// AlibabaCloudMachine is the Schema for the alibabacloudmachines API.
type AlibabaCloudMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AlibabaCloudMachineSpec   `json:"spec,omitempty"`
	Status AlibabaCloudMachineStatus `json:"status,omitempty"`
}

// GetConditions returns the conditions of the AlibabaCloudMachine.
func (m *AlibabaCloudMachine) GetConditions() []metav1.Condition {
	return m.Status.Conditions
}

// SetConditions sets the conditions of the AlibabaCloudMachine.
func (m *AlibabaCloudMachine) SetConditions(conditions []metav1.Condition) {
	m.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// AlibabaCloudMachineList contains a list of AlibabaCloudMachine.
type AlibabaCloudMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AlibabaCloudMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AlibabaCloudMachine{}, &AlibabaCloudMachineList{})
}
