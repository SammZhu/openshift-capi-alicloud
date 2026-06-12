//go:build integration

package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	infrav1 "github.com/SammZhu/openshift-capi-alicloud/api/v1beta1"
	alibabaClient "github.com/SammZhu/openshift-capi-alicloud/pkg/client"
	fakeclient "github.com/SammZhu/openshift-capi-alicloud/pkg/client/fake"
)

func newMachineReconciler(fc *fakeclient.FakeClient) *AlibabaCloudMachineReconciler {
	return &AlibabaCloudMachineReconciler{
		Client:                    k8sClient,
		Scheme:                    testScheme,
		AlibabaCloudClientBuilder: fakeBuilder(fc),
	}
}

type machineOpts struct {
	clusterReady        bool
	withBootstrap       bool
	paused              bool
	httpTokensAfterBoot string
}

// makeMachineFixture wires the full owner graph the machine reconciler needs:
// CAPI Cluster (infraRef → AlibabaCloudCluster) + AlibabaCloudCluster (+ status
// ready) + CAPI Machine (cluster-name label, bootstrap) + AlibabaCloudMachine
// owned by the Machine. Returns the AlibabaCloudMachine to reconcile.
func makeMachineFixture(t *testing.T, ns string, o machineOpts) *infrav1.AlibabaCloudMachine {
	t.Helper()

	capiCluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: ns},
		Spec: clusterv1.ClusterSpec{
			Paused: ptr(false),
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: infrav1.GroupVersion.Group,
				Kind:     "AlibabaCloudCluster",
				Name:     "ali-cluster",
			},
		},
	}
	if err := k8sClient.Create(reconcileCtx(), capiCluster); err != nil {
		t.Fatalf("create CAPI Cluster: %v", err)
	}

	aliCluster := &infrav1.AlibabaCloudCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ali-cluster", Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clusterv1.GroupVersion.String(), Kind: "Cluster",
				Name: capiCluster.Name, UID: capiCluster.UID,
			}},
		},
		Spec: infrav1.AlibabaCloudClusterSpec{
			Region:               "cn-wulanchabu",
			BootImageID:          "m-bootimg",
			ControlPlaneEndpoint: clusterv1.APIEndpoint{Host: "api.byo.example.com", Port: 6443},
		},
	}
	if err := k8sClient.Create(reconcileCtx(), aliCluster); err != nil {
		t.Fatalf("create AlibabaCloudCluster: %v", err)
	}
	if o.clusterReady {
		aliCluster.Status.Ready = true
		// status.controlPlaneEndpoint also carries minProperties:1, so mirror the
		// spec endpoint into status (as the controller does) before updating.
		aliCluster.Status.ControlPlaneEndpoint = aliCluster.Spec.ControlPlaneEndpoint
		if err := k8sClient.Status().Update(reconcileCtx(), aliCluster); err != nil {
			t.Fatalf("set cluster status ready: %v", err)
		}
	}

	// The Machine CRD requires spec.bootstrap to be present. Two valid states:
	//  - ready:   DataSecretName populated (bootstrap secret exists)
	//  - waiting: a ConfigRef exists but its secret isn't generated yet (nil
	//             DataSecretName) — the realistic "WaitingForBootstrapData" state.
	bootstrap := clusterv1.Bootstrap{}
	if o.withBootstrap {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "bootstrap", Namespace: ns},
			Data:       map[string][]byte{"value": []byte("e30=")},
		}
		if err := k8sClient.Create(reconcileCtx(), secret); err != nil {
			t.Fatalf("create bootstrap secret: %v", err)
		}
		bootstrap.DataSecretName = ptr("bootstrap")
	} else {
		bootstrap.ConfigRef = clusterv1.ContractVersionedObjectReference{
			APIGroup: "bootstrap.cluster.x-k8s.io", Kind: "KubeadmConfig", Name: "boot-config",
		}
	}

	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "machine", Namespace: ns,
			Labels: map[string]string{clusterv1.ClusterNameLabel: capiCluster.Name},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: capiCluster.Name,
			Bootstrap:   bootstrap,
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: infrav1.GroupVersion.Group, Kind: "AlibabaCloudMachine", Name: "ali-machine",
			},
		},
	}
	if err := k8sClient.Create(reconcileCtx(), machine); err != nil {
		t.Fatalf("create Machine: %v", err)
	}

	aliMachine := &infrav1.AlibabaCloudMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ali-machine", Namespace: ns,
			Labels: map[string]string{clusterv1.ClusterNameLabel: capiCluster.Name},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clusterv1.GroupVersion.String(), Kind: "Machine",
				Name: machine.Name, UID: machine.UID,
			}},
		},
		Spec: infrav1.AlibabaCloudMachineSpec{InstanceType: "ecs.g7.large"},
	}
	if o.httpTokensAfterBoot != "" {
		aliMachine.Spec.MetadataOptions = &infrav1.MetadataOptions{
			HttpEndpoint: "enabled", HttpTokens: "optional", HttpTokensAfterBoot: o.httpTokensAfterBoot,
		}
	}
	if o.paused {
		aliMachine.Annotations = map[string]string{clusterv1.PausedAnnotation: "true"}
	}
	if err := k8sClient.Create(reconcileCtx(), aliMachine); err != nil {
		t.Fatalf("create AlibabaCloudMachine: %v", err)
	}
	return aliMachine
}

func reconcileMachine(t *testing.T, r *AlibabaCloudMachineReconciler, obj client.Object) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(reconcileCtx(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	return res
}

func getMachine(t *testing.T, key client.ObjectKey) *infrav1.AlibabaCloudMachine {
	t.Helper()
	out := &infrav1.AlibabaCloudMachine{}
	if err := k8sClient.Get(reconcileCtx(), key, out); err != nil {
		t.Fatalf("get AlibabaCloudMachine: %v", err)
	}
	return out
}

func readyCond(m *infrav1.AlibabaCloudMachine) *metav1.Condition {
	return apimeta.FindStatusCondition(m.Status.Conditions, clusterv1.ReadyCondition)
}

// When the infra cluster is not yet Ready, the machine gets a finalizer but stays
// Ready=False (ClusterInfrastructureNotReady) and requeues — no cloud call.
func TestIntegration_Machine_ClusterInfraNotReady(t *testing.T) {
	ns := freshNamespace(t)
	ali := makeMachineFixture(t, ns, machineOpts{clusterReady: false, withBootstrap: true})
	r := newMachineReconciler(&fakeclient.FakeClient{})

	res := reconcileMachine(t, r, ali)
	if res.RequeueAfter == 0 {
		t.Errorf("expected requeue while cluster infra not ready, got %+v", res)
	}
	got := getMachine(t, client.ObjectKeyFromObject(ali))
	if !controllerutil.ContainsFinalizer(got, infrav1.MachineFinalizer) {
		t.Errorf("expected machine finalizer, got %v", got.Finalizers)
	}
	if c := readyCond(got); c == nil || c.Status != metav1.ConditionFalse || c.Reason != "ClusterInfrastructureNotReady" {
		t.Errorf("expected Ready=False/ClusterInfrastructureNotReady, got %+v", c)
	}
}

// With the cluster Ready but no bootstrap data, the machine waits
// (WaitingForBootstrapData) and requeues rather than booting an ECS.
func TestIntegration_Machine_WaitingForBootstrap(t *testing.T) {
	ns := freshNamespace(t)
	ali := makeMachineFixture(t, ns, machineOpts{clusterReady: true, withBootstrap: false})
	r := newMachineReconciler(&fakeclient.FakeClient{})

	res := reconcileMachine(t, r, ali)
	if res.RequeueAfter == 0 {
		t.Errorf("expected requeue while waiting for bootstrap, got %+v", res)
	}
	got := getMachine(t, client.ObjectKeyFromObject(ali))
	if c := readyCond(got); c == nil || c.Status != metav1.ConditionFalse || c.Reason != "WaitingForBootstrapData" {
		t.Errorf("expected Ready=False/WaitingForBootstrapData, got %+v", c)
	}
}

// A paused AlibabaCloudMachine short-circuits before reconcileNormal: no finalizer.
func TestIntegration_Machine_PausedSkips(t *testing.T) {
	ns := freshNamespace(t)
	ali := makeMachineFixture(t, ns, machineOpts{clusterReady: true, withBootstrap: true, paused: true})
	r := newMachineReconciler(&fakeclient.FakeClient{})

	reconcileMachine(t, r, ali)

	got := getMachine(t, client.ObjectKeyFromObject(ali))
	if controllerutil.ContainsFinalizer(got, infrav1.MachineFinalizer) {
		t.Errorf("paused machine should not get a finalizer, got %v", got.Finalizers)
	}
}

// setMachineNodeRef gives the owning CAPI Machine a nodeRef — the signal that the
// node has joined (Ignition done), which the IMDS post-boot hardening waits for.
func setMachineNodeRef(t *testing.T, ns, node string) {
	t.Helper()
	m := &clusterv1.Machine{}
	if err := k8sClient.Get(reconcileCtx(), client.ObjectKey{Namespace: ns, Name: "machine"}, m); err != nil {
		t.Fatalf("get Machine: %v", err)
	}
	m.Status.NodeRef.Name = node
	if err := k8sClient.Status().Update(reconcileCtx(), m); err != nil {
		t.Fatalf("set Machine nodeRef: %v", err)
	}
}

// End-to-end machine lifecycle against a real apiserver with a fake cloud:
// create → ECS provisioned (dot-form providerID) → Running + node joins → Ready +
// provisioned + IMDS hardened to IMDSv2 (G14) → delete frees the ECS before the
// finalizer drops, leaving no orphan (G8). This locks in the chain we verified
// live, and (via the envtest CRD) that the new status fields persist — they are
// not pruned. Mirrors scripts/verify-capi-pools.sh expectations at the unit of one
// Machine.
func TestIntegration_Machine_Lifecycle(t *testing.T) {
	ns := freshNamespace(t)
	ali := makeMachineFixture(t, ns, machineOpts{clusterReady: true, withBootstrap: true, httpTokensAfterBoot: "required"})

	phase := "Pending"
	var created, deleted, hardenedTokens string
	fc := &fakeclient.FakeClient{
		CreateECSInstanceFn: func(alibabaClient.CreateInstanceParams) (*alibabaClient.CreateInstanceResponse, error) {
			created = "i-e2e"
			return &alibabaClient.CreateInstanceResponse{InstanceID: "i-e2e"}, nil
		},
		DescribeInstanceByIDFn: func(id string) (*alibabaClient.InstanceDescription, error) {
			if phase == "" {
				return nil, nil // ECS gone
			}
			return &alibabaClient.InstanceDescription{InstanceID: id, Status: phase}, nil
		},
		ModifyInstanceMetadataFn: func(_, _, tokens string, _ int) error { hardenedTokens = tokens; return nil },
		DeleteECSInstanceFn:      func(id string, _ bool) error { deleted = id; return nil },
	}
	r := newMachineReconciler(fc)

	// 1) Create: provisions an ECS, records InstanceID + dot-form providerID, requeues (Pending).
	reconcileMachine(t, r, ali)
	got := getMachine(t, client.ObjectKeyFromObject(ali))
	if created != "i-e2e" {
		t.Fatalf("expected ECS create, none happened")
	}
	if got.Status.InstanceID == nil || *got.Status.InstanceID != "i-e2e" {
		t.Fatalf("Status.InstanceID = %v, want i-e2e", got.Status.InstanceID)
	}
	if got.Spec.ProviderID == nil || *got.Spec.ProviderID != "alicloud://cn-wulanchabu.i-e2e" {
		t.Fatalf("providerID = %v, want alicloud://cn-wulanchabu.i-e2e (CCM dot form, #78)", got.Spec.ProviderID)
	}
	if got.Status.Ready {
		t.Errorf("must not be Ready while the ECS is Pending")
	}
	if hardenedTokens != "" {
		t.Errorf("IMDS must not be hardened before the node joins (G14 timing)")
	}

	// 2) Node joins: ECS Running + the owning Machine gets a nodeRef → Ready,
	//    provisioned, and IMDS hardened to IMDSv2.
	phase = "Running"
	setMachineNodeRef(t, ns, "node-e2e")
	reconcileMachine(t, r, ali)
	got = getMachine(t, client.ObjectKeyFromObject(ali))
	if !got.Status.Ready {
		t.Errorf("expected Ready once the ECS is Running")
	}
	if got.Status.Initialization == nil || got.Status.Initialization.Provisioned == nil || !*got.Status.Initialization.Provisioned {
		t.Errorf("expected v1beta2 initialization.provisioned=true")
	}
	if hardenedTokens != "required" {
		t.Errorf("expected IMDS hardened to required after join, got %q", hardenedTokens)
	}
	if got.Status.MetadataHardened == nil || !*got.Status.MetadataHardened {
		t.Errorf("expected Status.MetadataHardened=true (and the CRD must persist it, not prune)")
	}

	// 3) Delete: the ECS is freed before the finalizer drops; once gone, the object
	//    is removed — no orphan ECS.
	if err := k8sClient.Delete(reconcileCtx(), got); err != nil {
		t.Fatalf("delete AlibabaCloudMachine: %v", err)
	}
	reconcileMachine(t, r, ali) // describes Running → issues delete → requeue
	if deleted != "i-e2e" {
		t.Fatalf("expected ECS delete on the delete path, got %q", deleted)
	}
	phase = ""                  // ECS now released
	reconcileMachine(t, r, ali) // gone → remove finalizer
	if err := k8sClient.Get(reconcileCtx(), client.ObjectKeyFromObject(ali), &infrav1.AlibabaCloudMachine{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected the AlibabaCloudMachine gone after finalizer removal, get err = %v", err)
	}
}

// TestIntegration_Machine_WatchHardensWhenNodeRefBindsLater is the regression for
// the live-found G14 bug (2026-06-12): IMDS hardening is gated on the owning
// Machine's nodeRef, which CAPI core binds AFTER the node joins. The controller
// reconciled the AlibabaCloudMachine only during provisioning (nodeRef still
// empty → "deferring") and the Running success path returns ctrl.Result{} with NO
// requeue — so without a watch on the Machine, the later nodeRef bind never
// re-enqueued it and hardening never happened (until the ~10h resync).
//
// The other integration tests can't catch this: they set nodeRef and call
// Reconcile in the same breath, exercising the harden LOGIC but not the
// re-enqueue. This one runs a REAL manager (so SetupWithManager's Machine watch is
// live) and asserts hardening fires purely from binding nodeRef. Remove the
// Watches(&Machine{}) and this test times out.
func TestIntegration_Machine_WatchHardensWhenNodeRefBindsLater(t *testing.T) {
	ns := freshNamespace(t)
	ali := makeMachineFixture(t, ns, machineOpts{clusterReady: true, withBootstrap: true, httpTokensAfterBoot: "required"})

	var mu sync.Mutex
	hardened := "" // set by the fake when the controller flips IMDS
	fc := &fakeclient.FakeClient{
		CreateECSInstanceFn: func(alibabaClient.CreateInstanceParams) (*alibabaClient.CreateInstanceResponse, error) {
			return &alibabaClient.CreateInstanceResponse{InstanceID: "i-watch"}, nil
		},
		DescribeInstanceByIDFn: func(id string) (*alibabaClient.InstanceDescription, error) {
			return &alibabaClient.InstanceDescription{InstanceID: id, Status: "Running"}, nil
		},
		ModifyInstanceMetadataFn: func(_, _, tokens string, _ int) error {
			mu.Lock()
			hardened = tokens
			mu.Unlock()
			return nil
		},
	}
	getHardened := func() string { mu.Lock(); defer mu.Unlock(); return hardened }

	// Long resync so a periodic cache re-list cannot re-enqueue the machine — the
	// ONLY re-trigger after provisioning must be the Machine watch, or the test is
	// not actually guarding it.
	syncPeriod := time.Hour
	mgr, err := ctrl.NewManager(testEnv.Config, ctrl.Options{
		Scheme:  testScheme,
		Metrics: metricsserver.Options{BindAddress: "0"}, // disable; avoid port clashes
		Cache:   cache.Options{SyncPeriod: &syncPeriod},
	})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	r := &AlibabaCloudMachineReconciler{
		Client:                    mgr.GetClient(),
		Scheme:                    testScheme,
		Log:                       ctrl.Log.WithName("test-machine-watch"),
		AlibabaCloudClientBuilder: fakeBuilder(fc),
	}
	if err := r.SetupWithManager(context.Background(), mgr, controller.Options{}); err != nil {
		t.Fatalf("SetupWithManager: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = mgr.Start(ctx) }()

	key := client.ObjectKeyFromObject(ali)

	// The controller provisions and settles in the DEFERRED state: InstanceID set,
	// Ready, but NOT hardened (nodeRef is still empty).
	requireEventually(t, 25*time.Second, func() bool {
		m := &infrav1.AlibabaCloudMachine{}
		return k8sClient.Get(context.Background(), key, m) == nil && m.Status.InstanceID != nil
	}, "AlibabaCloudMachine never provisioned (got an InstanceID)")
	if h := getHardened(); h != "" {
		t.Fatalf("IMDS hardened before nodeRef bound (must defer, G14 timing): %q", h)
	}

	// Bind nodeRef on the owning Machine. With the Machine watch this re-enqueues
	// the AlibabaCloudMachine within ~a second and it hardens. Without the watch the
	// only re-trigger is a periodic ~30s cache relist (and in production the
	// reconcile settles entirely → hardening never happens) — so a tight window
	// both reflects the real requirement (IMDS must harden PROMPTLY after join) and
	// makes removing Watches(&Machine{}) fail this test.
	setMachineNodeRef(t, ns, "node-watch")

	requireEventually(t, 12*time.Second, func() bool {
		m := &infrav1.AlibabaCloudMachine{}
		if k8sClient.Get(context.Background(), key, m) != nil {
			return false
		}
		return getHardened() == "required" && m.Status.MetadataHardened != nil && *m.Status.MetadataHardened
	}, "IMDS not hardened promptly after nodeRef bound — the Machine watch did not re-enqueue (G14 regression)")
}

// requireEventually polls cond until it returns true or the timeout elapses.
func requireEventually(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out after %s: %s", timeout, msg)
}
