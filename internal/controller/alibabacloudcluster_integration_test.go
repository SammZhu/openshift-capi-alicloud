//go:build integration

package controller

import (
	"fmt"
	"sync/atomic"
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

var nsCounter atomic.Int32

// freshNamespace creates and returns a unique namespace for test isolation.
func freshNamespace(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("test-%d", nsCounter.Add(1))
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := k8sClient.Create(reconcileCtx(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(reconcileCtx(), ns) })
	return name
}

func newClusterReconciler(fc *fakeclient.FakeClient) *AlibabaCloudClusterReconciler {
	return &AlibabaCloudClusterReconciler{
		Client:                    k8sClient,
		Scheme:                    testScheme,
		AlibabaCloudClientBuilder: fakeBuilder(fc),
	}
}

// makeOwnedCluster creates a CAPI Cluster and an AlibabaCloudCluster owned by it.
func makeOwnedCluster(t *testing.T, ns string, mutate func(*infrav1.AlibabaCloudCluster)) *infrav1.AlibabaCloudCluster {
	t.Helper()
	capiCluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: ns},
		// Spec must be non-zero: the v1beta2 type uses json omitzero, so an empty
		// spec serialises to null and the CRD rejects it ("spec: Required value").
		Spec: clusterv1.ClusterSpec{Paused: ptr(false)},
	}
	if err := k8sClient.Create(reconcileCtx(), capiCluster); err != nil {
		t.Fatalf("create CAPI Cluster: %v", err)
	}
	aliCluster := &infrav1.AlibabaCloudCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ali-cluster",
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clusterv1.GroupVersion.String(),
				Kind:       "Cluster",
				Name:       capiCluster.Name,
				UID:        capiCluster.UID,
			}},
		},
		Spec: infrav1.AlibabaCloudClusterSpec{
			Region: "cn-wulanchabu",
			// Our CRD enforces minProperties:1 on controlPlaneEndpoint (BYO: the
			// SLB is provisioned out-of-band), so it must be set at creation.
			ControlPlaneEndpoint: clusterv1.APIEndpoint{Host: "api.byo.example.com", Port: 6443},
		},
	}
	if mutate != nil {
		mutate(aliCluster)
	}
	if err := k8sClient.Create(reconcileCtx(), aliCluster); err != nil {
		t.Fatalf("create AlibabaCloudCluster: %v", err)
	}
	return aliCluster
}

func reconcileCluster(t *testing.T, r *AlibabaCloudClusterReconciler, obj client.Object) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(reconcileCtx(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	return res
}

func getCluster(t *testing.T, key client.ObjectKey) *infrav1.AlibabaCloudCluster {
	t.Helper()
	out := &infrav1.AlibabaCloudCluster{}
	if err := k8sClient.Get(reconcileCtx(), key, out); err != nil {
		t.Fatalf("get AlibabaCloudCluster: %v", err)
	}
	return out
}

// A BYO cluster (spec.controlPlaneEndpoint set) reconciles to Ready=True and
// carries the cluster finalizer.
func TestIntegration_Cluster_ReadyWithBYOEndpoint(t *testing.T) {
	ns := freshNamespace(t)
	ali := makeOwnedCluster(t, ns, nil)
	r := newClusterReconciler(&fakeclient.FakeClient{})

	reconcileCluster(t, r, ali)

	got := getCluster(t, client.ObjectKeyFromObject(ali))
	if !controllerutil.ContainsFinalizer(got, infrav1.ClusterFinalizer) {
		t.Errorf("expected cluster finalizer, got finalizers=%v", got.Finalizers)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, clusterv1.ReadyCondition)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Errorf("expected Ready=True, got %+v", cond)
	}
}

// The CCM preflight flags Nodes stuck with the cloud-provider uninitialized taint
// (CCM missing) as CloudControllerManagerReady=False; a clean Node keeps it True.
func TestIntegration_Cluster_CCMPreflight(t *testing.T) {
	ns := freshNamespace(t)

	stuckNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-stuck-" + ns},
		Spec: corev1.NodeSpec{Taints: []corev1.Taint{{
			Key:    "node.cloudprovider.kubernetes.io/uninitialized",
			Effect: corev1.TaintEffectNoSchedule,
		}}},
	}
	if err := k8sClient.Create(reconcileCtx(), stuckNode); err != nil {
		t.Fatalf("create stuck node: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(reconcileCtx(), stuckNode) })

	ali := makeOwnedCluster(t, ns, nil)
	r := newClusterReconciler(&fakeclient.FakeClient{})
	// CCMGracePeriod=0 → a freshly-created stuck node is flagged immediately.

	reconcileCluster(t, r, ali)

	got := getCluster(t, client.ObjectKeyFromObject(ali))
	cond := apimeta.FindStatusCondition(got.Status.Conditions, infrav1.CloudControllerManagerReadyCondition)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != infrav1.NodesAwaitingCloudProviderReason {
		t.Fatalf("expected CloudControllerManagerReady=False/NodesAwaitingCloudProvider, got %+v", cond)
	}

	// Remove the taint → next reconcile reports the CCM healthy.
	stuckNode.Spec.Taints = nil
	if err := k8sClient.Update(reconcileCtx(), stuckNode); err != nil {
		t.Fatalf("clear taint: %v", err)
	}
	reconcileCluster(t, r, ali)
	got = getCluster(t, client.ObjectKeyFromObject(ali))
	cond = apimeta.FindStatusCondition(got.Status.Conditions, infrav1.CloudControllerManagerReadyCondition)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected CloudControllerManagerReady=True after taint cleared, got %+v", cond)
	}
}

// A paused Cluster short-circuits reconciliation: no finalizer is added.
// (The ControlPlaneEndpointMissing branch is unreachable via the API — the CRD's
//
//	minProperties:1 on controlPlaneEndpoint enforces BYO endpoint at creation — so
//	it is covered by the unit tests, not here.)
func TestIntegration_Cluster_PausedSkips(t *testing.T) {
	ns := freshNamespace(t)
	ali := makeOwnedCluster(t, ns, func(c *infrav1.AlibabaCloudCluster) {
		c.Annotations = map[string]string{clusterv1.PausedAnnotation: "true"}
	})
	r := newClusterReconciler(&fakeclient.FakeClient{})

	reconcileCluster(t, r, ali)

	got := getCluster(t, client.ObjectKeyFromObject(ali))
	if controllerutil.ContainsFinalizer(got, infrav1.ClusterFinalizer) {
		t.Errorf("paused cluster should not get a finalizer, got %v", got.Finalizers)
	}
}
