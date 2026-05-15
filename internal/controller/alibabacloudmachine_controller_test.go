package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/SammZhu/openshift-capi-alicloud/api/v1beta1"
	alibabaClient "github.com/SammZhu/openshift-capi-alicloud/pkg/client"
	fakeclient "github.com/SammZhu/openshift-capi-alicloud/pkg/client/fake"
)

// newTestScheme registers all required API types.
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := infrav1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme infrav1: %v", err)
	}
	if err := clusterv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme clusterv1: %v", err)
	}
	return s
}

func ptr[T any](v T) *T { return &v }

// fakeBuilder returns a ClientBuilderFunc that always returns the given ECS fake.
func fakeBuilder(c alibabaClient.Client) alibabaClient.ClientBuilderFunc {
	return func(_ runtimeclient.Client, _ string) (alibabaClient.Client, error) {
		return c, nil
	}
}

// ── resolveFailureDomain ────────────────────────────────────────────────────────

func TestResolveFailureDomain_NoFailureDomain(t *testing.T) {
	r := &AlibabaCloudMachineReconciler{}
	machine := &clusterv1.Machine{}
	cluster := &infrav1.AlibabaCloudCluster{}
	aliMachine := &infrav1.AlibabaCloudMachine{}

	if err := r.resolveFailureDomain(machine, cluster, aliMachine); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if aliMachine.Spec.ZoneID != "" || aliMachine.Spec.VSwitchID != "" {
		t.Error("expected ZoneID and VSwitchID to remain empty")
	}
}

func TestResolveFailureDomain_MatchFound(t *testing.T) {
	r := &AlibabaCloudMachineReconciler{}
	machine := &clusterv1.Machine{
		Spec: clusterv1.MachineSpec{FailureDomain: "cn-hangzhou-h"},
	}
	cluster := &infrav1.AlibabaCloudCluster{
		Status: infrav1.AlibabaCloudClusterStatus{
			FailureDomains: []clusterv1.FailureDomain{
				{Name: "cn-hangzhou-h", Attributes: map[string]string{"vSwitchID": "vsw-aaa"}},
			},
		},
	}
	aliMachine := &infrav1.AlibabaCloudMachine{}

	if err := r.resolveFailureDomain(machine, cluster, aliMachine); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if aliMachine.Spec.ZoneID != "cn-hangzhou-h" {
		t.Errorf("ZoneID = %q, want cn-hangzhou-h", aliMachine.Spec.ZoneID)
	}
	if aliMachine.Spec.VSwitchID != "vsw-aaa" {
		t.Errorf("VSwitchID = %q, want vsw-aaa", aliMachine.Spec.VSwitchID)
	}
}

func TestResolveFailureDomain_NotFound(t *testing.T) {
	r := &AlibabaCloudMachineReconciler{}
	machine := &clusterv1.Machine{
		Spec: clusterv1.MachineSpec{FailureDomain: "cn-hangzhou-z"},
	}
	if err := r.resolveFailureDomain(machine, &infrav1.AlibabaCloudCluster{}, &infrav1.AlibabaCloudMachine{}); err == nil {
		t.Fatal("expected error for missing failure domain")
	}
}

func TestResolveFailureDomain_ExplicitSpecNotOverridden(t *testing.T) {
	r := &AlibabaCloudMachineReconciler{}
	machine := &clusterv1.Machine{
		Spec: clusterv1.MachineSpec{FailureDomain: "cn-hangzhou-h"},
	}
	cluster := &infrav1.AlibabaCloudCluster{
		Status: infrav1.AlibabaCloudClusterStatus{
			FailureDomains: []clusterv1.FailureDomain{
				{Name: "cn-hangzhou-h", Attributes: map[string]string{"vSwitchID": "vsw-from-fd"}},
			},
		},
	}
	aliMachine := &infrav1.AlibabaCloudMachine{
		Spec: infrav1.AlibabaCloudMachineSpec{ZoneID: "cn-hangzhou-h", VSwitchID: "vsw-explicit"},
	}

	if err := r.resolveFailureDomain(machine, cluster, aliMachine); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if aliMachine.Spec.VSwitchID != "vsw-explicit" {
		t.Errorf("VSwitchID was overwritten: got %q, want vsw-explicit", aliMachine.Spec.VSwitchID)
	}
}

// ── syncInstanceStatus ──────────────────────────────────────────────────────────

func TestSyncInstanceStatus_SetsAddresses(t *testing.T) {
	r := &AlibabaCloudMachineReconciler{}
	m := &infrav1.AlibabaCloudMachine{}
	info := &instanceInfo{
		InstanceID: "i-123",
		State:      infrav1.InstanceStateRunning,
		PrivateIP:  "192.168.1.10",
		PublicIP:   "47.1.2.3",
	}
	r.syncInstanceStatus(m, info)

	if *m.Status.InstanceID != "i-123" {
		t.Errorf("InstanceID = %q, want i-123", *m.Status.InstanceID)
	}
	if len(m.Status.Addresses) != 2 {
		t.Fatalf("expected 2 addresses, got %d", len(m.Status.Addresses))
	}
	if m.Status.Addresses[0].Type != clusterv1.MachineInternalIP || m.Status.Addresses[0].Address != "192.168.1.10" {
		t.Errorf("unexpected internal address: %+v", m.Status.Addresses[0])
	}
	if m.Status.Addresses[1].Type != clusterv1.MachineExternalIP || m.Status.Addresses[1].Address != "47.1.2.3" {
		t.Errorf("unexpected external address: %+v", m.Status.Addresses[1])
	}
}

func TestSyncInstanceStatus_NoPublicIP(t *testing.T) {
	r := &AlibabaCloudMachineReconciler{}
	m := &infrav1.AlibabaCloudMachine{}
	r.syncInstanceStatus(m, &instanceInfo{InstanceID: "i-456", State: infrav1.InstanceStateRunning, PrivateIP: "10.0.0.5"})

	if len(m.Status.Addresses) != 1 {
		t.Fatalf("expected 1 address (private only), got %d", len(m.Status.Addresses))
	}
	if m.Status.Addresses[0].Type != clusterv1.MachineInternalIP {
		t.Errorf("expected InternalIP type, got %v", m.Status.Addresses[0].Type)
	}
}

// ── findOrCreateInstance (state machine) ────────────────────────────────────────

func TestFindOrCreate_PendingRequeues(t *testing.T) {
	fakeECS := &fakeclient.FakeClient{
		DescribeInstanceByIDFn: func(id string) (*alibabaClient.InstanceDescription, error) {
			return &alibabaClient.InstanceDescription{InstanceID: id, Status: string(infrav1.InstanceStatePending)}, nil
		},
	}
	r := &AlibabaCloudMachineReconciler{}
	id := "i-pending"
	aliMachine := &infrav1.AlibabaCloudMachine{Status: infrav1.AlibabaCloudMachineStatus{InstanceID: &id}}

	info, err := r.findOrCreateInstance(context.Background(), fakeECS, &clusterv1.Machine{}, &infrav1.AlibabaCloudCluster{}, aliMachine)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if info != nil {
		t.Errorf("expected nil info (pending → requeue), got %+v", info)
	}
}

func TestFindOrCreate_RunningReturnsInfo(t *testing.T) {
	fakeECS := &fakeclient.FakeClient{
		DescribeInstanceByIDFn: func(id string) (*alibabaClient.InstanceDescription, error) {
			return &alibabaClient.InstanceDescription{
				InstanceID:     id,
				Status:         string(infrav1.InstanceStateRunning),
				InnerIpAddress: struct{ IpAddress []string }{IpAddress: []string{"10.0.0.1"}},
			}, nil
		},
	}
	r := &AlibabaCloudMachineReconciler{}
	id := "i-running"
	aliMachine := &infrav1.AlibabaCloudMachine{Status: infrav1.AlibabaCloudMachineStatus{InstanceID: &id}}

	info, err := r.findOrCreateInstance(context.Background(), fakeECS, &clusterv1.Machine{}, &infrav1.AlibabaCloudCluster{}, aliMachine)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil info for Running instance")
	}
	if info.State != infrav1.InstanceStateRunning {
		t.Errorf("State = %q, want Running", info.State)
	}
}

func TestFindOrCreate_DeletedIsError(t *testing.T) {
	fakeECS := &fakeclient.FakeClient{
		DescribeInstanceByIDFn: func(id string) (*alibabaClient.InstanceDescription, error) {
			return &alibabaClient.InstanceDescription{InstanceID: id, Status: string(infrav1.InstanceStateDeleted)}, nil
		},
	}
	r := &AlibabaCloudMachineReconciler{}
	id := "i-deleted"
	aliMachine := &infrav1.AlibabaCloudMachine{Status: infrav1.AlibabaCloudMachineStatus{InstanceID: &id}}

	if _, err := r.findOrCreateInstance(context.Background(), fakeECS, &clusterv1.Machine{}, &infrav1.AlibabaCloudCluster{}, aliMachine); err == nil {
		t.Fatal("expected error for Deleted instance")
	}
}

func TestFindOrCreate_DisappearedIsError(t *testing.T) {
	fakeECS := &fakeclient.FakeClient{
		DescribeInstanceByIDFn: func(id string) (*alibabaClient.InstanceDescription, error) {
			return nil, nil
		},
	}
	r := &AlibabaCloudMachineReconciler{}
	id := "i-gone"
	aliMachine := &infrav1.AlibabaCloudMachine{Status: infrav1.AlibabaCloudMachineStatus{InstanceID: &id}}

	if _, err := r.findOrCreateInstance(context.Background(), fakeECS, &clusterv1.Machine{}, &infrav1.AlibabaCloudCluster{}, aliMachine); err == nil {
		t.Fatal("expected error when instance has disappeared")
	}
}

func TestFindOrCreate_NoInstanceIDCreatesNew(t *testing.T) {
	created := false
	fakeECS := &fakeclient.FakeClient{
		CreateECSInstanceFn: func(p alibabaClient.CreateInstanceParams) (*alibabaClient.CreateInstanceResponse, error) {
			created = true
			return &alibabaClient.CreateInstanceResponse{InstanceID: "i-new"}, nil
		},
	}
	r := &AlibabaCloudMachineReconciler{}
	aliMachine := &infrav1.AlibabaCloudMachine{
		Spec: infrav1.AlibabaCloudMachineSpec{RegionID: "cn-hangzhou", InstanceType: "ecs.c6.large", ImageID: "m-test"},
	}

	info, err := r.findOrCreateInstance(context.Background(), fakeECS, &clusterv1.Machine{}, &infrav1.AlibabaCloudCluster{}, aliMachine)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Error("expected CreateECSInstance to be called")
	}
	if info != nil {
		t.Error("expected nil info after create (should requeue)")
	}
	if aliMachine.Status.InstanceID == nil || *aliMachine.Status.InstanceID != "i-new" {
		t.Errorf("InstanceID not set correctly, got %v", aliMachine.Status.InstanceID)
	}
}

// ── Reconcile — top-level paths ─────────────────────────────────────────────────

func TestReconcile_MachineNotFound(t *testing.T) {
	scheme := newTestScheme(t)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &AlibabaCloudMachineReconciler{Client: k8sClient, Scheme: scheme}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "ghost"},
	})
	if err != nil {
		t.Fatalf("expected no error for not-found machine, got %v", err)
	}
	if res.Requeue || res.RequeueAfter != 0 {
		t.Errorf("expected empty Result, got %+v", res)
	}
}

func TestReconcile_ClusterNotReady_Requeues(t *testing.T) {
	const ns = "default"

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: ns},
		Spec: clusterv1.ClusterSpec{
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: infrav1.GroupVersion.Group,
				Kind:     "AlibabaCloudCluster",
				Name:     "test-cluster",
			},
		},
	}
	aliCluster := &infrav1.AlibabaCloudCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: ns},
		Spec:       infrav1.AlibabaCloudClusterSpec{Region: "cn-hangzhou"},
		Status:     infrav1.AlibabaCloudClusterStatus{Ready: false},
	}
	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: ns,
			Labels:    map[string]string{clusterv1.ClusterNameLabel: "test-cluster"},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: "test-cluster",
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				Kind: "AlibabaCloudMachine",
				Name: "test-alimachine",
			},
		},
	}
	aliMachine := &infrav1.AlibabaCloudMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-alimachine",
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: clusterv1.GroupVersion.String(),
					Kind:       "Machine",
					Name:       "test-machine",
					Controller: ptr(true),
				},
			},
		},
		Spec: infrav1.AlibabaCloudMachineSpec{
			InstanceType: "ecs.c6.large",
			ImageID:      "m-test",
			RegionID:     "cn-hangzhou",
		},
	}

	scheme := newTestScheme(t)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).
		WithRuntimeObjects(cluster, aliCluster, machine, aliMachine).
		WithStatusSubresource(&infrav1.AlibabaCloudMachine{}, &infrav1.AlibabaCloudCluster{}).
		Build()

	r := &AlibabaCloudMachineReconciler{
		Client:                    k8sClient,
		Scheme:                    scheme,
		AlibabaCloudClientBuilder: fakeBuilder(&fakeclient.FakeClient{}),
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: ns, Name: "test-alimachine"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Errorf("expected non-zero RequeueAfter when cluster is not ready, got %v", res)
	}
}
