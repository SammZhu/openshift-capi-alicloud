package client

import (
	"fmt"
	"os"
	"strconv"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
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

// DataDiskParam describes an additional data disk to attach at instance creation.
type DataDiskParam struct {
	Category         string
	Size             int
	PerformanceLevel string
	Encrypted        *bool
	KMSKeyID         string
}

// CreateInstanceParams holds the parameters for creating an ECS instance via CAPI.
type CreateInstanceParams struct {
	RegionID                   string
	ZoneID                     string
	InstanceType               string
	ImageID                    string
	SecurityGroupIDs           []string
	VSwitchID                  string
	SystemDiskCategory         string
	SystemDiskSize             int
	SystemDiskPerformanceLevel string
	SystemDiskEncrypted        *bool
	SystemDiskKMSKeyID         string
	DataDisks                  []DataDiskParam
	RAMRoleName                string
	UserData                   string
	Tags                       []Tag
	ResourceGroupID            string
	// SpotStrategy is one of NoSpot / SpotWithPriceLimit / SpotAsPriceGo. Empty
	// means a regular pay-as-you-go instance.
	SpotStrategy string
	// SpotPriceLimit is the hourly price ceiling; only used with
	// SpotWithPriceLimit.
	SpotPriceLimit *float64
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

// resolveCredential builds an Alibaba Cloud SDK credential for CAPA.
//
// The Alibaba Cloud Go SDK's NewClientWithOptions does NOT auto-discover
// credentials when the credential argument is nil — it returns
// SDK.UnsupportedCredential.  We resolve credentials explicitly here, in
// this order:
//
//  1. AccessKey from environment variables.  Both
//     ALIBABA_CLOUD_ACCESS_KEY_{ID,SECRET} (the newer
//     alibabacloud-credentials-go spelling) and the older
//     ALIBABACLOUD_ACCESS_KEY_{ID,SECRET} are accepted.
//  2. ECS RAM role from the instance metadata service, when env var
//     ALIBABA_CLOUD_ECS_METADATA names the role to assume (mirrors the
//     convention used by cloud-provider-alibaba-cloud).
//  3. nil — preserves the previous fail-loud behaviour for callers that
//     want NewClientWithOptions to return UnsupportedCredential.
func resolveCredential() auth.Credential {
	ak := firstNonEmpty("ALIBABA_CLOUD_ACCESS_KEY_ID", "ALIBABACLOUD_ACCESS_KEY_ID")
	sk := firstNonEmpty("ALIBABA_CLOUD_ACCESS_KEY_SECRET", "ALIBABACLOUD_ACCESS_KEY_SECRET")
	if ak != "" && sk != "" {
		klog.V(2).Info("alibaba: using AccessKey credential from environment")
		return credentials.NewAccessKeyCredential(ak, sk)
	}
	if role := os.Getenv("ALIBABA_CLOUD_ECS_METADATA"); role != "" {
		klog.V(2).Infof("alibaba: using ECS RAM role credential: %s", role)
		return credentials.NewEcsRamRoleCredential(role)
	}
	klog.Warning("alibaba: no credentials in environment; SDK will return UnsupportedCredential")
	return nil
}

func firstNonEmpty(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// NewCAPIClient creates an Alibaba Cloud Client suitable for CAPI controllers.
// Credentials are resolved from environment / RAM-role (see resolveCredential).
func NewCAPIClient(_ runtimeclient.Client, regionID string) (Client, error) {
	sdkConfig := &sdk.Config{
		UserAgent: machineProviderUserAgent,
		Scheme:    "HTTPS",
	}

	cred := resolveCredential()

	ecsClient, err := ecs.NewClientWithOptions(regionID, sdkConfig, cred)
	if err != nil {
		return nil, fmt.Errorf("failed to create ECS client for region %s: %w", regionID, err)
	}

	vpcClient, err := newVPCClient(regionID, sdkConfig, cred)
	if err != nil {
		return nil, err
	}

	slbClient, err := newSLBClient(regionID, sdkConfig, cred)
	if err != nil {
		return nil, err
	}

	rmClient, err := newRMClient(regionID, sdkConfig, cred)
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

func newVPCClient(regionID string, sdkConfig *sdk.Config, cred auth.Credential) (*vpc.Client, error) {
	c, err := vpc.NewClientWithOptions(regionID, sdkConfig, cred)
	if err != nil {
		return nil, fmt.Errorf("failed to create VPC client: %w", err)
	}
	return c, nil
}

func newSLBClient(regionID string, sdkConfig *sdk.Config, cred auth.Credential) (*slb.Client, error) {
	c, err := slb.NewClientWithOptions(regionID, sdkConfig, cred)
	if err != nil {
		return nil, fmt.Errorf("failed to create SLB client: %w", err)
	}
	return c, nil
}

func newRMClient(regionID string, sdkConfig *sdk.Config, cred auth.Credential) (*resourcemanager.Client, error) {
	c, err := resourcemanager.NewClientWithOptions(regionID, sdkConfig, cred)
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
	req.SystemDiskPerformanceLevel = params.SystemDiskPerformanceLevel
	req.RamRoleName = params.RAMRoleName
	req.UserData = params.UserData
	req.ResourceGroupId = params.ResourceGroupID
	req.Amount = "1"

	// System-disk encryption + KMS key are not typed fields on RunInstancesRequest
	// in this SDK version (only SystemDisk.PerformanceLevel is). Inject them as raw
	// query params — InitParams merges struct-tag fields into this same map at call
	// time without clobbering manually-set entries.
	if params.SystemDiskEncrypted != nil && *params.SystemDiskEncrypted {
		req.GetQueryParams()["SystemDisk.Encrypted"] = "true"
		if params.SystemDiskKMSKeyID != "" {
			req.GetQueryParams()["SystemDisk.KMSKeyId"] = params.SystemDiskKMSKeyID
		}
	}

	if params.SpotStrategy != "" {
		req.SpotStrategy = params.SpotStrategy
	}
	if params.SpotPriceLimit != nil {
		req.SpotPriceLimit = requests.NewFloat(*params.SpotPriceLimit)
	}

	if len(params.SecurityGroupIDs) > 0 {
		ids := make([]string, len(params.SecurityGroupIDs))
		copy(ids, params.SecurityGroupIDs)
		req.SecurityGroupIds = &ids
	}

	if len(params.DataDisks) > 0 {
		disks := make([]ecs.RunInstancesDataDisk, len(params.DataDisks))
		for i, d := range params.DataDisks {
			disks[i] = ecs.RunInstancesDataDisk{
				Category:         d.Category,
				Size:             fmt.Sprintf("%d", d.Size),
				PerformanceLevel: d.PerformanceLevel,
				KMSKeyId:         d.KMSKeyID,
			}
			if d.Encrypted != nil {
				disks[i].Encrypted = strconv.FormatBool(*d.Encrypted)
			}
		}
		req.DataDisk = &disks
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
