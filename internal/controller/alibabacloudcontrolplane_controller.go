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
	"fmt"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"

	cpv1 "github.com/SammZhu/openshift-capi-alicloud/api/controlplane/v1beta1"
)

// AlibabaCloudControlPlaneReconciler reconciles an AlibabaCloudControlPlane.
//
// Today it only implements the EXTERNALLY-managed control plane (the AKS/EKS/GKE
// pattern): the Kubernetes control plane already exists and is managed out-of-band
// (e.g. an OpenShift cluster brought up by the Assisted Installer / ROS), so this
// controller provisions nothing — it just reports the control plane as initialized
// + externally-managed. CAPI core then marks the owning Cluster's
// ControlPlaneInitialized condition True, which unblocks worker Machine node-health
// (and therefore MachineDeployment readiness) without representing the masters as
// Cluster API Machines.
type AlibabaCloudControlPlaneReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

// +kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=alibabacloudcontrolplanes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=alibabacloudcontrolplanes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;clusters/status,verbs=get;list;watch

// SetupWithManager sets up the controller with the Manager.
func (r *AlibabaCloudControlPlaneReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, options controller.Options) error {
	log := ctrl.LoggerFrom(ctx)
	return ctrl.NewControllerManagedBy(mgr).
		For(&cpv1.AlibabaCloudControlPlane{}).
		WithOptions(options).
		WithEventFilter(predicates.ResourceNotPaused(mgr.GetScheme(), log)).
		Complete(r)
}

// Reconcile handles AlibabaCloudControlPlane reconciliation.
func (r *AlibabaCloudControlPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	log := ctrl.LoggerFrom(ctx)

	cp := &cpv1.AlibabaCloudControlPlane{}
	if err := r.Get(ctx, req.NamespacedName, cp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Respect pause when an owner Cluster is set (tolerate it not being set yet —
	// the Cluster controller wires the ownerRef asynchronously).
	cluster, err := util.GetOwnerCluster(ctx, r.Client, cp.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if cluster != nil && annotations.IsPaused(cluster, cp) {
		log.Info("AlibabaCloudControlPlane or linked Cluster is paused, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	patchHelper, err := patch.NewHelper(cp, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if err := patchHelper.Patch(ctx, cp,
			patch.WithOwnedConditions{Conditions: []string{clusterv1.ReadyCondition}},
		); err != nil && reterr == nil {
			reterr = err
		}
	}()

	// External control plane is not ours: no finalizer, nothing to clean up on delete.
	if !cp.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	switch cp.Spec.Mode {
	case cpv1.ControlPlaneModeExternal, "":
		r.reconcileExternal(cp)
	default:
		conditions.Set(cp, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  "UnsupportedMode",
			Message: fmt.Sprintf("control plane mode %q is not supported (only %q is implemented)", cp.Spec.Mode, cpv1.ControlPlaneModeExternal),
		})
	}

	return ctrl.Result{}, nil
}

// reconcileExternal adopts a pre-existing, externally-managed control plane.
// The control plane is brought up out-of-band, so we report it as initialized +
// externally-managed without provisioning anything. CAPI core reads
// status.initialization.controlPlaneInitialized (to set the Cluster's
// ControlPlaneInitialized condition, which gates worker node-health) and
// status.externalManagedControlPlane (to skip control-plane-node bookkeeping).
func (r *AlibabaCloudControlPlaneReconciler) reconcileExternal(cp *cpv1.AlibabaCloudControlPlane) {
	t := true
	cp.Status.ExternalManagedControlPlane = &t
	cp.Status.Initialization = &cpv1.AlibabaCloudControlPlaneInitializationStatus{ControlPlaneInitialized: &t}
	cp.Status.Ready = true
	cp.Status.Version = cp.Spec.Version
	conditions.Set(cp, metav1.Condition{
		Type:   clusterv1.ReadyCondition,
		Status: metav1.ConditionTrue,
		Reason: "ExternalControlPlaneAdopted",
	})
}
