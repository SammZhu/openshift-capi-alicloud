package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	sdkerrors "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

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

// G8: createInstance must persist the resolved region onto Spec.RegionID (durable
// on the spec) so the delete path can resolve a region from the machine alone and
// sweep a tagged orphan even if Spec.ProviderID/Status.InstanceID writes are lost.
func TestFindOrCreate_PersistsResolvedRegionOnSpec(t *testing.T) {
	var gotParamRegion string
	fakeECS := &fakeclient.FakeClient{
		CreateECSInstanceFn: func(p alibabaClient.CreateInstanceParams) (*alibabaClient.CreateInstanceResponse, error) {
			gotParamRegion = p.RegionID
			return &alibabaClient.CreateInstanceResponse{InstanceID: "i-new"}, nil
		},
	}
	r := &AlibabaCloudMachineReconciler{}
	// Machine omits RegionID — region must come from the cluster and be persisted.
	aliMachine := &infrav1.AlibabaCloudMachine{
		Spec: infrav1.AlibabaCloudMachineSpec{InstanceType: "ecs.c6.large", ImageID: "m-test"},
	}
	aliCluster := &infrav1.AlibabaCloudCluster{Spec: infrav1.AlibabaCloudClusterSpec{Region: "cn-shanghai"}}

	if _, err := r.findOrCreateInstance(context.Background(), fakeECS, &clusterv1.Machine{}, aliCluster, aliMachine); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if aliMachine.Spec.RegionID != "cn-shanghai" {
		t.Errorf("Spec.RegionID not persisted, got %q want cn-shanghai", aliMachine.Spec.RegionID)
	}
	if gotParamRegion != "cn-shanghai" {
		t.Errorf("RunInstances used region %q, want cn-shanghai", gotParamRegion)
	}
	if aliMachine.Spec.ProviderID == nil || *aliMachine.Spec.ProviderID != "alicloud://cn-shanghai.i-new" {
		t.Errorf("providerID = %v, want alicloud://cn-shanghai.i-new", aliMachine.Spec.ProviderID)
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

// ── P3-CAPA.1 region resolution + providerID format ─────────────────────────

func TestResolveRegion_SpecWins(t *testing.T) {
	m := &infrav1.AlibabaCloudMachine{Spec: infrav1.AlibabaCloudMachineSpec{RegionID: "cn-hangzhou"}}
	c := &infrav1.AlibabaCloudCluster{Spec: infrav1.AlibabaCloudClusterSpec{Region: "cn-wulanchabu"}}
	if got := resolveRegion(m, c); got != "cn-hangzhou" {
		t.Fatalf("want cn-hangzhou, got %q", got)
	}
}

func TestResolveRegion_FallsBackToCluster(t *testing.T) {
	m := &infrav1.AlibabaCloudMachine{} // Spec.RegionID empty
	c := &infrav1.AlibabaCloudCluster{Spec: infrav1.AlibabaCloudClusterSpec{Region: "cn-wulanchabu"}}
	if got := resolveRegion(m, c); got != "cn-wulanchabu" {
		t.Fatalf("want cn-wulanchabu, got %q", got)
	}
}

// providerIDFor MUST emit the DOT form to match the Alibaba CCM's
// Node.spec.providerID, so CAPI core's exact-match nodeRef binding works.
func TestProviderIDFor_DotFormatMatchesCCM(t *testing.T) {
	if got := providerIDFor("cn-wulanchabu", "i-abc123"); got != "alicloud://cn-wulanchabu.i-abc123" {
		t.Fatalf("want alicloud://cn-wulanchabu.i-abc123 (CCM dot form), got %q", got)
	}
}

func TestRegionFromMachine_SlashProviderID(t *testing.T) {
	m := &infrav1.AlibabaCloudMachine{Spec: infrav1.AlibabaCloudMachineSpec{
		ProviderID: ptr("alicloud://cn-wulanchabu/i-abc123"),
	}}
	got, err := regionFromMachine(m)
	if err != nil || got != "cn-wulanchabu" {
		t.Fatalf("want cn-wulanchabu/nil, got %q/%v", got, err)
	}
}

func TestRegionFromMachine_LegacyDotProviderID(t *testing.T) {
	m := &infrav1.AlibabaCloudMachine{Spec: infrav1.AlibabaCloudMachineSpec{
		ProviderID: ptr("alicloud://cn-hangzhou.i-legacy"),
	}}
	got, err := regionFromMachine(m)
	if err != nil || got != "cn-hangzhou" {
		t.Fatalf("want cn-hangzhou/nil, got %q/%v", got, err)
	}
}

func TestRegionFromMachine_EmptyRegionSegmentErrors(t *testing.T) {
	// The original bug: alicloud://.i-abc (dot form, empty region).
	m := &infrav1.AlibabaCloudMachine{Spec: infrav1.AlibabaCloudMachineSpec{
		ProviderID: ptr("alicloud://.i-abc"),
	}}
	if _, err := regionFromMachine(m); err == nil {
		t.Fatal("want error for empty region segment, got nil")
	}
}

// ── P3-CAPA.2 bootstrap data source precedence ──────────────────────────────

func TestGetUserData_PrefersMachineBootstrap(t *testing.T) {
	s := newTestScheme(t)
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "boot-secret", Namespace: "default"},
		Data:       map[string][]byte{"value": []byte("hello-ignition")},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(secret).Build()
	r := &AlibabaCloudMachineReconciler{Client: cl}
	machine := &clusterv1.Machine{Spec: clusterv1.MachineSpec{
		Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr("boot-secret")},
	}}
	m := &infrav1.AlibabaCloudMachine{ObjectMeta: metav1.ObjectMeta{Namespace: "default"}}
	got, err := r.getUserData(context.Background(), machine, m)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := base64.StdEncoding.EncodeToString([]byte("hello-ignition"))
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestGetUserData_NoBootstrapNoLegacyReturnsEmpty(t *testing.T) {
	s := newTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	r := &AlibabaCloudMachineReconciler{Client: cl}
	machine := &clusterv1.Machine{} // Bootstrap.DataSecretName nil
	m := &infrav1.AlibabaCloudMachine{ObjectMeta: metav1.ObjectMeta{Namespace: "default"}}
	got, err := r.getUserData(context.Background(), machine, m)
	if err != nil || got != "" {
		t.Fatalf("want empty/nil, got %q/%v", got, err)
	}
}

// ── reconcileDelete — wait-for-terminated (#26) ─────────────────────────────────

// machineWithInstance builds an AlibabaCloudMachine in deletion, carrying the
// finalizer + an InstanceID + a region so reconcileDelete can run end-to-end.
func machineWithInstance(id string) *infrav1.AlibabaCloudMachine {
	m := &infrav1.AlibabaCloudMachine{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "m"},
		Spec:       infrav1.AlibabaCloudMachineSpec{RegionID: "cn-hangzhou"},
		Status:     infrav1.AlibabaCloudMachineStatus{InstanceID: &id},
	}
	controllerutil.AddFinalizer(m, infrav1.MachineFinalizer)
	return m
}

func TestReconcileDelete_NoInstanceID_RemovesFinalizer(t *testing.T) {
	r := &AlibabaCloudMachineReconciler{}
	m := &infrav1.AlibabaCloudMachine{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "m"}}
	controllerutil.AddFinalizer(m, infrav1.MachineFinalizer)

	res, err := r.reconcileDelete(context.Background(), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("should not requeue when nothing was provisioned, got %v", res.RequeueAfter)
	}
	if controllerutil.ContainsFinalizer(m, infrav1.MachineFinalizer) {
		t.Error("finalizer should be removed when there is no instance")
	}
}

func TestReconcileDelete_StillRunning_IssuesDeleteAndRequeues(t *testing.T) {
	deleteCalls := 0
	fakeECS := &fakeclient.FakeClient{
		DescribeInstanceByIDFn: func(id string) (*alibabaClient.InstanceDescription, error) {
			return &alibabaClient.InstanceDescription{InstanceID: id, Status: string(infrav1.InstanceStateRunning)}, nil
		},
		DeleteECSInstanceFn: func(id string, force bool) error {
			deleteCalls++
			if !force {
				t.Error("delete should be forced")
			}
			return nil
		},
	}
	r := &AlibabaCloudMachineReconciler{AlibabaCloudClientBuilder: fakeBuilder(fakeECS)}
	m := machineWithInstance("i-run")

	res, err := r.reconcileDelete(context.Background(), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleteCalls != 1 {
		t.Errorf("expected exactly 1 delete call for a Running instance, got %d", deleteCalls)
	}
	if res.RequeueAfter == 0 {
		t.Error("should requeue to poll for termination")
	}
	if !controllerutil.ContainsFinalizer(m, infrav1.MachineFinalizer) {
		t.Error("finalizer must NOT be removed while the instance still exists")
	}
}

func TestReconcileDelete_Stopping_DoesNotReDelete(t *testing.T) {
	deleteCalls := 0
	fakeECS := &fakeclient.FakeClient{
		DescribeInstanceByIDFn: func(id string) (*alibabaClient.InstanceDescription, error) {
			return &alibabaClient.InstanceDescription{InstanceID: id, Status: string(infrav1.InstanceStateStopping)}, nil
		},
		DeleteECSInstanceFn: func(id string, force bool) error { deleteCalls++; return nil },
	}
	r := &AlibabaCloudMachineReconciler{AlibabaCloudClientBuilder: fakeBuilder(fakeECS)}
	m := machineWithInstance("i-stopping")

	res, err := r.reconcileDelete(context.Background(), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleteCalls != 0 {
		t.Errorf("must not re-issue delete against a Stopping instance, got %d calls", deleteCalls)
	}
	if res.RequeueAfter == 0 {
		t.Error("should requeue while instance is terminating")
	}
	if !controllerutil.ContainsFinalizer(m, infrav1.MachineFinalizer) {
		t.Error("finalizer must remain until the instance is gone")
	}
}

// P3-CAPA.12: a Stopped instance is a stable resting state on Alibaba Cloud —
// it never releases on its own. The controller MUST (re)issue the force delete
// against it, otherwise the finalizer hangs forever. (Regression: previously
// Stopped was lumped with Stopping/Deleted into the wait-only branch, which
// deadlocked deletion for any machine whose ECS ended up Stopped.)
func TestReconcileDelete_Stopped_IssuesDelete(t *testing.T) {
	deleteCalls := 0
	fakeECS := &fakeclient.FakeClient{
		DescribeInstanceByIDFn: func(id string) (*alibabaClient.InstanceDescription, error) {
			return &alibabaClient.InstanceDescription{InstanceID: id, Status: string(infrav1.InstanceStateStopped)}, nil
		},
		DeleteECSInstanceFn: func(id string, force bool) error {
			deleteCalls++
			if !force {
				t.Error("delete should be forced")
			}
			return nil
		},
	}
	r := &AlibabaCloudMachineReconciler{AlibabaCloudClientBuilder: fakeBuilder(fakeECS)}
	m := machineWithInstance("i-stopped")

	res, err := r.reconcileDelete(context.Background(), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleteCalls != 1 {
		t.Errorf("expected exactly 1 delete call to release a Stopped instance, got %d", deleteCalls)
	}
	if res.RequeueAfter == 0 {
		t.Error("should requeue to poll for termination")
	}
	if !controllerutil.ContainsFinalizer(m, infrav1.MachineFinalizer) {
		t.Error("finalizer must NOT be removed while the instance still exists")
	}
}

func TestReconcileDelete_Gone_RemovesFinalizer(t *testing.T) {
	deleteCalls := 0
	fakeECS := &fakeclient.FakeClient{
		DescribeInstanceByIDFn: func(id string) (*alibabaClient.InstanceDescription, error) {
			return nil, nil // instance no longer exists
		},
		DeleteECSInstanceFn: func(id string, force bool) error { deleteCalls++; return nil },
	}
	r := &AlibabaCloudMachineReconciler{AlibabaCloudClientBuilder: fakeBuilder(fakeECS)}
	m := machineWithInstance("i-gone")

	res, err := r.reconcileDelete(context.Background(), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("should not requeue once the instance is gone, got %v", res.RequeueAfter)
	}
	if deleteCalls != 0 {
		t.Errorf("should not call delete when the instance is already gone, got %d", deleteCalls)
	}
	if controllerutil.ContainsFinalizer(m, infrav1.MachineFinalizer) {
		t.Error("finalizer should be removed once the instance is confirmed gone")
	}
}

// ── reconcileNormal — terminal failure recording (#27) ──────────────────────────

func readyClusterMachine() (*clusterv1.Machine, *infrav1.AlibabaCloudCluster) {
	machine := &clusterv1.Machine{
		Spec: clusterv1.MachineSpec{
			Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr("boot")},
		},
	}
	cluster := &infrav1.AlibabaCloudCluster{
		Status: infrav1.AlibabaCloudClusterStatus{Ready: true},
	}
	return machine, cluster
}

func TestReconcileNormal_TerminalError_SetsFailureReason(t *testing.T) {
	fakeECS := &fakeclient.FakeClient{
		DescribeInstanceByIDFn: func(id string) (*alibabaClient.InstanceDescription, error) {
			return &alibabaClient.InstanceDescription{InstanceID: id, Status: string(infrav1.InstanceStateDeleted)}, nil
		},
	}
	r := &AlibabaCloudMachineReconciler{AlibabaCloudClientBuilder: fakeBuilder(fakeECS)}
	id := "i-term"
	aliMachine := &infrav1.AlibabaCloudMachine{
		Spec:   infrav1.AlibabaCloudMachineSpec{RegionID: "cn-hangzhou"},
		Status: infrav1.AlibabaCloudMachineStatus{InstanceID: &id},
	}
	machine, cluster := readyClusterMachine()

	res, err := r.reconcileNormal(context.Background(), machine, cluster, aliMachine)
	if err != nil {
		t.Fatalf("terminal failure should be swallowed (no requeue/err), got %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("terminal failure must not requeue, got RequeueAfter=%v", res.RequeueAfter)
	}
	if aliMachine.Status.FailureReason == nil || *aliMachine.Status.FailureReason != "InstanceTerminalState" {
		t.Errorf("FailureReason = %v, want InstanceTerminalState", aliMachine.Status.FailureReason)
	}
	if aliMachine.Status.FailureMessage == nil || *aliMachine.Status.FailureMessage == "" {
		t.Error("FailureMessage should be populated on terminal failure")
	}
	if aliMachine.Status.Ready {
		t.Error("Ready must be false on terminal failure")
	}
}

// P3-CAPA.11: a deleting AlibabaCloudMachine whose owner Machine is already
// gone must still reach reconcileDelete and release its finalizer (Reconcile
// must not bail in GetOwnerMachine on the delete path).
func TestReconcile_DeleteWithoutOwnerMachine_RemovesFinalizer(t *testing.T) {
	scheme := newTestScheme(t)
	now := metav1.Now()
	id := "i-orphan"
	acm := &infrav1.AlibabaCloudMachine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         "default",
			Name:              "orphan",
			Finalizers:        []string{infrav1.MachineFinalizer},
			DeletionTimestamp: &now,
		},
		Spec:   infrav1.AlibabaCloudMachineSpec{RegionID: "cn-hangzhou"},
		Status: infrav1.AlibabaCloudMachineStatus{InstanceID: &id},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(acm).Build()
	// ECS already gone → reconcileDelete confirms termination and drops finalizer.
	fakeECS := &fakeclient.FakeClient{
		DescribeInstanceByIDFn: func(string) (*alibabaClient.InstanceDescription, error) { return nil, nil },
	}
	r := &AlibabaCloudMachineReconciler{Client: k8sClient, Scheme: scheme, AlibabaCloudClientBuilder: fakeBuilder(fakeECS)}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "orphan"},
	})
	if err != nil {
		t.Fatalf("delete reconcile must not error when owner Machine is absent: %v", err)
	}
	got := &infrav1.AlibabaCloudMachine{}
	gerr := k8sClient.Get(context.Background(), runtimeclient.ObjectKey{Namespace: "default", Name: "orphan"}, got)
	if !apierrors.IsNotFound(gerr) {
		t.Fatalf("expected object gone after finalizer removal; get err=%v finalizers=%v", gerr, got.Finalizers)
	}
}

// G8: a delete whose Status.InstanceID write was lost (nil) must still find the
// live ECS via the durable Spec.ProviderID, delete it, and KEEP the finalizer —
// not shortcut to "nothing provisioned" and orphan a billable instance.
func TestReconcile_Delete_LostInstanceID_RecoversFromProviderID(t *testing.T) {
	scheme := newTestScheme(t)
	now := metav1.Now()
	pid := "alicloud://cn-hangzhou.i-leak01"
	acm := &infrav1.AlibabaCloudMachine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         "default",
			Name:              "lost-id",
			Finalizers:        []string{infrav1.MachineFinalizer},
			DeletionTimestamp: &now,
		},
		// Status.InstanceID intentionally nil (lost write); providerID is durable.
		Spec: infrav1.AlibabaCloudMachineSpec{ProviderID: &pid},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&infrav1.AlibabaCloudMachine{}).
		WithObjects(acm).Build()

	var deleted string
	fakeECS := &fakeclient.FakeClient{
		DescribeInstanceByIDFn: func(id string) (*alibabaClient.InstanceDescription, error) {
			if id != "i-leak01" {
				t.Errorf("describe id = %q, want i-leak01 (recovered from providerID)", id)
			}
			return &alibabaClient.InstanceDescription{InstanceID: id, Status: "Running"}, nil
		},
		DeleteECSInstanceFn: func(id string, _ bool) error { deleted = id; return nil },
	}
	r := &AlibabaCloudMachineReconciler{Client: k8sClient, Scheme: scheme, AlibabaCloudClientBuilder: fakeBuilder(fakeECS)}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "lost-id"},
	}); err != nil {
		t.Fatalf("delete reconcile errored: %v", err)
	}
	if deleted != "i-leak01" {
		t.Fatalf("expected DeleteECSInstance(i-leak01); got %q — instance would have been orphaned", deleted)
	}
	got := &infrav1.AlibabaCloudMachine{}
	if err := k8sClient.Get(context.Background(), runtimeclient.ObjectKey{Namespace: "default", Name: "lost-id"}, got); err != nil {
		t.Fatalf("object should still exist (finalizer kept while ECS terminates): %v", err)
	}
	if !controllerutil.ContainsFinalizer(got, infrav1.MachineFinalizer) {
		t.Error("finalizer must be retained until the ECS is confirmed gone")
	}
}

// G8: even with BOTH Status.InstanceID and Spec.ProviderID lost, a tagged ECS
// must be swept by the per-machine tag and deleted before the finalizer drops.
func TestReconcile_Delete_TagSweepCatchesOrphan(t *testing.T) {
	scheme := newTestScheme(t)
	now := metav1.Now()
	acm := &infrav1.AlibabaCloudMachine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         "default",
			Name:              "tagged-orphan",
			Finalizers:        []string{infrav1.MachineFinalizer},
			DeletionTimestamp: &now,
		},
		// No InstanceID, no providerID — only the durable region survives. The ECS
		// tag is the last handle on the billable instance.
		Spec: infrav1.AlibabaCloudMachineSpec{RegionID: "cn-hangzhou"},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(acm).Build()

	var deleted, sweepKey, sweepVal string
	fakeECS := &fakeclient.FakeClient{
		FindInstanceByTagFn: func(_, key, value string) (string, error) {
			sweepKey, sweepVal = key, value
			return "i-tagged-orphan", nil
		},
		DeleteECSInstanceFn: func(id string, _ bool) error { deleted = id; return nil },
	}
	r := &AlibabaCloudMachineReconciler{Client: k8sClient, Scheme: scheme, AlibabaCloudClientBuilder: fakeBuilder(fakeECS)}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "tagged-orphan"},
	}); err != nil {
		t.Fatalf("delete reconcile errored: %v", err)
	}
	if sweepKey != alibabaClient.MachineNameTagKey || sweepVal != "tagged-orphan" {
		t.Errorf("tag sweep used key=%q val=%q, want key=%q val=tagged-orphan", sweepKey, sweepVal, alibabaClient.MachineNameTagKey)
	}
	if deleted != "i-tagged-orphan" {
		t.Fatalf("expected DeleteECSInstance(i-tagged-orphan) from tag sweep; got %q — orphan leaked", deleted)
	}
	got := &infrav1.AlibabaCloudMachine{}
	if err := k8sClient.Get(context.Background(), runtimeclient.ObjectKey{Namespace: "default", Name: "tagged-orphan"}, got); err != nil {
		t.Fatalf("object should still exist until the orphan is confirmed gone: %v", err)
	}
	if !controllerutil.ContainsFinalizer(got, infrav1.MachineFinalizer) {
		t.Error("finalizer must be retained until the swept orphan is gone")
	}
}

// G14: post-boot IMDSv2 hardening fires once, only after the node has joined
// (owning Machine has a nodeRef), and is gated so it isn't repeated.
func TestMaybeHardenMetadata(t *testing.T) {
	joined := &clusterv1.Machine{}
	joined.Status.NodeRef.Name = "node-1"
	notJoined := &clusterv1.Machine{}
	hardenedTrue := true

	cases := []struct {
		name       string
		machine    *clusterv1.Machine
		opts       *infrav1.MetadataOptions
		status     *bool
		wantCalled bool
		wantTokens string
		wantStatus bool
	}{
		{"hardens after join", joined, &infrav1.MetadataOptions{HttpTokens: "optional", HttpTokensAfterBoot: "required"}, nil, true, "required", true},
		{"deferred before join", notJoined, &infrav1.MetadataOptions{HttpTokens: "optional", HttpTokensAfterBoot: "required"}, nil, false, "", false},
		{"already hardened", joined, &infrav1.MetadataOptions{HttpTokensAfterBoot: "required"}, &hardenedTrue, false, "", true},
		{"field unset", joined, &infrav1.MetadataOptions{HttpTokens: "optional"}, nil, false, "", false},
		{"no metadataOptions", joined, nil, nil, false, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotID, gotTokens string
			called := false
			fakeECS := &fakeclient.FakeClient{
				ModifyInstanceMetadataFn: func(id, _, tokens string, _ int) error {
					called = true
					gotID, gotTokens = id, tokens
					return nil
				},
			}
			r := &AlibabaCloudMachineReconciler{}
			m := &infrav1.AlibabaCloudMachine{
				Spec:   infrav1.AlibabaCloudMachineSpec{MetadataOptions: tc.opts},
				Status: infrav1.AlibabaCloudMachineStatus{MetadataHardened: tc.status},
			}
			r.maybeHardenMetadata(context.Background(), fakeECS, tc.machine, m, "i-test")

			if called != tc.wantCalled {
				t.Fatalf("ModifyInstanceMetadata called=%v, want %v", called, tc.wantCalled)
			}
			if tc.wantCalled {
				if gotID != "i-test" || gotTokens != tc.wantTokens {
					t.Errorf("modify(id=%q tokens=%q), want (i-test, %q)", gotID, gotTokens, tc.wantTokens)
				}
			}
			gotStatus := m.Status.MetadataHardened != nil && *m.Status.MetadataHardened
			if gotStatus != tc.wantStatus {
				t.Errorf("Status.MetadataHardened=%v, want %v", gotStatus, tc.wantStatus)
			}
		})
	}
}

// G14: a failed harden leaves MetadataHardened false so it retries next reconcile.
func TestMaybeHardenMetadata_FailureRetries(t *testing.T) {
	joined := &clusterv1.Machine{}
	joined.Status.NodeRef.Name = "node-1"
	fakeECS := &fakeclient.FakeClient{
		ModifyInstanceMetadataFn: func(string, string, string, int) error { return fmt.Errorf("api boom") },
	}
	r := &AlibabaCloudMachineReconciler{}
	m := &infrav1.AlibabaCloudMachine{
		Spec: infrav1.AlibabaCloudMachineSpec{MetadataOptions: &infrav1.MetadataOptions{HttpTokensAfterBoot: "required"}},
	}
	r.maybeHardenMetadata(context.Background(), fakeECS, joined, m, "i-test")
	if m.Status.MetadataHardened != nil && *m.Status.MetadataHardened {
		t.Error("MetadataHardened must stay false after a failed modify so it retries")
	}
}

func TestReconcileNormal_TransientError_NoFailureReason(t *testing.T) {
	fakeECS := &fakeclient.FakeClient{
		DescribeInstanceByIDFn: func(id string) (*alibabaClient.InstanceDescription, error) {
			return nil, fmt.Errorf("ECS API throttled") // transient, retryable
		},
	}
	r := &AlibabaCloudMachineReconciler{AlibabaCloudClientBuilder: fakeBuilder(fakeECS)}
	id := "i-transient"
	aliMachine := &infrav1.AlibabaCloudMachine{
		Spec:   infrav1.AlibabaCloudMachineSpec{RegionID: "cn-hangzhou"},
		Status: infrav1.AlibabaCloudMachineStatus{InstanceID: &id},
	}
	machine, cluster := readyClusterMachine()

	if _, err := r.reconcileNormal(context.Background(), machine, cluster, aliMachine); err == nil {
		t.Fatal("transient error must propagate so it is retried with backoff")
	}
	if aliMachine.Status.FailureReason != nil {
		t.Errorf("transient error must NOT set FailureReason, got %v", *aliMachine.Status.FailureReason)
	}
}

// ── classifyCreateError (FSD PR-B: capacity / spot terminal mapping) ─────────

func serverErr(code string) error {
	return sdkerrors.NewServerError(403, fmt.Sprintf(`{"Code":%q,"Message":"x"}`, code), "")
}

func TestClassifyCreateError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantReason string // "" means NOT a terminalError (recoverable)
	}{
		{"zone capacity", serverErr("Invalid.Zone.NotEnoughResource"), "InstanceCapacityExhausted"},
		{"insufficient capacity", serverErr("InsufficientCapacity"), "InstanceCapacityExhausted"},
		{"no stock", serverErr("OperationDenied.NoStock"), "InstanceCapacityExhausted"},
		{"spot strategy", serverErr("Invalid.SpotStrategy.NotSupported"), "SpotConfigurationRejected"},
		{"spot price", serverErr("Invalid.SpotPriceLimit.Exceeded"), "SpotConfigurationRejected"},
		{"throttling is recoverable", serverErr("Throttling"), ""},
		{"plain error is recoverable", errors.New("dial tcp: timeout"), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyCreateError(tc.err)
			var termErr *terminalError
			isTerminal := errors.As(got, &termErr)
			if tc.wantReason == "" {
				if isTerminal {
					t.Fatalf("expected a recoverable error, got terminal %q", termErr.reason)
				}
				return
			}
			if !isTerminal {
				t.Fatalf("expected terminal error %q, got %v", tc.wantReason, got)
			}
			if termErr.reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", termErr.reason, tc.wantReason)
			}
		})
	}
}

// ── createInstance image resolution (P3-CAPA.16: cluster bootImageID fallback) ─

func TestCreateInstance_FallsBackToClusterBootImage(t *testing.T) {
	var gotImage string
	fakeECS := &fakeclient.FakeClient{
		CreateECSInstanceFn: func(p alibabaClient.CreateInstanceParams) (*alibabaClient.CreateInstanceResponse, error) {
			gotImage = p.ImageID
			return &alibabaClient.CreateInstanceResponse{InstanceID: "i-1"}, nil
		},
	}
	r := &AlibabaCloudMachineReconciler{}
	cluster := &infrav1.AlibabaCloudCluster{Spec: infrav1.AlibabaCloudClusterSpec{Region: "cn-x", BootImageID: "m-boot"}}
	m := &infrav1.AlibabaCloudMachine{Spec: infrav1.AlibabaCloudMachineSpec{InstanceType: "ecs.g7.large"}}
	if err := r.createInstance(context.Background(), fakeECS, &clusterv1.Machine{}, cluster, m); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotImage != "m-boot" {
		t.Errorf("ImageID = %q, want m-boot (cluster bootImageID fallback)", gotImage)
	}
}

func TestCreateInstance_ExplicitImageWins(t *testing.T) {
	var gotImage string
	fakeECS := &fakeclient.FakeClient{
		CreateECSInstanceFn: func(p alibabaClient.CreateInstanceParams) (*alibabaClient.CreateInstanceResponse, error) {
			gotImage = p.ImageID
			return &alibabaClient.CreateInstanceResponse{InstanceID: "i-1"}, nil
		},
	}
	r := &AlibabaCloudMachineReconciler{}
	cluster := &infrav1.AlibabaCloudCluster{Spec: infrav1.AlibabaCloudClusterSpec{Region: "cn-x", BootImageID: "m-boot"}}
	m := &infrav1.AlibabaCloudMachine{Spec: infrav1.AlibabaCloudMachineSpec{InstanceType: "ecs.g7.large", ImageID: "m-explicit"}}
	if err := r.createInstance(context.Background(), fakeECS, &clusterv1.Machine{}, cluster, m); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotImage != "m-explicit" {
		t.Errorf("ImageID = %q, want m-explicit (machine imageID wins)", gotImage)
	}
}

func TestCreateInstance_NoBootImageIsTerminal(t *testing.T) {
	r := &AlibabaCloudMachineReconciler{}
	m := &infrav1.AlibabaCloudMachine{Spec: infrav1.AlibabaCloudMachineSpec{InstanceType: "ecs.g7.large"}}
	err := r.createInstance(context.Background(), &fakeclient.FakeClient{}, &clusterv1.Machine{}, &infrav1.AlibabaCloudCluster{}, m)
	var termErr *terminalError
	if !errors.As(err, &termErr) || termErr.reason != "NoBootImage" {
		t.Fatalf("expected NoBootImage terminal error, got %v", err)
	}
}

func TestResolveMetadataOptions(t *testing.T) {
	cases := []struct {
		name         string
		in           *infrav1.MetadataOptions
		wantEndpoint string
		wantTokens   string
		wantHop      int
	}{
		{"nil defaults to secure baseline", nil, "enabled", "required", 0},
		{"empty fields fall back to defaults", &infrav1.MetadataOptions{}, "enabled", "required", 0},
		{"explicit opt-out to tokenless", &infrav1.MetadataOptions{HttpTokens: "optional"}, "enabled", "optional", 0},
		{"explicit disable endpoint", &infrav1.MetadataOptions{HttpEndpoint: "disabled"}, "disabled", "required", 0},
		{"hop limit passthrough", &infrav1.MetadataOptions{HttpPutResponseHopLimit: 2}, "enabled", "required", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ep, tok, hop := resolveMetadataOptions(tc.in)
			if ep != tc.wantEndpoint || tok != tc.wantTokens || hop != tc.wantHop {
				t.Errorf("resolveMetadataOptions = (%q,%q,%d), want (%q,%q,%d)",
					ep, tok, hop, tc.wantEndpoint, tc.wantTokens, tc.wantHop)
			}
		})
	}
}

func TestIgnitionObjectKey(t *testing.T) {
	cluster := &infrav1.AlibabaCloudCluster{}
	cluster.Name = "demo"
	m := &infrav1.AlibabaCloudMachine{}
	m.Namespace = "ns1"
	m.Name = "worker-a"
	if got := ignitionObjectKey(cluster, m); got != "capi-ignition/demo/ns1/worker-a.ign" {
		t.Errorf("ignitionObjectKey = %q", got)
	}
}

func TestBuildIgnitionPointer(t *testing.T) {
	raw := []byte(`{"ignition":{"version":"3.4.0"},"big":"payload"}`)
	url := "https://b.oss-cn-x-internal.aliyuncs.com/k?sig=abc"
	out, err := buildIgnitionPointer(url, raw)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("pointer not valid JSON: %v", err)
	}
	repl := cfg["ignition"].(map[string]any)["config"].(map[string]any)["replace"].(map[string]any)
	if repl["source"] != url {
		t.Errorf("source = %v, want %v", repl["source"], url)
	}
	hash := repl["verification"].(map[string]any)["hash"].(string)
	if len(hash) < len("sha512-") || hash[:7] != "sha512-" {
		t.Errorf("hash = %q, want sha512- prefix", hash)
	}
}

func TestMaybeOffloadUserData(t *testing.T) {
	r := &AlibabaCloudMachineReconciler{}
	cluster := &infrav1.AlibabaCloudCluster{}
	cluster.Name = "c"
	m := &infrav1.AlibabaCloudMachine{}
	m.Namespace = "ns"
	m.Name = "w"

	small := base64.StdEncoding.EncodeToString([]byte("tiny"))
	big := base64.StdEncoding.EncodeToString(make([]byte, 20000)) // > 16384 base64

	// 1. small payload passes through untouched, no offload, no status.
	fake := &fakeclient.FakeClient{}
	got, err := r.maybeOffloadUserData(fake, cluster, m, "cn-x", small)
	if err != nil || got != small || m.Status.IgnitionOSS != nil {
		t.Fatalf("small: got=%v err=%v ossRef=%v", got == small, err, m.Status.IgnitionOSS)
	}

	// 2. oversized + no storage configured -> terminal UserDataTooLarge.
	_, err = r.maybeOffloadUserData(fake, cluster, m, "cn-x", big)
	var term *terminalError
	if !errors.As(err, &term) || term.reason != "UserDataTooLarge" {
		t.Fatalf("oversized/no-store: want terminal UserDataTooLarge, got %v", err)
	}

	// 3. oversized + storage configured -> pointer returned + status recorded.
	cluster.Spec.IgnitionStorage = &infrav1.IgnitionStorageSpec{OSSBucket: "bkt"}
	var putCalled bool
	fake.PutIgnitionObjectFn = func(p alibabaClient.IgnitionStoreParams) (string, error) {
		putCalled = true
		if p.Bucket != "bkt" || p.Key != "capi-ignition/c/ns/w.ign" {
			t.Errorf("unexpected put params: %+v", p)
		}
		return "https://bkt.oss-cn-x-internal.aliyuncs.com/" + p.Key + "?sig=x", nil
	}
	out, err := r.maybeOffloadUserData(fake, cluster, m, "cn-x", big)
	if err != nil || !putCalled {
		t.Fatalf("offload: err=%v putCalled=%v", err, putCalled)
	}
	if out == big {
		t.Fatal("offload: user-data not replaced with pointer")
	}
	if m.Status.IgnitionOSS == nil || m.Status.IgnitionOSS.Key != "capi-ignition/c/ns/w.ign" {
		t.Fatalf("offload: status.IgnitionOSS not recorded: %+v", m.Status.IgnitionOSS)
	}
	decoded, _ := base64.StdEncoding.DecodeString(out)
	if !json.Valid(decoded) {
		t.Fatal("offload: pointer is not valid JSON")
	}
}

// TestResolveFailureDomain_MultiAZ confirms a machine assigned to the third zone
// of a 3-AZ cluster resolves to that zone's vSwitch — the B2 HA spread path,
// where one MachineDeployment per AZ pins failureDomain and the controller maps
// each to its subnet.
func TestResolveFailureDomain_MultiAZ(t *testing.T) {
	r := &AlibabaCloudMachineReconciler{}
	cluster := &infrav1.AlibabaCloudCluster{
		Status: infrav1.AlibabaCloudClusterStatus{
			FailureDomains: []clusterv1.FailureDomain{
				{Name: "cn-wulanchabu-a", Attributes: map[string]string{"vSwitchID": "vsw-a"}},
				{Name: "cn-wulanchabu-b", Attributes: map[string]string{"vSwitchID": "vsw-b"}},
				{Name: "cn-wulanchabu-c", Attributes: map[string]string{"vSwitchID": "vsw-c"}},
			},
		},
	}
	for _, tc := range []struct{ zone, wantVsw string }{
		{"cn-wulanchabu-a", "vsw-a"},
		{"cn-wulanchabu-b", "vsw-b"},
		{"cn-wulanchabu-c", "vsw-c"},
	} {
		m := &clusterv1.Machine{Spec: clusterv1.MachineSpec{FailureDomain: tc.zone}}
		am := &infrav1.AlibabaCloudMachine{}
		if err := r.resolveFailureDomain(m, cluster, am); err != nil {
			t.Fatalf("%s: %v", tc.zone, err)
		}
		if am.Spec.ZoneID != tc.zone || am.Spec.VSwitchID != tc.wantVsw {
			t.Errorf("%s -> zone=%q vsw=%q, want zone=%q vsw=%q", tc.zone, am.Spec.ZoneID, am.Spec.VSwitchID, tc.zone, tc.wantVsw)
		}
	}
}
