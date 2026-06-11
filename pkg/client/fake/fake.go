// Package fake provides a test double for the alibabaClient.Client interface.
// The three CAPI-controller methods (DescribeInstanceByID, CreateECSInstance,
// DeleteECSInstance) are stubbed with configurable Fn fields. Every other
// method in the large Client interface returns a "not implemented" error so
// tests fail loudly if an unexpected method is called.
package fake

import (
	"fmt"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/resourcemanager"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/slb"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"

	alibabaClient "github.com/SammZhu/openshift-capi-alicloud/pkg/client"
)

// FakeClient is a test double for alibabaClient.Client.
// Populate the *Fn fields to control what each CAPI method returns.
type FakeClient struct {
	DescribeInstanceByIDFn   func(instanceID string) (*alibabaClient.InstanceDescription, error)
	CreateECSInstanceFn      func(params alibabaClient.CreateInstanceParams) (*alibabaClient.CreateInstanceResponse, error)
	DeleteECSInstanceFn      func(instanceID string, force bool) error
	FindInstanceByTagFn      func(region, key, value string) (string, error)
	ModifyInstanceMetadataFn func(instanceID, httpEndpoint, httpTokens string, hopLimit int) error
	PutIgnitionObjectFn      func(params alibabaClient.IgnitionStoreParams) (string, error)
	DeleteIgnitionObjectFn   func(params alibabaClient.IgnitionStoreParams) error
}

// Verify interface compliance at compile time.
var _ alibabaClient.Client = (*FakeClient)(nil)

// ── CAPI-facing methods ─────────────────────────────────────────────────────────

func (f *FakeClient) DescribeInstanceByID(id string) (*alibabaClient.InstanceDescription, error) {
	if f.DescribeInstanceByIDFn != nil {
		return f.DescribeInstanceByIDFn(id)
	}
	return nil, nil
}

func (f *FakeClient) CreateECSInstance(p alibabaClient.CreateInstanceParams) (*alibabaClient.CreateInstanceResponse, error) {
	if f.CreateECSInstanceFn != nil {
		return f.CreateECSInstanceFn(p)
	}
	return &alibabaClient.CreateInstanceResponse{InstanceID: "i-fake"}, nil
}

func (f *FakeClient) DeleteECSInstance(id string, force bool) error {
	if f.DeleteECSInstanceFn != nil {
		return f.DeleteECSInstanceFn(id, force)
	}
	return nil
}

func (f *FakeClient) FindInstanceByTag(region, key, value string) (string, error) {
	if f.FindInstanceByTagFn != nil {
		return f.FindInstanceByTagFn(region, key, value)
	}
	return "", nil
}

func (f *FakeClient) ModifyInstanceMetadata(instanceID, httpEndpoint, httpTokens string, hopLimit int) error {
	if f.ModifyInstanceMetadataFn != nil {
		return f.ModifyInstanceMetadataFn(instanceID, httpEndpoint, httpTokens, hopLimit)
	}
	return nil
}

func (f *FakeClient) PutIgnitionObject(p alibabaClient.IgnitionStoreParams) (string, error) {
	if f.PutIgnitionObjectFn != nil {
		return f.PutIgnitionObjectFn(p)
	}
	return "https://fake-bucket.oss-fake-internal.aliyuncs.com/" + p.Key, nil
}

func (f *FakeClient) DeleteIgnitionObject(p alibabaClient.IgnitionStoreParams) error {
	if f.DeleteIgnitionObjectFn != nil {
		return f.DeleteIgnitionObjectFn(p)
	}
	return nil
}

// ── ECS stubs ───────────────────────────────────────────────────────────────────

func (f *FakeClient) RunInstances(*ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
	return nil, fmt.Errorf("FakeClient: RunInstances not implemented")
}
func (f *FakeClient) CreateInstance(*ecs.CreateInstanceRequest) (*ecs.CreateInstanceResponse, error) {
	return nil, fmt.Errorf("FakeClient: CreateInstance not implemented")
}
func (f *FakeClient) DescribeInstances(*ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeInstances not implemented")
}
func (f *FakeClient) DeleteInstances(*ecs.DeleteInstancesRequest) (*ecs.DeleteInstancesResponse, error) {
	return nil, fmt.Errorf("FakeClient: DeleteInstances not implemented")
}
func (f *FakeClient) StartInstance(*ecs.StartInstanceRequest) (*ecs.StartInstanceResponse, error) {
	return nil, fmt.Errorf("FakeClient: StartInstance not implemented")
}
func (f *FakeClient) RebootInstance(*ecs.RebootInstanceRequest) (*ecs.RebootInstanceResponse, error) {
	return nil, fmt.Errorf("FakeClient: RebootInstance not implemented")
}
func (f *FakeClient) StopInstance(*ecs.StopInstanceRequest) (*ecs.StopInstanceResponse, error) {
	return nil, fmt.Errorf("FakeClient: StopInstance not implemented")
}
func (f *FakeClient) StartInstances(*ecs.StartInstancesRequest) (*ecs.StartInstancesResponse, error) {
	return nil, fmt.Errorf("FakeClient: StartInstances not implemented")
}
func (f *FakeClient) RebootInstances(*ecs.RebootInstancesRequest) (*ecs.RebootInstancesResponse, error) {
	return nil, fmt.Errorf("FakeClient: RebootInstances not implemented")
}
func (f *FakeClient) StopInstances(*ecs.StopInstancesRequest) (*ecs.StopInstancesResponse, error) {
	return nil, fmt.Errorf("FakeClient: StopInstances not implemented")
}
func (f *FakeClient) DeleteInstance(*ecs.DeleteInstanceRequest) (*ecs.DeleteInstanceResponse, error) {
	return nil, fmt.Errorf("FakeClient: DeleteInstance not implemented")
}
func (f *FakeClient) AttachInstanceRAMRole(*ecs.AttachInstanceRamRoleRequest) (*ecs.AttachInstanceRamRoleResponse, error) {
	return nil, fmt.Errorf("FakeClient: AttachInstanceRAMRole not implemented")
}
func (f *FakeClient) DetachInstanceRAMRole(*ecs.DetachInstanceRamRoleRequest) (*ecs.DetachInstanceRamRoleResponse, error) {
	return nil, fmt.Errorf("FakeClient: DetachInstanceRAMRole not implemented")
}
func (f *FakeClient) DescribeInstanceStatus(*ecs.DescribeInstanceStatusRequest) (*ecs.DescribeInstanceStatusResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeInstanceStatus not implemented")
}
func (f *FakeClient) ReActivateInstances(*ecs.ReActivateInstancesRequest) (*ecs.ReActivateInstancesResponse, error) {
	return nil, fmt.Errorf("FakeClient: ReActivateInstances not implemented")
}
func (f *FakeClient) DescribeUserData(*ecs.DescribeUserDataRequest) (*ecs.DescribeUserDataResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeUserData not implemented")
}
func (f *FakeClient) DescribeInstanceTypes(*ecs.DescribeInstanceTypesRequest) (*ecs.DescribeInstanceTypesResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeInstanceTypes not implemented")
}
func (f *FakeClient) ModifyInstanceAttribute(*ecs.ModifyInstanceAttributeRequest) (*ecs.ModifyInstanceAttributeResponse, error) {
	return nil, fmt.Errorf("FakeClient: ModifyInstanceAttribute not implemented")
}
func (f *FakeClient) ModifyInstanceMetadataOptions(*ecs.ModifyInstanceMetadataOptionsRequest) (*ecs.ModifyInstanceMetadataOptionsResponse, error) {
	return nil, fmt.Errorf("FakeClient: ModifyInstanceMetadataOptions not implemented")
}
func (f *FakeClient) TagResources(*ecs.TagResourcesRequest) (*ecs.TagResourcesResponse, error) {
	return nil, fmt.Errorf("FakeClient: TagResources not implemented")
}
func (f *FakeClient) ListTagResources(*ecs.ListTagResourcesRequest) (*ecs.ListTagResourcesResponse, error) {
	return nil, fmt.Errorf("FakeClient: ListTagResources not implemented")
}
func (f *FakeClient) UntagResources(*ecs.UntagResourcesRequest) (*ecs.UntagResourcesResponse, error) {
	return nil, fmt.Errorf("FakeClient: UntagResources not implemented")
}
func (f *FakeClient) AllocatePublicIPAddress(*ecs.AllocatePublicIpAddressRequest) (*ecs.AllocatePublicIpAddressResponse, error) {
	return nil, fmt.Errorf("FakeClient: AllocatePublicIPAddress not implemented")
}

// ── Disk stubs ──────────────────────────────────────────────────────────────────

func (f *FakeClient) CreateDisk(*ecs.CreateDiskRequest) (*ecs.CreateDiskResponse, error) {
	return nil, fmt.Errorf("FakeClient: CreateDisk not implemented")
}
func (f *FakeClient) AttachDisk(*ecs.AttachDiskRequest) (*ecs.AttachDiskResponse, error) {
	return nil, fmt.Errorf("FakeClient: AttachDisk not implemented")
}
func (f *FakeClient) DescribeDisks(*ecs.DescribeDisksRequest) (*ecs.DescribeDisksResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeDisks not implemented")
}
func (f *FakeClient) ModifyDiskChargeType(*ecs.ModifyDiskChargeTypeRequest) (*ecs.ModifyDiskChargeTypeResponse, error) {
	return nil, fmt.Errorf("FakeClient: ModifyDiskChargeType not implemented")
}
func (f *FakeClient) ModifyDiskAttribute(*ecs.ModifyDiskAttributeRequest) (*ecs.ModifyDiskAttributeResponse, error) {
	return nil, fmt.Errorf("FakeClient: ModifyDiskAttribute not implemented")
}
func (f *FakeClient) ModifyDiskSpec(*ecs.ModifyDiskSpecRequest) (*ecs.ModifyDiskSpecResponse, error) {
	return nil, fmt.Errorf("FakeClient: ModifyDiskSpec not implemented")
}
func (f *FakeClient) ReplaceSystemDisk(*ecs.ReplaceSystemDiskRequest) (*ecs.ReplaceSystemDiskResponse, error) {
	return nil, fmt.Errorf("FakeClient: ReplaceSystemDisk not implemented")
}
func (f *FakeClient) ReInitDisk(*ecs.ReInitDiskRequest) (*ecs.ReInitDiskResponse, error) {
	return nil, fmt.Errorf("FakeClient: ReInitDisk not implemented")
}
func (f *FakeClient) ResetDisk(*ecs.ResetDiskRequest) (*ecs.ResetDiskResponse, error) {
	return nil, fmt.Errorf("FakeClient: ResetDisk not implemented")
}
func (f *FakeClient) ResizeDisk(*ecs.ResizeDiskRequest) (*ecs.ResizeDiskResponse, error) {
	return nil, fmt.Errorf("FakeClient: ResizeDisk not implemented")
}
func (f *FakeClient) DetachDisk(*ecs.DetachDiskRequest) (*ecs.DetachDiskResponse, error) {
	return nil, fmt.Errorf("FakeClient: DetachDisk not implemented")
}
func (f *FakeClient) DeleteDisk(*ecs.DeleteDiskRequest) (*ecs.DeleteDiskResponse, error) {
	return nil, fmt.Errorf("FakeClient: DeleteDisk not implemented")
}

// ── Region / Zone / Image stubs ─────────────────────────────────────────────────

func (f *FakeClient) DescribeRegions(*ecs.DescribeRegionsRequest) (*ecs.DescribeRegionsResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeRegions not implemented")
}
func (f *FakeClient) DescribeZones(*ecs.DescribeZonesRequest) (*ecs.DescribeZonesResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeZones not implemented")
}
func (f *FakeClient) DescribeImages(*ecs.DescribeImagesRequest) (*ecs.DescribeImagesResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeImages not implemented")
}

// ── Security group stubs ────────────────────────────────────────────────────────

func (f *FakeClient) CreateSecurityGroup(*ecs.CreateSecurityGroupRequest) (*ecs.CreateSecurityGroupResponse, error) {
	return nil, fmt.Errorf("FakeClient: CreateSecurityGroup not implemented")
}
func (f *FakeClient) AuthorizeSecurityGroup(*ecs.AuthorizeSecurityGroupRequest) (*ecs.AuthorizeSecurityGroupResponse, error) {
	return nil, fmt.Errorf("FakeClient: AuthorizeSecurityGroup not implemented")
}
func (f *FakeClient) AuthorizeSecurityGroupEgress(*ecs.AuthorizeSecurityGroupEgressRequest) (*ecs.AuthorizeSecurityGroupEgressResponse, error) {
	return nil, fmt.Errorf("FakeClient: AuthorizeSecurityGroupEgress not implemented")
}
func (f *FakeClient) RevokeSecurityGroup(*ecs.RevokeSecurityGroupRequest) (*ecs.RevokeSecurityGroupResponse, error) {
	return nil, fmt.Errorf("FakeClient: RevokeSecurityGroup not implemented")
}
func (f *FakeClient) RevokeSecurityGroupEgress(*ecs.RevokeSecurityGroupEgressRequest) (*ecs.RevokeSecurityGroupEgressResponse, error) {
	return nil, fmt.Errorf("FakeClient: RevokeSecurityGroupEgress not implemented")
}
func (f *FakeClient) JoinSecurityGroup(*ecs.JoinSecurityGroupRequest) (*ecs.JoinSecurityGroupResponse, error) {
	return nil, fmt.Errorf("FakeClient: JoinSecurityGroup not implemented")
}
func (f *FakeClient) LeaveSecurityGroup(*ecs.LeaveSecurityGroupRequest) (*ecs.LeaveSecurityGroupResponse, error) {
	return nil, fmt.Errorf("FakeClient: LeaveSecurityGroup not implemented")
}
func (f *FakeClient) DescribeSecurityGroupAttribute(*ecs.DescribeSecurityGroupAttributeRequest) (*ecs.DescribeSecurityGroupAttributeResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeSecurityGroupAttribute not implemented")
}
func (f *FakeClient) DescribeSecurityGroups(*ecs.DescribeSecurityGroupsRequest) (*ecs.DescribeSecurityGroupsResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeSecurityGroups not implemented")
}
func (f *FakeClient) DescribeSecurityGroupReferences(*ecs.DescribeSecurityGroupReferencesRequest) (*ecs.DescribeSecurityGroupReferencesResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeSecurityGroupReferences not implemented")
}
func (f *FakeClient) ModifySecurityGroupAttribute(*ecs.ModifySecurityGroupAttributeRequest) (*ecs.ModifySecurityGroupAttributeResponse, error) {
	return nil, fmt.Errorf("FakeClient: ModifySecurityGroupAttribute not implemented")
}
func (f *FakeClient) ModifySecurityGroupEgressRule(*ecs.ModifySecurityGroupEgressRuleRequest) (*ecs.ModifySecurityGroupEgressRuleResponse, error) {
	return nil, fmt.Errorf("FakeClient: ModifySecurityGroupEgressRule not implemented")
}
func (f *FakeClient) ModifySecurityGroupPolicy(*ecs.ModifySecurityGroupPolicyRequest) (*ecs.ModifySecurityGroupPolicyResponse, error) {
	return nil, fmt.Errorf("FakeClient: ModifySecurityGroupPolicy not implemented")
}
func (f *FakeClient) ModifySecurityGroupRule(*ecs.ModifySecurityGroupRuleRequest) (*ecs.ModifySecurityGroupRuleResponse, error) {
	return nil, fmt.Errorf("FakeClient: ModifySecurityGroupRule not implemented")
}
func (f *FakeClient) DeleteSecurityGroup(*ecs.DeleteSecurityGroupRequest) (*ecs.DeleteSecurityGroupResponse, error) {
	return nil, fmt.Errorf("FakeClient: DeleteSecurityGroup not implemented")
}

// ── VPC stubs ───────────────────────────────────────────────────────────────────

func (f *FakeClient) DescribeVpcs(*vpc.DescribeVpcsRequest) (*vpc.DescribeVpcsResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeVpcs not implemented")
}
func (f *FakeClient) CreateVpc(*vpc.CreateVpcRequest) (*vpc.CreateVpcResponse, error) {
	return nil, fmt.Errorf("FakeClient: CreateVpc not implemented")
}
func (f *FakeClient) DeleteVpc(*vpc.DeleteVpcRequest) (*vpc.DeleteVpcResponse, error) {
	return nil, fmt.Errorf("FakeClient: DeleteVpc not implemented")
}
func (f *FakeClient) DescribeVSwitches(*vpc.DescribeVSwitchesRequest) (*vpc.DescribeVSwitchesResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeVSwitches not implemented")
}
func (f *FakeClient) CreateVSwitch(*vpc.CreateVSwitchRequest) (*vpc.CreateVSwitchResponse, error) {
	return nil, fmt.Errorf("FakeClient: CreateVSwitch not implemented")
}
func (f *FakeClient) DeleteVSwitch(*vpc.DeleteVSwitchRequest) (*vpc.DeleteVSwitchResponse, error) {
	return nil, fmt.Errorf("FakeClient: DeleteVSwitch not implemented")
}
func (f *FakeClient) CreateNatGateway(*vpc.CreateNatGatewayRequest) (*vpc.CreateNatGatewayResponse, error) {
	return nil, fmt.Errorf("FakeClient: CreateNatGateway not implemented")
}
func (f *FakeClient) DescribeNatGateways(*vpc.DescribeNatGatewaysRequest) (*vpc.DescribeNatGatewaysResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeNatGateways not implemented")
}
func (f *FakeClient) DeleteNatGateway(*vpc.DeleteNatGatewayRequest) (*vpc.DeleteNatGatewayResponse, error) {
	return nil, fmt.Errorf("FakeClient: DeleteNatGateway not implemented")
}
func (f *FakeClient) AllocateEipAddress(*vpc.AllocateEipAddressRequest) (*vpc.AllocateEipAddressResponse, error) {
	return nil, fmt.Errorf("FakeClient: AllocateEipAddress not implemented")
}
func (f *FakeClient) AssociateEipAddress(*vpc.AssociateEipAddressRequest) (*vpc.AssociateEipAddressResponse, error) {
	return nil, fmt.Errorf("FakeClient: AssociateEipAddress not implemented")
}
func (f *FakeClient) ModifyEipAddressAttribute(*vpc.ModifyEipAddressAttributeRequest) (*vpc.ModifyEipAddressAttributeResponse, error) {
	return nil, fmt.Errorf("FakeClient: ModifyEipAddressAttribute not implemented")
}
func (f *FakeClient) DescribeEipAddresses(*vpc.DescribeEipAddressesRequest) (*vpc.DescribeEipAddressesResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeEipAddresses not implemented")
}
func (f *FakeClient) UnassociateEipAddress(*vpc.UnassociateEipAddressRequest) (*vpc.UnassociateEipAddressResponse, error) {
	return nil, fmt.Errorf("FakeClient: UnassociateEipAddress not implemented")
}
func (f *FakeClient) ReleaseEipAddress(*vpc.ReleaseEipAddressRequest) (*vpc.ReleaseEipAddressResponse, error) {
	return nil, fmt.Errorf("FakeClient: ReleaseEipAddress not implemented")
}

// ── SLB stubs ───────────────────────────────────────────────────────────────────

func (f *FakeClient) DescribeLoadBalancers(*slb.DescribeLoadBalancersRequest) (*slb.DescribeLoadBalancersResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeLoadBalancers not implemented")
}
func (f *FakeClient) CreateLoadBalancer(*slb.CreateLoadBalancerRequest) (*slb.CreateLoadBalancerResponse, error) {
	return nil, fmt.Errorf("FakeClient: CreateLoadBalancer not implemented")
}
func (f *FakeClient) DeleteLoadBalancer(*slb.DeleteLoadBalancerRequest) (*slb.DeleteLoadBalancerResponse, error) {
	return nil, fmt.Errorf("FakeClient: DeleteLoadBalancer not implemented")
}
func (f *FakeClient) CreateLoadBalancerTCPListener(*slb.CreateLoadBalancerTCPListenerRequest) (*slb.CreateLoadBalancerTCPListenerResponse, error) {
	return nil, fmt.Errorf("FakeClient: CreateLoadBalancerTCPListener not implemented")
}
func (f *FakeClient) SetLoadBalancerTCPListenerAttribute(*slb.SetLoadBalancerTCPListenerAttributeRequest) (*slb.SetLoadBalancerTCPListenerAttributeResponse, error) {
	return nil, fmt.Errorf("FakeClient: SetLoadBalancerTCPListenerAttribute not implemented")
}
func (f *FakeClient) DescribeLoadBalancerTCPListenerAttribute(*slb.DescribeLoadBalancerTCPListenerAttributeRequest) (*slb.DescribeLoadBalancerTCPListenerAttributeResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeLoadBalancerTCPListenerAttribute not implemented")
}
func (f *FakeClient) CreateLoadBalancerUDPListener(*slb.CreateLoadBalancerUDPListenerRequest) (*slb.CreateLoadBalancerUDPListenerResponse, error) {
	return nil, fmt.Errorf("FakeClient: CreateLoadBalancerUDPListener not implemented")
}
func (f *FakeClient) SetLoadBalancerUDPListenerAttribute(*slb.SetLoadBalancerUDPListenerAttributeRequest) (*slb.SetLoadBalancerUDPListenerAttributeResponse, error) {
	return nil, fmt.Errorf("FakeClient: SetLoadBalancerUDPListenerAttribute not implemented")
}
func (f *FakeClient) DescribeLoadBalancerUDPListenerAttribute(*slb.DescribeLoadBalancerUDPListenerAttributeRequest) (*slb.DescribeLoadBalancerUDPListenerAttributeResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeLoadBalancerUDPListenerAttribute not implemented")
}
func (f *FakeClient) CreateLoadBalancerHTTPListener(*slb.CreateLoadBalancerHTTPListenerRequest) (*slb.CreateLoadBalancerHTTPListenerResponse, error) {
	return nil, fmt.Errorf("FakeClient: CreateLoadBalancerHTTPListener not implemented")
}
func (f *FakeClient) SetLoadBalancerHTTPListenerAttribute(*slb.SetLoadBalancerHTTPListenerAttributeRequest) (*slb.SetLoadBalancerHTTPListenerAttributeResponse, error) {
	return nil, fmt.Errorf("FakeClient: SetLoadBalancerHTTPListenerAttribute not implemented")
}
func (f *FakeClient) DescribeLoadBalancerHTTPListenerAttribute(*slb.DescribeLoadBalancerHTTPListenerAttributeRequest) (*slb.DescribeLoadBalancerHTTPListenerAttributeResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeLoadBalancerHTTPListenerAttribute not implemented")
}
func (f *FakeClient) CreateLoadBalancerHTTPSListener(*slb.CreateLoadBalancerHTTPSListenerRequest) (*slb.CreateLoadBalancerHTTPSListenerResponse, error) {
	return nil, fmt.Errorf("FakeClient: CreateLoadBalancerHTTPSListener not implemented")
}
func (f *FakeClient) SetLoadBalancerHTTPSListenerAttribute(*slb.SetLoadBalancerHTTPSListenerAttributeRequest) (*slb.SetLoadBalancerHTTPSListenerAttributeResponse, error) {
	return nil, fmt.Errorf("FakeClient: SetLoadBalancerHTTPSListenerAttribute not implemented")
}
func (f *FakeClient) DescribeLoadBalancerHTTPSListenerAttribute(*slb.DescribeLoadBalancerHTTPSListenerAttributeRequest) (*slb.DescribeLoadBalancerHTTPSListenerAttributeResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeLoadBalancerHTTPSListenerAttribute not implemented")
}
func (f *FakeClient) StartLoadBalancerListener(*slb.StartLoadBalancerListenerRequest) (*slb.StartLoadBalancerListenerResponse, error) {
	return nil, fmt.Errorf("FakeClient: StartLoadBalancerListener not implemented")
}
func (f *FakeClient) StopLoadBalancerListener(*slb.StopLoadBalancerListenerRequest) (*slb.StopLoadBalancerListenerResponse, error) {
	return nil, fmt.Errorf("FakeClient: StopLoadBalancerListener not implemented")
}
func (f *FakeClient) DeleteLoadBalancerListener(*slb.DeleteLoadBalancerListenerRequest) (*slb.DeleteLoadBalancerListenerResponse, error) {
	return nil, fmt.Errorf("FakeClient: DeleteLoadBalancerListener not implemented")
}
func (f *FakeClient) DescribeLoadBalancerListeners(*slb.DescribeLoadBalancerListenersRequest) (*slb.DescribeLoadBalancerListenersResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeLoadBalancerListeners not implemented")
}
func (f *FakeClient) AddBackendServers(*slb.AddBackendServersRequest) (*slb.AddBackendServersResponse, error) {
	return nil, fmt.Errorf("FakeClient: AddBackendServers not implemented")
}
func (f *FakeClient) RemoveBackendServers(*slb.RemoveBackendServersRequest) (*slb.RemoveBackendServersResponse, error) {
	return nil, fmt.Errorf("FakeClient: RemoveBackendServers not implemented")
}
func (f *FakeClient) SetBackendServers(*slb.SetBackendServersRequest) (*slb.SetBackendServersResponse, error) {
	return nil, fmt.Errorf("FakeClient: SetBackendServers not implemented")
}
func (f *FakeClient) DescribeHealthStatus(*slb.DescribeHealthStatusRequest) (*slb.DescribeHealthStatusResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeHealthStatus not implemented")
}
func (f *FakeClient) CreateVServerGroup(*slb.CreateVServerGroupRequest) (*slb.CreateVServerGroupResponse, error) {
	return nil, fmt.Errorf("FakeClient: CreateVServerGroup not implemented")
}
func (f *FakeClient) SetVServerGroupAttribute(*slb.SetVServerGroupAttributeRequest) (*slb.SetVServerGroupAttributeResponse, error) {
	return nil, fmt.Errorf("FakeClient: SetVServerGroupAttribute not implemented")
}
func (f *FakeClient) AddVServerGroupBackendServers(*slb.AddVServerGroupBackendServersRequest) (*slb.AddVServerGroupBackendServersResponse, error) {
	return nil, fmt.Errorf("FakeClient: AddVServerGroupBackendServers not implemented")
}
func (f *FakeClient) RemoveVServerGroupBackendServers(*slb.RemoveVServerGroupBackendServersRequest) (*slb.RemoveVServerGroupBackendServersResponse, error) {
	return nil, fmt.Errorf("FakeClient: RemoveVServerGroupBackendServers not implemented")
}
func (f *FakeClient) ModifyVServerGroupBackendServers(*slb.ModifyVServerGroupBackendServersRequest) (*slb.ModifyVServerGroupBackendServersResponse, error) {
	return nil, fmt.Errorf("FakeClient: ModifyVServerGroupBackendServers not implemented")
}
func (f *FakeClient) DeleteVServerGroup(*slb.DeleteVServerGroupRequest) (*slb.DeleteVServerGroupResponse, error) {
	return nil, fmt.Errorf("FakeClient: DeleteVServerGroup not implemented")
}
func (f *FakeClient) DescribeVServerGroups(*slb.DescribeVServerGroupsRequest) (*slb.DescribeVServerGroupsResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeVServerGroups not implemented")
}
func (f *FakeClient) DescribeVServerGroupAttribute(*slb.DescribeVServerGroupAttributeRequest) (*slb.DescribeVServerGroupAttributeResponse, error) {
	return nil, fmt.Errorf("FakeClient: DescribeVServerGroupAttribute not implemented")
}

// ── ResourceManager stubs ───────────────────────────────────────────────────────

func (f *FakeClient) ListResourceGroups(*resourcemanager.ListResourceGroupsRequest) (*resourcemanager.ListResourceGroupsResponse, error) {
	return nil, fmt.Errorf("FakeClient: ListResourceGroups not implemented")
}
