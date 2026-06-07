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
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	certv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/SammZhu/openshift-capi-alicloud/api/v1beta1"
)

const (
	nodeBootstrapperUser = "system:serviceaccount:openshift-machine-config-operator:node-bootstrapper"
	nodeUserPrefix       = "system:node:"
	nodesGroup           = "system:nodes"
)

// CertificateSigningRequestReconciler auto-approves the kubelet client
// (bootstrap) and serving CSRs for worker nodes provisioned by an
// AlibabaCloudMachine.
//
// Route B nodes have no machine-api Machine, so OpenShift's built-in
// cluster-machine-approver never approves their CSRs and they would hang at
// NotReady until someone runs `oc adm certificate approve`. This reconciler is
// the CAPA-bound equivalent: it only approves a CSR when it can tie the node
// back to a CAPA machine, mirroring the upstream approver's safety model.
//
// +kubebuilder:rbac:groups=certificates.k8s.io,resources=certificatesigningrequests,verbs=get;list;watch
// +kubebuilder:rbac:groups=certificates.k8s.io,resources=certificatesigningrequests/approval,verbs=update
// +kubebuilder:rbac:groups=certificates.k8s.io,resources=signers,resourceNames=kubernetes.io/kube-apiserver-client-kubelet;kubernetes.io/kubelet-serving,verbs=approve
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
type CertificateSigningRequestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

// SetupWithManager wires the CSR reconciler.
func (r *CertificateSigningRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&certv1.CertificateSigningRequest{}).
		Complete(r)
}

func (r *CertificateSigningRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("csr", req.Name)

	csr := &certv1.CertificateSigningRequest{}
	if err := r.Get(ctx, req.NamespacedName, csr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Already decided, or already issued — nothing to do.
	if isCSRDecided(csr) || len(csr.Status.Certificate) > 0 {
		return ctrl.Result{}, nil
	}
	// Only the two kubelet signers are in scope; everything else is left for the
	// platform's own approvers.
	if csr.Spec.SignerName != certv1.KubeAPIServerClientKubeletSignerName &&
		csr.Spec.SignerName != certv1.KubeletServingSignerName {
		return ctrl.Result{}, nil
	}

	x509cr, err := parseCSR(csr.Spec.Request)
	if err != nil {
		log.Error(err, "could not parse CSR; leaving for manual review")
		return ctrl.Result{}, nil
	}

	ok, reason, err := r.shouldApprove(ctx, csr, x509cr)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ok {
		log.Info("CSR not auto-approved — no AlibabaCloudMachine backs it; leaving for manual review",
			"signer", csr.Spec.SignerName)
		return ctrl.Result{}, nil
	}

	csr.Status.Conditions = append(csr.Status.Conditions, certv1.CertificateSigningRequestCondition{
		Type:    certv1.CertificateApproved,
		Status:  corev1.ConditionTrue,
		Reason:  "CAPAApprove",
		Message: reason,
	})
	if err := r.SubResource("approval").Update(ctx, csr); err != nil {
		return ctrl.Result{}, fmt.Errorf("approve CSR %s: %w", csr.Name, err)
	}
	log.Info("approved CSR", "signer", csr.Spec.SignerName, "reason", reason)
	return ctrl.Result{}, nil
}

// shouldApprove decides whether a kubelet CSR may be auto-approved.
func (r *CertificateSigningRequestReconciler) shouldApprove(
	ctx context.Context,
	csr *certv1.CertificateSigningRequest,
	x509cr *x509.CertificateRequest,
) (bool, string, error) {
	// Every kubelet identity is CN=system:node:<name>, org=system:nodes.
	if !strings.HasPrefix(x509cr.Subject.CommonName, nodeUserPrefix) ||
		!containsStr(x509cr.Subject.Organization, nodesGroup) {
		return false, "", nil
	}
	nodeName := strings.TrimPrefix(x509cr.Subject.CommonName, nodeUserPrefix)

	machines := &infrav1.AlibabaCloudMachineList{}
	if err := r.List(ctx, machines); err != nil {
		return false, "", err
	}

	switch csr.Spec.SignerName {
	case certv1.KubeAPIServerClientKubeletSignerName:
		// Bootstrap (client) CSR. It carries no IP SANs, so it can't be bound to
		// a specific machine — gate on the node-bootstrapper SA AND the existence
		// of a CAPA machine that is provisioned but has not joined as a node yet.
		if csr.Spec.Username != nodeBootstrapperUser {
			return false, "", nil
		}
		pending, err := r.hasPendingMachine(ctx, machines.Items)
		if err != nil {
			return false, "", err
		}
		if !pending {
			return false, "", nil
		}
		return true, fmt.Sprintf("bootstrap CSR for %q; a provisioned AlibabaCloudMachine is awaiting its node", nodeName), nil

	case certv1.KubeletServingSignerName:
		// Serving CSR. Must come from the node's own kubelet, the node must
		// already exist and be backed by a CAPA machine (providerID match), and
		// every SAN must be one of that node's known addresses.
		if csr.Spec.Username != nodeUserPrefix+nodeName {
			return false, "", nil
		}
		node := &corev1.Node{}
		if err := r.Get(ctx, client.ObjectKey{Name: nodeName}, node); err != nil {
			return false, "", client.IgnoreNotFound(err)
		}
		if !nodeBackedByCAPAMachine(node, machines.Items) {
			return false, "", nil
		}
		if !sansSubsetOfNode(x509cr, node) {
			return false, "", nil
		}
		return true, fmt.Sprintf("serving CSR SANs all match node %q addresses (CAPA-backed)", nodeName), nil
	}
	return false, "", nil
}

// hasPendingMachine reports whether any provisioned AlibabaCloudMachine has no
// corresponding Node yet (its instance is absent from the node list).
func (r *CertificateSigningRequestReconciler) hasPendingMachine(ctx context.Context, machines []infrav1.AlibabaCloudMachine) (bool, error) {
	nodes := &corev1.NodeList{}
	if err := r.List(ctx, nodes); err != nil {
		return false, err
	}
	joined := make(map[string]struct{}, len(nodes.Items))
	for i := range nodes.Items {
		if id := providerInstanceID(nodes.Items[i].Spec.ProviderID); id != "" {
			joined[id] = struct{}{}
		}
	}
	for i := range machines {
		m := &machines[i]
		if m.Spec.ProviderID == nil || m.Status.InstanceID == nil {
			continue
		}
		if _, ok := joined[providerInstanceID(*m.Spec.ProviderID)]; !ok {
			return true, nil
		}
	}
	return false, nil
}

// nodeBackedByCAPAMachine reports whether the node was provisioned by a CAPA
// machine. The node providerID (set by the Alibaba CCM, dot-separated
// "alicloud://<region>.<id>") and the machine providerID (set by CAPA,
// slash-separated "alicloud://<region>/<id>") differ in separator, so we compare
// by the normalised instance ID rather than the raw string.
func nodeBackedByCAPAMachine(node *corev1.Node, machines []infrav1.AlibabaCloudMachine) bool {
	id := providerInstanceID(node.Spec.ProviderID)
	if id == "" {
		return false
	}
	for i := range machines {
		if machines[i].Spec.ProviderID != nil && providerInstanceID(*machines[i].Spec.ProviderID) == id {
			return true
		}
	}
	return false
}

// providerInstanceID extracts the ECS instance ID from a providerID, tolerating
// both the CAPA slash form (alicloud://<region>/<id>) and the CCM dot form
// (alicloud://<region>.<id>).
func providerInstanceID(providerID string) string {
	s := strings.TrimPrefix(providerID, "alicloud://")
	if i := strings.LastIndexAny(s, "/."); i >= 0 {
		return s[i+1:]
	}
	return s
}

// sansSubsetOfNode reports whether every IP/DNS SAN in the CSR is one of the
// node's advertised addresses (or the node name itself). This prevents a
// compromised kubelet from minting a serving cert for an identity it doesn't own.
func sansSubsetOfNode(cr *x509.CertificateRequest, node *corev1.Node) bool {
	ips := make(map[string]struct{})
	dns := map[string]struct{}{node.Name: {}}
	for _, addr := range node.Status.Addresses {
		switch addr.Type {
		case corev1.NodeInternalIP, corev1.NodeExternalIP:
			ips[addr.Address] = struct{}{}
		case corev1.NodeHostName, corev1.NodeInternalDNS, corev1.NodeExternalDNS:
			dns[addr.Address] = struct{}{}
		}
	}
	if len(cr.IPAddresses) == 0 && len(cr.DNSNames) == 0 {
		return false
	}
	for _, ip := range cr.IPAddresses {
		if _, ok := ips[ip.String()]; !ok {
			return false
		}
	}
	for _, d := range cr.DNSNames {
		if _, ok := dns[d]; !ok {
			return false
		}
	}
	return true
}

func isCSRDecided(csr *certv1.CertificateSigningRequest) bool {
	for _, c := range csr.Status.Conditions {
		if c.Type == certv1.CertificateApproved || c.Type == certv1.CertificateDenied {
			return true
		}
	}
	return false
}

func parseCSR(pemBytes []byte) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("PEM block is not a CERTIFICATE REQUEST")
	}
	return x509.ParseCertificateRequest(block.Bytes)
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
