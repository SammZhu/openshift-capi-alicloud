// Package v1beta1 contains the control-plane API for AlibabaCloud.
//
// It implements the Cluster API control-plane contract for an EXTERNALLY-managed
// Kubernetes control plane (the AKS/EKS/GKE pattern): the control plane already
// exists and is managed outside Cluster API (e.g. an OpenShift cluster installed
// by the Assisted Installer / ROS), so this provider provisions nothing — it only
// reports the control plane as initialized + externally-managed so CAPI core can
// drive worker MachineDeployments under the same Cluster.
//
// +kubebuilder:object:generate=true
// +groupName=controlplane.cluster.x-k8s.io
package v1beta1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "controlplane.cluster.x-k8s.io", Version: "v1beta1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
