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
	// Instance metadata service (IMDS) hardening. The controller defaults these
	// to the secure baseline (HttpEndpoint=enabled, HttpTokens=required) so that
	// metadata access requires a token (IMDSv2-equivalent), mitigating SSRF.
	// Empty means "leave the Alibaba Cloud API default".
	HttpEndpoint string
	HttpTokens   string
	// HttpPutResponseHopLimit caps the metadata token TTL hop count; 0 leaves the
	// API default.
	HttpPutResponseHopLimit int
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

// resolveCredential builds an Alibaba Cloud SDK credential for CAPA, preferring
// an ECS RAM role (auto-rotated STS credentials, no long-lived secret on disk)
// over static AccessKeys — the least-privilege, rotation-friendly default
// (P3-CAPA.30). See docs/RAM-POLICY.md for the minimal RAM policy.
//
// Precedence:
//  1. Explicitly named ECS RAM role — env ALIBABA_CLOUD_ECS_METADATA.
//  2. Static AccessKey — env ALIBABA_CLOUD_ACCESS_KEY_{ID,SECRET} (or the older
//     ALIBABACLOUD_* spelling). Explicit opt-in for dev / non-ECS. NOT rotated:
//     changing it requires restarting the controller.
//  3. Default — auto-discover the instance RAM role from the metadata service.
//     The recommended production path: the SDK fetches and refreshes STS
//     credentials automatically. Fails at first API call if the controller ECS
//     has no RAM role attached.
func resolveCredential() auth.Credential {
	return resolveCredentialFrom(os.Getenv)
}

// resolveCredentialFrom is the testable core (env lookup injected).
func resolveCredentialFrom(getenv func(string) string) auth.Credential {
	if role := getenv("ALIBABA_CLOUD_ECS_METADATA"); role != "" {
		klog.V(2).Infof("alibaba: credential source = ECS RAM role (explicit): %s", role)
		return credentials.NewEcsRamRoleCredential(role)
	}

	ak := firstNonEmptyFrom(getenv, "ALIBABA_CLOUD_ACCESS_KEY_ID", "ALIBABACLOUD_ACCESS_KEY_ID")
	sk := firstNonEmptyFrom(getenv, "ALIBABA_CLOUD_ACCESS_KEY_SECRET", "ALIBABACLOUD_ACCESS_KEY_SECRET")
	if ak != "" && sk != "" {
		klog.V(2).Info("alibaba: credential source = static AccessKey from environment " +
			"(no auto-rotation; prefer an ECS RAM role in production)")
		return credentials.NewAccessKeyCredential(ak, sk)
	}
	if (ak == "") != (sk == "") {
		klog.Warning("alibaba: only one of ACCESS_KEY_ID/SECRET set; ignoring partial AccessKey")
	}

	klog.V(2).Info("alibaba: credential source = ECS RAM role (auto-discovered from metadata)")
	return credentials.NewEcsRamRoleCredential("")
}

func firstNonEmpty(keys ...string) string {
	return firstNonEmptyFrom(os.Getenv, keys...)
}

func firstNonEmptyFrom(getenv func(string) string, keys ...string) string {
	for _, k := range keys {
		if v := getenv(k); v != "" {
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
		// SDK-level retry covers transport errors and HTTP 5xx. Throttling (4xx
		// Throttling* codes) is handled separately by retryThrottled with backoff,
		// since the SDK's built-in retry neither matches those codes nor sleeps.
		AutoRetry:    true,
		MaxRetryTime: 3,
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

	var resp *ecs.DescribeInstancesResponse
	err := retryThrottled("DescribeInstances", func() (e error) {
		resp, e = c.ecsClient.DescribeInstances(req)
		return e
	})
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
	if err := retryThrottled("DeleteInstance", func() error {
		_, e := c.ecsClient.DeleteInstance(delReq)
		return e
	}); err != nil {
		return fmt.Errorf("DeleteInstance(%s): %w", instanceID, err)
	}
	klog.Infof("Deleted ECS instance %s", instanceID)
	return nil
}

// ModifyInstanceMetadata updates the instance's metadata-service (IMDS) options.
// Empty httpEndpoint/httpTokens and a non-positive hopLimit are left unchanged.
func (c *alibabacloudClient) ModifyInstanceMetadata(instanceID, httpEndpoint, httpTokens string, hopLimit int) error {
	req := ecs.CreateModifyInstanceMetadataOptionsRequest()
	req.InstanceId = instanceID
	if httpEndpoint != "" {
		req.HttpEndpoint = httpEndpoint
	}
	if httpTokens != "" {
		req.HttpTokens = httpTokens
	}
	if hopLimit > 0 {
		req.HttpPutResponseHopLimit = requests.NewInteger(hopLimit)
	}
	if err := retryThrottled("ModifyInstanceMetadataOptions", func() error {
		_, e := c.ecsClient.ModifyInstanceMetadataOptions(req)
		return e
	}); err != nil {
		return fmt.Errorf("ModifyInstanceMetadataOptions(%s): %w", instanceID, err)
	}
	klog.Infof("Hardened IMDS options on %s (httpTokens=%s)", instanceID, httpTokens)
	return nil
}

// MachineNameTagKey is the per-machine tag CAPA stamps on every instance it
// creates (see the controller's toSDKTags). It is unique within a cluster and is
// used both as the idempotency key for adopt-before-create AND as the durable
// handle the delete path sweeps by to catch a tagged orphan whose
// Status.InstanceID write was lost.
const MachineNameTagKey = "k8s.io/cluster-api-machine"

// machineTag returns the value of the cluster-api-machine tag, or "" if absent.
func machineTag(tags []Tag) string {
	for _, t := range tags {
		if t.Key == MachineNameTagKey {
			return t.Value
		}
	}
	return ""
}

// FindInstanceByTag returns the ID of an existing, non-deleted instance in region
// carrying the given tag, or "" if none exists.
func (c *alibabacloudClient) FindInstanceByTag(region, key, value string) (string, error) {
	req := ecs.CreateDescribeInstancesRequest()
	req.RegionId = region
	req.Tag = &[]ecs.DescribeInstancesTag{{Key: key, Value: value}}
	var resp *ecs.DescribeInstancesResponse
	err := retryThrottled("DescribeInstances", func() (e error) {
		resp, e = c.ecsClient.DescribeInstances(req)
		return e
	})
	if err != nil {
		return "", fmt.Errorf("DescribeInstances by tag %s=%s: %w", key, value, err)
	}
	for _, inst := range resp.Instances.Instance {
		if inst.Status != "Deleted" {
			return inst.InstanceId, nil
		}
	}
	return "", nil
}

// CreateECSInstance creates an ECS instance and returns its ID.
func (c *alibabacloudClient) CreateECSInstance(params CreateInstanceParams) (*CreateInstanceResponse, error) {
	// Idempotency guard: if an instance already exists for this machine (tagged
	// k8s.io/cluster-api-machine=<name>), adopt it instead of creating a
	// duplicate. This protects against a lost Status.InstanceID write — e.g. a
	// concurrent patch conflict, or the controller restarting between
	// RunInstances and the status persist — which would otherwise leave the
	// first instance orphaned and billing.
	if mv := machineTag(params.Tags); mv != "" {
		existing, err := c.FindInstanceByTag(params.RegionID, MachineNameTagKey, mv)
		if err != nil {
			return nil, err
		}
		if existing != "" {
			klog.Infof("adopting existing ECS instance %s for %s=%s (skipping RunInstances)", existing, MachineNameTagKey, mv)
			return &CreateInstanceResponse{InstanceID: existing}, nil
		}
	}

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

	// Instance metadata service hardening (IMDSv2). With HttpTokens=required the
	// node's metadata can only be read with a session token, mitigating SSRF.
	if params.HttpEndpoint != "" {
		req.HttpEndpoint = params.HttpEndpoint
	}
	if params.HttpTokens != "" {
		req.HttpTokens = params.HttpTokens
	}
	if params.HttpPutResponseHopLimit > 0 {
		req.HttpPutResponseHopLimit = requests.NewInteger(params.HttpPutResponseHopLimit)
	}

	if len(params.SecurityGroupIDs) > 0 {
		ids := make([]string, len(params.SecurityGroupIDs))
		copy(ids, params.SecurityGroupIDs)
		req.SecurityGroupIds = &ids
	}

	// Bind the system disk's lifecycle to the instance explicitly. Pay-as-you-go
	// system disks are already released with the instance, but setting it removes
	// any ambiguity and guards against a misconfigured retained system disk
	// becoming an orphan that keeps billing after the Machine is gone.
	req.GetQueryParams()["SystemDisk.DeleteWithInstance"] = "true"

	if len(params.DataDisks) > 0 {
		disks := make([]ecs.RunInstancesDataDisk, len(params.DataDisks))
		for i, d := range params.DataDisks {
			disks[i] = ecs.RunInstancesDataDisk{
				Category:         d.Category,
				Size:             fmt.Sprintf("%d", d.Size),
				PerformanceLevel: d.PerformanceLevel,
				KMSKeyId:         d.KMSKeyID,
				// Disks created with the instance must die with it — otherwise a
				// deleted Machine leaves orphan data disks billing indefinitely.
				DeleteWithInstance: "true",
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

	var resp *ecs.RunInstancesResponse
	err := retryThrottled("RunInstances", func() (e error) {
		resp, e = c.ecsClient.RunInstances(req)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("RunInstances: %w", err)
	}
	if len(resp.InstanceIdSets.InstanceIdSet) == 0 {
		return nil, fmt.Errorf("RunInstances returned no instance IDs")
	}
	instanceID := resp.InstanceIdSets.InstanceIdSet[0]
	klog.Infof("Created ECS instance %s", instanceID)

	// Propagate the instance tags to every resource created with the instance —
	// disks (system + data) and network interfaces (ENIs) — for cost allocation.
	// RunInstances tags only the instance itself, so finance cannot attribute
	// storage/network spend by tag unless we stamp the child resources too. This
	// is best-effort: the instance already exists, so a tagging failure must not
	// fail provisioning — log and continue.
	if len(params.Tags) > 0 {
		c.tagInstanceDisks(params.RegionID, instanceID, params.Tags)
		c.tagInstanceENIs(params.RegionID, instanceID, params.Tags)
	}
	return &CreateInstanceResponse{InstanceID: instanceID}, nil
}

// toTagResourcesTags converts CAPA tags to the ECS TagResources shape.
func toTagResourcesTags(tags []Tag) []ecs.TagResourcesTag {
	out := make([]ecs.TagResourcesTag, len(tags))
	for i, t := range tags {
		out[i] = ecs.TagResourcesTag{Key: t.Key, Value: t.Value}
	}
	return out
}

// tagInstanceDisks applies tags to all disks attached to the given instance.
// Best-effort: every failure is logged and swallowed.
func (c *alibabacloudClient) tagInstanceDisks(region, instanceID string, tags []Tag) {
	dreq := ecs.CreateDescribeDisksRequest()
	dreq.RegionId = region
	dreq.InstanceId = instanceID
	dresp, err := c.ecsClient.DescribeDisks(dreq)
	if err != nil {
		klog.Warningf("tag disks: DescribeDisks(%s): %v", instanceID, err)
		return
	}
	ids := make([]string, 0, len(dresp.Disks.Disk))
	for _, d := range dresp.Disks.Disk {
		if d.DiskId != "" {
			ids = append(ids, d.DiskId)
		}
	}
	if len(ids) == 0 {
		return
	}

	treq := ecs.CreateTagResourcesRequest()
	treq.RegionId = region
	treq.ResourceType = "disk"
	treq.ResourceId = &ids
	ttags := toTagResourcesTags(tags)
	treq.Tag = &ttags
	if err := retryThrottled("TagResources(disk)", func() error {
		_, e := c.ecsClient.TagResources(treq)
		return e
	}); err != nil {
		klog.Warningf("tag disks: TagResources(%v): %v", ids, err)
		return
	}
	klog.V(2).Infof("tagged %d disk(s) of instance %s for cost allocation", len(ids), instanceID)
}

// tagInstanceENIs applies tags to all network interfaces (ENIs) attached to the
// given instance, so network spend is attributable by tag. Best-effort: every
// failure is logged and swallowed.
func (c *alibabacloudClient) tagInstanceENIs(region, instanceID string, tags []Tag) {
	nreq := ecs.CreateDescribeNetworkInterfacesRequest()
	nreq.RegionId = region
	nreq.InstanceId = instanceID
	nresp, err := c.ecsClient.DescribeNetworkInterfaces(nreq)
	if err != nil {
		klog.Warningf("tag enis: DescribeNetworkInterfaces(%s): %v", instanceID, err)
		return
	}
	ids := make([]string, 0, len(nresp.NetworkInterfaceSets.NetworkInterfaceSet))
	for _, ni := range nresp.NetworkInterfaceSets.NetworkInterfaceSet {
		if ni.NetworkInterfaceId != "" {
			ids = append(ids, ni.NetworkInterfaceId)
		}
	}
	if len(ids) == 0 {
		return
	}

	treq := ecs.CreateTagResourcesRequest()
	treq.RegionId = region
	treq.ResourceType = "eni"
	treq.ResourceId = &ids
	ttags := toTagResourcesTags(tags)
	treq.Tag = &ttags
	if err := retryThrottled("TagResources(eni)", func() error {
		_, e := c.ecsClient.TagResources(treq)
		return e
	}); err != nil {
		klog.Warningf("tag enis: TagResources(%v): %v", ids, err)
		return
	}
	klog.V(2).Infof("tagged %d ENI(s) of instance %s for cost allocation", len(ids), instanceID)
}
