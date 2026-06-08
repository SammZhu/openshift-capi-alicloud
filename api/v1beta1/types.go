package v1beta1

// AlibabaCloudMachineProviderConditionType is a valid value for AlibabaCloudMachineProviderCondition.Type.
type AlibabaCloudMachineProviderConditionType string

const (
	// CloudControllerManagerReadyCondition reports whether the Alibaba
	// cloud-controller-manager (CCM) appears to be initializing Nodes. CAPA never
	// touches Node objects — the CCM removes the node.cloudprovider.kubernetes.io/
	// uninitialized taint and sets providerID/addresses/zone. If worker Nodes stay
	// uninitialized past the grace period, CCM is missing or broken and workers
	// will never become Ready; this condition surfaces that runtime dependency gap
	// loudly instead of letting workers hang silently.
	CloudControllerManagerReadyCondition = "CloudControllerManagerReady"

	// NodesAwaitingCloudProviderReason: one or more Nodes have been stuck with the
	// uninitialized taint past the grace period (CCM likely absent).
	NodesAwaitingCloudProviderReason = "NodesAwaitingCloudProvider"
	// CloudControllerManagerHealthyReason: no stuck-uninitialized Nodes observed.
	CloudControllerManagerHealthyReason = "CloudControllerManagerHealthy"
)

// InstanceState describes the state of an Alibaba Cloud ECS instance.
type InstanceState string

var (
	// InstanceStatePending is the string representing an instance in a pending state.
	InstanceStatePending = InstanceState("Pending")
	// InstanceStateRunning is the string representing an instance in a running state.
	InstanceStateRunning = InstanceState("Running")
	// InstanceStateStopped is the string representing an instance in a stopped state.
	InstanceStateStopped = InstanceState("Stopped")
	// InstanceStateStopping is the string representing an instance in a stopping state.
	InstanceStateStopping = InstanceState("Stopping")
	// InstanceStateStarting is the string representing an instance in a starting state.
	InstanceStateStarting = InstanceState("Starting")
	// InstanceStateDeleted is the string representing an instance that has been deleted.
	InstanceStateDeleted = InstanceState("Deleted")
)

// Tag is a key-value pair applied to an Alibaba Cloud resource.
type Tag struct {
	// Key is the tag key.
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
	// Value is the tag value.
	Value string `json:"value"`
}

// SystemDisk describes the system disk configuration for an ECS instance.
type SystemDisk struct {
	// Category is the disk category (e.g. cloud_efficiency, cloud_ssd, cloud_essd).
	// +kubebuilder:validation:Enum=cloud_efficiency;cloud_ssd;cloud_essd;cloud_auto
	Category string `json:"category"`
	// Size is the disk size in GiB.
	// +kubebuilder:validation:Minimum=20
	Size int `json:"size"`
	// PerformanceLevel is the ESSD performance level. Only meaningful for the
	// cloud_essd category; ignored otherwise.
	// +kubebuilder:validation:Enum=PL0;PL1;PL2;PL3
	// +optional
	PerformanceLevel string `json:"performanceLevel,omitempty"`
	// Encrypted enables server-side encryption of the disk.
	// +optional
	Encrypted *bool `json:"encrypted,omitempty"`
	// KMSKeyID is the ID of the KMS key used to encrypt the disk. When Encrypted
	// is true and KMSKeyID is empty, Alibaba Cloud's default EBS service key is
	// used. Setting KMSKeyID without Encrypted=true is rejected by the webhook.
	// +optional
	KMSKeyID string `json:"kmsKeyID,omitempty"`
}

// DataDisk describes an additional data disk for an ECS instance.
type DataDisk struct {
	// Category is the disk category.
	// +kubebuilder:validation:Enum=cloud_efficiency;cloud_ssd;cloud_essd;cloud_auto
	Category string `json:"category"`
	// Size is the disk size in GiB.
	// +kubebuilder:validation:Minimum=20
	Size int `json:"size"`
	// Name is an optional identifier for the data disk.
	// +optional
	Name string `json:"name,omitempty"`
	// PerformanceLevel is the ESSD performance level. Only meaningful for the
	// cloud_essd category; ignored otherwise.
	// +kubebuilder:validation:Enum=PL0;PL1;PL2;PL3
	// +optional
	PerformanceLevel string `json:"performanceLevel,omitempty"`
	// Encrypted enables server-side encryption of the disk.
	// +optional
	Encrypted *bool `json:"encrypted,omitempty"`
	// KMSKeyID is the ID of the KMS key used to encrypt the disk. When Encrypted
	// is true and KMSKeyID is empty, Alibaba Cloud's default EBS service key is
	// used. Setting KMSKeyID without Encrypted=true is rejected by the webhook.
	// +optional
	KMSKeyID string `json:"kmsKeyID,omitempty"`
}

// FailureDomain maps an Alibaba Cloud availability zone to the VSwitch that
// should be used for ECS instances placed in that zone.
// When AlibabaCloudCluster.Spec.FailureDomains is populated, CAPI will
// automatically distribute Machines across the listed zones.
type FailureDomain struct {
	// ZoneID is the Alibaba Cloud availability zone identifier (e.g. cn-hangzhou-h).
	// +kubebuilder:validation:Required
	ZoneID string `json:"zoneID"`

	// VSwitchID is the ID of the VSwitch to use for instances in this zone.
	// +kubebuilder:validation:Required
	VSwitchID string `json:"vSwitchID"`
}

// NetworkInterface describes a network interface for an ECS instance.
type NetworkInterface struct {
	// SubnetID is the ID of the vSwitch to attach the interface to.
	SubnetID string `json:"subnetID"`
	// SecurityGroupIDs is the list of security group IDs to apply.
	// +optional
	SecurityGroupIDs []string `json:"securityGroupIDs,omitempty"`
}
