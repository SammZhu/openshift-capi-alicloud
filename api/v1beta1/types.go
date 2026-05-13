package v1beta1

// AlibabaCloudMachineProviderConditionType is a valid value for AlibabaCloudMachineProviderCondition.Type.
type AlibabaCloudMachineProviderConditionType string

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
