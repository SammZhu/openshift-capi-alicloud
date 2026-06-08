//go:build integration

package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	infrav1 "github.com/SammZhu/openshift-capi-alicloud/api/v1beta1"
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
	clusterReady  bool
	withBootstrap bool
	paused        bool
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
