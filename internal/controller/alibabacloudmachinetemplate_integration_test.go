//go:build integration

package controller

import (
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/SammZhu/openshift-capi-alicloud/api/v1beta1"
	fakeclient "github.com/SammZhu/openshift-capi-alicloud/pkg/client/fake"
)

// TestIntegration_MachineTemplate_CapacityFromInstanceType verifies the
// scale-from-zero capacity is resolved from the instanceType and persisted to
// status.capacity (cluster-autoscaler reads it), and that the per-instanceType
// DescribeInstanceTypes result is cached across templates.
func TestIntegration_MachineTemplate_CapacityFromInstanceType(t *testing.T) {
	ns := freshNamespace(t)

	var calls int32
	fc := &fakeclient.FakeClient{
		InstanceTypeCapacityFn: func(it string) (int64, int64, error) {
			atomic.AddInt32(&calls, 1)
			return 4, 16384, nil // ecs.g7.xlarge: 4 vCPU, 16 GiB
		},
	}
	r := &AlibabaCloudMachineTemplateReconciler{
		Client:                    k8sClient,
		Scheme:                    testScheme,
		AlibabaCloudClientBuilder: fakeBuilder(fc),
	}

	mk := func(name string) *infrav1.AlibabaCloudMachineTemplate {
		tmpl := &infrav1.AlibabaCloudMachineTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: infrav1.AlibabaCloudMachineTemplateSpec{
				Template: infrav1.AlibabaCloudMachineTemplateResource{
					Spec: infrav1.AlibabaCloudMachineSpec{
						InstanceType: "ecs.g7.xlarge",
						RegionID:     "cn-wulanchabu",
						SystemDisk:   &infrav1.SystemDisk{Category: "cloud_essd", Size: 40},
					},
				},
			},
		}
		if err := k8sClient.Create(reconcileCtx(), tmpl); err != nil {
			t.Fatalf("create template %s: %v", name, err)
		}
		return tmpl
	}

	t1 := mk("tmpl-a")
	if _, err := r.Reconcile(reconcileCtx(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(t1)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := &infrav1.AlibabaCloudMachineTemplate{}
	if err := k8sClient.Get(reconcileCtx(), client.ObjectKeyFromObject(t1), got); err != nil {
		t.Fatalf("get: %v", err)
	}
	capList := got.Status.Capacity
	if capList == nil {
		t.Fatal("status.capacity is nil — capacity was not resolved/persisted")
	}
	if c := capList[corev1.ResourceCPU]; c.Value() != 4 {
		t.Errorf("cpu = %s, want 4", c.String())
	}
	if m := capList[corev1.ResourceMemory]; m.Value() != 16384*1024*1024 {
		t.Errorf("memory = %d bytes, want %d (16Gi)", m.Value(), int64(16384)*1024*1024)
	}
	if p := capList[corev1.ResourcePods]; p.Value() != defaultMaxPodsPerNode {
		t.Errorf("pods = %s, want %d", p.String(), defaultMaxPodsPerNode)
	}
	if e := capList[corev1.ResourceEphemeralStorage]; e.Value() != 40*1024*1024*1024 {
		t.Errorf("ephemeral-storage = %s, want 40Gi", e.String())
	}

	// Idempotent: a second reconcile with status already set must not re-patch.
	if _, err := r.Reconcile(reconcileCtx(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(t1)}); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	// Cache: a second template with the SAME instanceType must reuse the cached
	// spec (no extra DescribeInstanceTypes call).
	t2 := mk("tmpl-b")
	if _, err := r.Reconcile(reconcileCtx(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(t2)}); err != nil {
		t.Fatalf("reconcile t2: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("InstanceTypeCapacity called %d times, want 1 (cached per instanceType)", n)
	}
}
