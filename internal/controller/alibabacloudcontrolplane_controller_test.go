/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	cpv1 "github.com/SammZhu/openshift-capi-alicloud/api/controlplane/v1beta1"
)

func cpScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme, clusterv1.AddToScheme, cpv1.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// External mode adopts a pre-existing control plane: it must report
// externalManagedControlPlane=true + initialization.controlPlaneInitialized=true
// (so CAPI core marks the Cluster ControlPlaneInitialized and worker node-health
// is unblocked) and mirror the version — without provisioning anything.
func TestControlPlane_ExternalAdopt(t *testing.T) {
	s := cpScheme(t)
	cp := &cpv1.AlibabaCloudControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "caworkers", Namespace: "default"},
		Spec:       cpv1.AlibabaCloudControlPlaneSpec{Mode: cpv1.ControlPlaneModeExternal, Version: "v1.33.0"},
	}
	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(cp).
		WithStatusSubresource(&cpv1.AlibabaCloudControlPlane{}).
		Build()
	r := &AlibabaCloudControlPlaneReconciler{Client: c, Scheme: s}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cp)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := &cpv1.AlibabaCloudControlPlane{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(cp), got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.ExternalManagedControlPlane == nil || !*got.Status.ExternalManagedControlPlane {
		t.Errorf("want externalManagedControlPlane=true, got %v", got.Status.ExternalManagedControlPlane)
	}
	if got.Status.Initialization == nil || got.Status.Initialization.ControlPlaneInitialized == nil ||
		!*got.Status.Initialization.ControlPlaneInitialized {
		t.Errorf("want initialization.controlPlaneInitialized=true, got %+v", got.Status.Initialization)
	}
	if !got.Status.Ready {
		t.Errorf("want status.ready=true")
	}
	if got.Status.Version != "v1.33.0" {
		t.Errorf("want status.version mirrored to v1.33.0, got %q", got.Status.Version)
	}
}

// An empty mode defaults to external (the CRD default), so it must behave the same.
func TestControlPlane_EmptyModeDefaultsExternal(t *testing.T) {
	s := cpScheme(t)
	cp := &cpv1.AlibabaCloudControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "caworkers", Namespace: "default"},
		Spec:       cpv1.AlibabaCloudControlPlaneSpec{Version: "v1.33.0"},
	}
	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(cp).
		WithStatusSubresource(&cpv1.AlibabaCloudControlPlane{}).
		Build()
	r := &AlibabaCloudControlPlaneReconciler{Client: c, Scheme: s}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cp)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &cpv1.AlibabaCloudControlPlane{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(cp), got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Initialization == nil || got.Status.Initialization.ControlPlaneInitialized == nil ||
		!*got.Status.Initialization.ControlPlaneInitialized {
		t.Errorf("empty mode should default to external + initialized, got %+v", got.Status.Initialization)
	}
}
