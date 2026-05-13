package client

import (
	"fmt"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/resourcemanager"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/slb"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"k8s.io/klog/v2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Tag is an Alibaba Cloud resource tag key-value pair.
type Tag struct {
	Key   string
	Value string
}

// CreateInstanceParams holds the parameters for creating an ECS instance via CAPI.
type CreateInstanceParams struct {
	RegionID           string
	ZoneID             string
	InstanceType       string
	ImageID            string
	SecurityGroupIDs   []string
	VSwitchID          string
	SystemDiskCategory string
	SystemDiskSize     int
	RAMRoleName        string
	UserData           string
	Tags               []Tag
	ResourceGroupID    string
}

// CreateInstanceResponse is the normalised response from CreateECSInstance.
type CreateInstanceResponse struct {
	InstanceID string
}

// InstanceDescription is the normalised view of an ECS instance returned by DescribeInstanceByID.
type InstanceDescription struct {
	InstanceID      string
	Status          string
	InnerIpAddress  struct{ IpAddress []string }
	PublicIpAddress struct{ IpAddress []string }
}

// ClientBuilderFunc creates an Alibaba Cloud Client for the given region using
// credentials resolved from the in-cluster controller-runtime client.
type ClientBuilderFunc func(c runtimeclient.Client, region string) (Client, error)

// NewCAPIClient creates an Alibaba Cloud Client suitable for CAPI controllers.
// Credentials are resolved from environment / RAM-role (ambient credentials).
func NewCAPIClient(_ runtimeclient.Client, regionID string) (Client, error) {
	sdkConfig := &sdk.Config{
		UserAgent: machineProviderUserAgent,
		Scheme:    "HTTPS",
	}

	// Use SDK's default credential chain (RAM role, env vars, etc.).
	ecsClient, err := ecs.NewClientWithOptions(regionID, sdkConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create ECS client for region %s: %w", regionID, err)
	}

	vpcClient, err := newVPCClient(regionID, sdkConfig)
	if err != nil {
		return nil, err
	}

	slbClient, err := newSLBClient(regionID, sdkConfig)
	if err != nil {
		return nil, err
	}

	rmClient, err := newRMClient(regionID, sdkConfig)
	if err != nil {
		return nil, err
	}

	return &alibabacloudClient{
		ecsClient: ecsClient,
		vpcClient: vpcClient,
		slbClient: slbClient,
		rmClient:  rmClient,
	}, nil
}

func newVPCClient(regionID string, sdkConfig *sdk.Config) (*vpc.Client, error) {
	c, err := vpc.NewClientWithOptions(regionID, sdkConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create VPC client: %w", err)
	}
	return c, nil
}

func newSLBClient(regionID string, sdkConfig *sdk.Config) (*slb.Client, error) {
	c, err := slb.NewClientWithOptions(regionID, sdkConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create SLB client: %w", err)
	}
	return c, nil
}

func newRMClient(regionID string, sdkConfig *sdk.Config) (*resourcemanager.Client, error) {
	c, err := resourcemanager.NewClientWithOptions(regionID, sdkConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create ResourceManager client: %w", err)
	}
	return c, nil
}

// DescribeInstanceByID fetches a single ECS instance by its ID.
// Returns nil if the instance does not exist.
func (c *alibabacloudClient) DescribeInstanceByID(instanceID string) (*InstanceDescription, error) {
	req := ecs.CreateDescribeInstancesRequest()
	req.InstanceIds = fmt.Sprintf(`["%s"]`, instanceID)

	resp, err := c.ecsClient.DescribeInstances(req)
	if err != nil {
		return nil, fmt.Errorf("DescribeInstances(%s): %w", instanceID, err)
	}
	if len(resp.Instances.Instance) == 0 {
		return nil, nil
	}
	inst := resp.Instances.Instance[0]
	desc := &InstanceDescription{
		InstanceID: inst.InstanceId,
		Status:     inst.Status,
	}
	desc.InnerIpAddress.IpAddress = inst.InnerIpAddress.IpAddress
	desc.PublicIpAddress.IpAddress = inst.PublicIpAddress.IpAddress
	return desc, nil
}

// DeleteECSInstance stops (if force=true) and deletes an ECS instance by ID.
// It is idempotent: returns nil if the instance no longer exists.
func (c *alibabacloudClient) DeleteECSInstance(instanceID string, force bool) error {
	// First stop the instance so it can be deleted.
	stopReq := ecs.CreateStopInstanceRequest()
	stopReq.InstanceId = instanceID
	if force {
		stopReq.ForceStop = "true"
	}
	if _, err := c.ecsClient.StopInstance(stopReq); err != nil {
		// If the instance is already stopped or not found, proceed to delete.
		klog.Warningf("StopInstance(%s) returned: %v (proceeding to delete)", instanceID, err)
	}

	delReq := ecs.CreateDeleteInstanceRequest()
	delReq.InstanceId = instanceID
	delReq.Force = "true" // allows deletion from Stopping/Stopped states
	if _, err := c.ecsClient.DeleteInstance(delReq); err != nil {
		return fmt.Errorf("DeleteInstance(%s): %w", instanceID, err)
	}
	klog.Infof("Deleted ECS instance %s", instanceID)
	return nil
}

// CreateECSInstance creates an ECS instance and returns its ID.
func (c *alibabacloudClient) CreateECSInstance(params CreateInstanceParams) (*CreateInstanceResponse, error) {
	req := ecs.CreateRunInstancesRequest()
	req.RegionId = params.RegionID
	req.ZoneId = params.ZoneID
	req.InstanceType = params.InstanceType
	req.ImageId = params.ImageID
	req.VSwitchId = params.VSwitchID
	req.SystemDiskCategory = params.SystemDiskCategory
	req.SystemDiskSize = fmt.Sprintf("%d", params.SystemDiskSize)
	req.RamRoleName = params.RAMRoleName
	req.UserData = params.UserData
	req.ResourceGroupId = params.ResourceGroupID
	req.Amount = "1"

	if len(params.SecurityGroupIDs) > 0 {
		ids := make([]string, len(params.SecurityGroupIDs))
		copy(ids, params.SecurityGroupIDs)
		req.SecurityGroupIds = &ids
	}

	tags := make([]ecs.RunInstancesTag, len(params.Tags))
	for i, t := range params.Tags {
		tags[i] = ecs.RunInstancesTag{Key: t.Key, Value: t.Value}
	}
	req.Tag = &tags

	resp, err := c.ecsClient.RunInstances(req)
	if err != nil {
		return nil, fmt.Errorf("RunInstances: %w", err)
	}
	if len(resp.InstanceIdSets.InstanceIdSet) == 0 {
		return nil, fmt.Errorf("RunInstances returned no instance IDs")
	}
	instanceID := resp.InstanceIdSets.InstanceIdSet[0]
	klog.Infof("Created ECS instance %s", instanceID)
	return &CreateInstanceResponse{InstanceID: instanceID}, nil
}
