package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	infrav1 "github.com/SammZhu/openshift-capi-alicloud/api/v1beta1"
	alibabaClient "github.com/SammZhu/openshift-capi-alicloud/pkg/client"
)

// AlibabaCloudClusterReconciler reconciles an AlibabaCloudCluster object.
type AlibabaCloudClusterReconciler struct {
	client.Client
	Scheme                    *runtime.Scheme
	Log                       logr.Logger
	AlibabaCloudClientBuilder alibabaClient.ClientBuilderFunc
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=alibabacloudclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=alibabacloudclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=alibabacloudclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;clusters/status,verbs=get;list;watch

// SetupWithManager sets up the controller with the Manager.
func (r *AlibabaCloudClusterReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, options controller.Options) error {
	log := ctrl.LoggerFrom(ctx)
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.AlibabaCloudCluster{}).
		WithOptions(options).
		WithEventFilter(predicates.ResourceNotPaused(mgr.GetScheme(), log)).
		Watches(
			&clusterv1.Cluster{},
			enqueueAlibabaCloudClusterForCluster(mgr.GetClient()),
			builder.WithPredicates(predicates.ClusterUnpaused(mgr.GetScheme(), log)),
		).
		Complete(r)
}

// Reconcile handles AlibabaCloudCluster reconciliation.
func (r *AlibabaCloudClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	log := ctrl.LoggerFrom(ctx)

	alibabaCluster := &infrav1.AlibabaCloudCluster{}
	if err := r.Get(ctx, req.NamespacedName, alibabaCluster); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	cluster, err := util.GetOwnerCluster(ctx, r.Client, alibabaCluster.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if cluster == nil {
		log.Info("Cluster Controller has not yet set OwnerRef")
		return ctrl.Result{}, nil
	}

	if annotations.IsPaused(cluster, alibabaCluster) {
		log.Info("AlibabaCloudCluster or linked Cluster is marked as paused, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	patchHelper, err := patch.NewHelper(alibabaCluster, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if err := patchHelper.Patch(ctx, alibabaCluster,
			patch.WithOwnedConditions{Conditions: []string{clusterv1.ReadyCondition}},
		); err != nil && reterr == nil {
			reterr = err
		}
	}()

	if !alibabaCluster.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, alibabaCluster)
	}

	return r.reconcileNormal(ctx, cluster, alibabaCluster)
}

func (r *AlibabaCloudClusterReconciler) reconcileNormal(ctx context.Context, cluster *clusterv1.Cluster, alibabaCluster *infrav1.AlibabaCloudCluster) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	controllerutil.AddFinalizer(alibabaCluster, infrav1.ClusterFinalizer)

	alibabaSDKClient, err := r.AlibabaCloudClientBuilder(r.Client, alibabaCluster.Spec.Region)
	if err != nil {
		conditions.Set(alibabaCluster, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  "AlibabaClientError",
			Message: err.Error(),
		})
		return ctrl.Result{}, fmt.Errorf("failed to create Alibaba Cloud client: %w", err)
	}
	_ = alibabaSDKClient

	if err := r.reconcileFailureDomains(ctx, alibabaCluster); err != nil {
		conditions.Set(alibabaCluster, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  "FailureDomainError",
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	}

	if err := r.reconcileVPC(ctx, alibabaCluster); err != nil {
		conditions.Set(alibabaCluster, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  "VPCReconcileError",
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	}

	if err := r.reconcileSLB(ctx, alibabaCluster); err != nil {
		conditions.Set(alibabaCluster, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  "SLBReconcileError",
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	}

	conditions.Set(alibabaCluster, metav1.Condition{
		Type:   clusterv1.ReadyCondition,
		Status: metav1.ConditionTrue,
		Reason: "AlibabaCloudClusterReady",
	})
	alibabaCluster.Status.Ready = true
	log.Info("AlibabaCloudCluster reconciled successfully")
	return ctrl.Result{}, nil
}

func (r *AlibabaCloudClusterReconciler) reconcileDelete(ctx context.Context, alibabaCluster *infrav1.AlibabaCloudCluster) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling AlibabaCloudCluster delete")

	if err := r.deleteSLB(ctx, alibabaCluster); err != nil {
		return ctrl.Result{}, err
	}

	if alibabaCluster.Spec.VpcID == "" {
		if err := r.deleteVPC(ctx, alibabaCluster); err != nil {
			return ctrl.Result{}, err
		}
	}

	controllerutil.RemoveFinalizer(alibabaCluster, infrav1.ClusterFinalizer)
	return ctrl.Result{}, nil
}

// reconcileFailureDomains publishes the available zones to CAPI so it can
// automatically spread Machines across availability zones. Each zone maps to
// a VSwitch ID stored in the FailureDomainSpec attributes.
func (r *AlibabaCloudClusterReconciler) reconcileFailureDomains(_ context.Context, alibabaCluster *infrav1.AlibabaCloudCluster) error {
	if len(alibabaCluster.Spec.FailureDomains) == 0 {
		return nil
	}
	fds := make([]clusterv1.FailureDomain, 0, len(alibabaCluster.Spec.FailureDomains))
	for _, fd := range alibabaCluster.Spec.FailureDomains {
		controlPlane := false
		fds = append(fds, clusterv1.FailureDomain{
			Name:         fd.ZoneID,
			ControlPlane: &controlPlane,
			Attributes: map[string]string{
				"vSwitchID": fd.VSwitchID,
			},
		})
	}
	alibabaCluster.Status.FailureDomains = fds
	return nil
}

func (r *AlibabaCloudClusterReconciler) reconcileVPC(ctx context.Context, alibabaCluster *infrav1.AlibabaCloudCluster) error {
	if alibabaCluster.Status.VpcID != "" {
		return nil
	}
	if alibabaCluster.Spec.VpcID != "" {
		alibabaCluster.Status.VpcID = alibabaCluster.Spec.VpcID
		return nil
	}
	ctrl.LoggerFrom(ctx).Info("VPC creation not yet implemented, skipping")
	return nil
}

func (r *AlibabaCloudClusterReconciler) reconcileSLB(ctx context.Context, alibabaCluster *infrav1.AlibabaCloudCluster) error {
	if alibabaCluster.Spec.ControlPlaneEndpoint.Host != "" {
		return nil
	}
	ctrl.LoggerFrom(ctx).Info("SLB creation not yet implemented, skipping")
	return nil
}

func (r *AlibabaCloudClusterReconciler) deleteSLB(ctx context.Context, alibabaCluster *infrav1.AlibabaCloudCluster) error {
	if alibabaCluster.Status.SLBInstanceID == "" {
		return nil
	}
	ctrl.LoggerFrom(ctx).Info("SLB deletion not yet implemented", "slbID", alibabaCluster.Status.SLBInstanceID)
	return nil
}

func (r *AlibabaCloudClusterReconciler) deleteVPC(ctx context.Context, alibabaCluster *infrav1.AlibabaCloudCluster) error {
	if alibabaCluster.Status.VpcID == "" {
		return nil
	}
	ctrl.LoggerFrom(ctx).Info("VPC deletion not yet implemented", "vpcID", alibabaCluster.Status.VpcID)
	return nil
}
