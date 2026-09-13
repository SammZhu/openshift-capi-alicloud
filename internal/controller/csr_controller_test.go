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
	"crypto/x509/pkix"
	"net"
	"strings"
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

func TestProviderInstanceID(t *testing.T) {
	cases := map[string]string{
		"alicloud://cn-x/i-1": "i-1", // CAPA slash form
		"alicloud://cn-x.i-1": "i-1", // CCM dot form
		"i-1":                 "i-1",
		"":                    "",
	}
	for in, want := range cases {
		if got := providerInstanceID(in); got != want {
			t.Errorf("providerInstanceID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNodeBackedByCAPAMachine(t *testing.T) {
	// CAPA writes the machine providerID slash-separated; the Alibaba CCM writes
	// the node providerID dot-separated. They must still match by instance id.
	machines := []infrav1.AlibabaCloudMachine{
		{Spec: infrav1.AlibabaCloudMachineSpec{ProviderID: ptr("alicloud://cn-x/i-1")}},
	}
	if !nodeBackedByCAPAMachine(node("n", "alicloud://cn-x.i-1"), machines) {
		t.Error("expected match across CCM dot form vs CAPA slash form")
	}
	if nodeBackedByCAPAMachine(node("n", "alicloud://cn-x.i-OTHER"), machines) {
		t.Error("should not match a different instance id")
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

// A kubelet renews its serving certificate periodically, and the renewal only
// takes effect once the CSR is approved. These cases pin down who may renew:
// membership and SAN containment decide it, not whether this provider created
// the node — the rule that used to leave ABI-installed nodes without any
// approver at all.
func TestShouldApproveServingCSR(t *testing.T) {
	s := csrScheme(t)
	capaMachine := infrav1.AlibabaCloudMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "w1", Namespace: "default"},
		Spec:       infrav1.AlibabaCloudMachineSpec{ProviderID: ptr("alicloud://cn-x/i-capa")},
		Status:     infrav1.AlibabaCloudMachineStatus{InstanceID: ptr("i-capa")},
	}

	cases := []struct {
		name       string
		node       *corev1.Node
		csrUser    string
		csrNode    string
		sanIPs     []net.IP
		sanDNS     []string
		wantOK     bool
		wantReason string
	}{
		{
			name:       "ABI node with no CAPA machine may renew",
			node:       node("cluster1-master-2", "", corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.32.6"}),
			csrUser:    "system:node:cluster1-master-2",
			csrNode:    "cluster1-master-2",
			sanIPs:     []net.IP{net.ParseIP("10.0.32.6")},
			wantOK:     true,
			wantReason: "no AlibabaCloudMachine",
		},
		{
			name:       "CAPA-backed node still approved, and still says so",
			node:       node("capa-w1", "alicloud://cn-x.i-capa", corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.62.84"}),
			csrUser:    "system:node:capa-w1",
			csrNode:    "capa-w1",
			sanIPs:     []net.IP{net.ParseIP("10.0.62.84")},
			wantOK:     true,
			wantReason: "CAPA-backed",
		},
		{
			name:    "a SAN the node does not own is refused",
			node:    node("cluster1-master-2", "", corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.32.6"}),
			csrUser: "system:node:cluster1-master-2",
			csrNode: "cluster1-master-2",
			sanIPs:  []net.IP{net.ParseIP("10.0.32.6"), net.ParseIP("10.0.16.5")},
			wantOK:  false,
		},
		{
			name:    "one node may not request a certificate for another",
			node:    node("cluster1-master-2", "", corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.32.6"}),
			csrUser: "system:node:cluster1-worker-1",
			csrNode: "cluster1-master-2",
			sanIPs:  []net.IP{net.ParseIP("10.0.32.6")},
			wantOK:  false,
		},
		{
			name:    "a node that is not a member is refused",
			node:    node("some-other-node", "", corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.32.6"}),
			csrUser: "system:node:not-a-member",
			csrNode: "not-a-member",
			sanIPs:  []net.IP{net.ParseIP("10.0.32.6")},
			wantOK:  false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl := fake.NewClientBuilder().WithScheme(s).
				WithObjects(c.node).WithLists(&infrav1.AlibabaCloudMachineList{Items: []infrav1.AlibabaCloudMachine{capaMachine}}).Build()
			r := &CertificateSigningRequestReconciler{Client: cl}
			csr := &certv1.CertificateSigningRequest{
				Spec: certv1.CertificateSigningRequestSpec{
					SignerName: certv1.KubeletServingSignerName,
					Username:   c.csrUser,
				},
			}
			x509cr := &x509.CertificateRequest{
				Subject:     pkixName("system:node:"+c.csrNode, "system:nodes"),
				IPAddresses: c.sanIPs,
				DNSNames:    c.sanDNS,
			}
			ok, reason, err := r.shouldApprove(context.Background(), csr, x509cr)
			if err != nil {
				t.Fatal(err)
			}
			if ok != c.wantOK {
				t.Fatalf("approve = %v (%s), want %v", ok, reason, c.wantOK)
			}
			if c.wantOK && !strings.Contains(reason, c.wantReason) {
				t.Fatalf("reason = %q, want it to mention %q", reason, c.wantReason)
			}
		})
	}
}

func pkixName(cn, org string) pkix.Name {
	return pkix.Name{CommonName: cn, Organization: []string{org}}
}
