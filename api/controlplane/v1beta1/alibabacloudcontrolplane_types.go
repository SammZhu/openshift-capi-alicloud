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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

// ControlPlaneMode declares how the Kubernetes control plane is managed.
// +kubebuilder:validation:Enum=external
type ControlPlaneMode string

const (
	// ControlPlaneModeExternal adopts a pre-existing, externally-managed control
	// plane (e.g. an OpenShift cluster installed out-of-band by the Assisted
	// Installer / ROS). The provider provisions nothing; it only reports the
	// control plane as initialized + externally-managed. This is the only mode
	// implemented today; a future "managed" mode would provision the control
	// plane (master instances) itself.
	ControlPlaneModeExternal ControlPlaneMode = "external"
)

// AlibabaCloudControlPlaneSpec defines the desired state of AlibabaCloudControlPlane.
type AlibabaCloudControlPlaneSpec struct {
	// Mode declares how the control plane is managed. "external" (the default)
	// adopts a control plane that already exists and is managed outside Cluster
	// API.
	// +kubebuilder:default=external
	// +optional
	Mode ControlPlaneMode `json:"mode,omitempty"`

	// Version is the Kubernetes version of the control plane. Required by the
	// Cluster API control-plane contract; it should match the version the
	// external control plane actually serves (e.g. v1.33.0).
	// +kubebuilder:validation:MinLength=1
	Version string `json:"version"`

	// ControlPlaneEndpoint is the endpoint used to reach the API server. For an
	// externally-managed control plane this is the existing cluster's internal
	// API endpoint (api-int). When set it is surfaced on Cluster.spec.controlPlaneEndpoint.
	// +optional
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint,omitempty"`
}

// AlibabaCloudControlPlaneInitializationStatus carries the Cluster API
// control-plane contract's initialization signal.
type AlibabaCloudControlPlaneInitializationStatus struct {
	// ControlPlaneInitialized is true once the Kubernetes control plane can
	// accept requests. CAPI core bubbles this up to Cluster's
	// status.initialization.controlPlaneInitialized + the ControlPlaneInitialized
	// condition, which in turn gates worker Machine node-health (and therefore
	// MachineDeployment readiness).
	// +optional
	ControlPlaneInitialized *bool `json:"controlPlaneInitialized,omitempty"`
}

// AlibabaCloudControlPlaneStatus defines the observed state of AlibabaCloudControlPlane.
type AlibabaCloudControlPlaneStatus struct {
	// Ready denotes that the control plane is ready.
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// ExternalManagedControlPlane signals the control plane is externally managed
	// (Cluster API contract): control-plane Node objects are not managed by CAPI.
	// Set true so CAPI core skips control-plane-node bookkeeping (the AKS/EKS/GKE
	// pattern; see util.IsExternalManagedControlPlane).
	// +optional
	ExternalManagedControlPlane *bool `json:"externalManagedControlPlane,omitempty"`

	// Initialization provides the control-plane contract's initialized signal.
	// +optional
	Initialization *AlibabaCloudControlPlaneInitializationStatus `json:"initialization,omitempty"`

	// Version is the observed Kubernetes version of the control plane.
	// +optional
	Version string `json:"version,omitempty"`

	// Conditions defines current service state of the AlibabaCloudControlPlane.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=alibabacloudcontrolplanes,scope=Namespaced,categories=cluster-api
// +kubebuilder:metadata:labels=cluster.x-k8s.io/v1beta2=v1beta1
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".metadata.labels['cluster\\.x-k8s\\.io/cluster-name']"
// +kubebuilder:printcolumn:name="Mode",type="string",JSONPath=".spec.mode"
// +kubebuilder:printcolumn:name="Initialized",type="string",JSONPath=".status.initialization.controlPlaneInitialized"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".spec.version"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// AlibabaCloudControlPlane represents a Kubernetes control plane for use as a
// Cluster's spec.controlPlaneRef. Today it only models an EXTERNALLY-managed
// control plane (mode=external): the control plane already exists out-of-band, so
// this object simply reports it as initialized + externally-managed so CAPI core
// will drive worker MachineDeployments under the same Cluster.
type AlibabaCloudControlPlane struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AlibabaCloudControlPlaneSpec   `json:"spec,omitempty"`
	Status AlibabaCloudControlPlaneStatus `json:"status,omitempty"`
}

// GetConditions returns the conditions of the AlibabaCloudControlPlane.
func (c *AlibabaCloudControlPlane) GetConditions() []metav1.Condition {
	return c.Status.Conditions
}

// SetConditions sets the conditions of the AlibabaCloudControlPlane.
func (c *AlibabaCloudControlPlane) SetConditions(conditions []metav1.Condition) {
	c.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// AlibabaCloudControlPlaneList contains a list of AlibabaCloudControlPlane.
type AlibabaCloudControlPlaneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AlibabaCloudControlPlane `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AlibabaCloudControlPlane{}, &AlibabaCloudControlPlaneList{})
}
