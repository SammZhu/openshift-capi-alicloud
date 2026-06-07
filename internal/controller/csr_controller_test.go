/*
Copyright 2024 The Kubernetes Authors.

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
	"crypto/x509"
	"net"
	"testing"

	certv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/SammZhu/openshift-capi-alicloud/api/v1beta1"
)

func node(name, providerID string, addrs ...corev1.NodeAddress) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{ProviderID: providerID},
		Status:     corev1.NodeStatus{Addresses: addrs},
	}
}

func TestNodeBackedByCAPAMachine(t *testing.T) {
	machines := []infrav1.AlibabaCloudMachine{
		{Spec: infrav1.AlibabaCloudMachineSpec{ProviderID: ptr("alicloud://cn-x/i-1")}},
	}
	if !nodeBackedByCAPAMachine(node("n", "alicloud://cn-x/i-1"), machines) {
		t.Error("expected match on providerID")
	}
	if nodeBackedByCAPAMachine(node("n", "alicloud://cn-x/i-OTHER"), machines) {
		t.Error("should not match a different providerID")
	}
	if nodeBackedByCAPAMachine(node("n", ""), machines) {
		t.Error("empty node providerID must not match")
	}
}

func TestSansSubsetOfNode(t *testing.T) {
	n := node("iz-worker", "alicloud://cn-x/i-1",
		corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.0.5"},
		corev1.NodeAddress{Type: corev1.NodeHostName, Address: "iz-worker"},
	)
	tests := []struct {
		name string
		cr   *x509.CertificateRequest
		want bool
	}{
		{"ip+hostname in node", &x509.CertificateRequest{IPAddresses: []net.IP{net.ParseIP("10.0.0.5")}, DNSNames: []string{"iz-worker"}}, true},
		{"ip only", &x509.CertificateRequest{IPAddresses: []net.IP{net.ParseIP("10.0.0.5")}}, true},
		{"foreign ip", &x509.CertificateRequest{IPAddresses: []net.IP{net.ParseIP("1.2.3.4")}}, false},
		{"foreign dns", &x509.CertificateRequest{DNSNames: []string{"evil.example.com"}}, false},
		{"no sans", &x509.CertificateRequest{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sansSubsetOfNode(tc.cr, n); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestIsCSRDecided(t *testing.T) {
	pending := &certv1.CertificateSigningRequest{}
	if isCSRDecided(pending) {
		t.Error("pending CSR should not be decided")
	}
	approved := &certv1.CertificateSigningRequest{Status: certv1.CertificateSigningRequestStatus{
		Conditions: []certv1.CertificateSigningRequestCondition{{Type: certv1.CertificateApproved}},
	}}
	if !isCSRDecided(approved) {
		t.Error("approved CSR should be decided")
	}
}

func csrScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := infrav1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHasPendingMachine(t *testing.T) {
	s := csrScheme(t)
	// One machine provisioned (providerID + instanceID), no node carries that
	// providerID yet → pending.
	m := &infrav1.AlibabaCloudMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "w1", Namespace: "default"},
		Spec:       infrav1.AlibabaCloudMachineSpec{ProviderID: ptr("alicloud://cn-x/i-1")},
		Status:     infrav1.AlibabaCloudMachineStatus{InstanceID: ptr("i-1")},
	}

	// No matching node → pending.
	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(node("master", "alicloud://cn-x/i-master")).Build()
	r := &CertificateSigningRequestReconciler{Client: c}
	pending, err := r.hasPendingMachine(context.Background(), []infrav1.AlibabaCloudMachine{*m})
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Error("expected pending: machine has no node yet")
	}

	// The node for the machine has joined → not pending.
	c2 := fake.NewClientBuilder().WithScheme(s).
		WithObjects(node("w1node", "alicloud://cn-x/i-1")).Build()
	r2 := &CertificateSigningRequestReconciler{Client: c2}
	pending2, err := r2.hasPendingMachine(context.Background(), []infrav1.AlibabaCloudMachine{*m})
	if err != nil {
		t.Fatal(err)
	}
	if pending2 {
		t.Error("expected not pending: the machine's node has joined")
	}
}
