package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
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

// DefaultCCMGracePeriod is how long a Node may carry the cloud-provider
// uninitialized taint before we treat it as evidence that the CCM is missing
// (vs. a node that just joined and the CCM hasn't reached yet).
const DefaultCCMGracePeriod = 5 * time.Minute

// uninitializedTaintKey is the taint the kubelet sets (with
// --cloud-provider=external) and the cloud-controller-manager removes once it has
// initialized the Node. Defined locally to avoid a k8s.io/cloud-provider dep.
const uninitializedTaintKey = "node.cloudprovider.kubernetes.io/uninitialized"

// AlibabaCloudClusterReconciler reconciles an AlibabaCloudCluster object.
type AlibabaCloudClusterReconciler struct {
	client.Client
	Scheme                    *runtime.Scheme
	Log                       logr.Logger
	Recorder                  record.EventRecorder
	AlibabaCloudClientBuilder alibabaClient.ClientBuilderFunc
	// CCMGracePeriod is how long a Node may carry the uninitialized taint before
	// the preflight flags a missing CCM. main sets it to ccmGracePeriod; zero means
	// flag immediately (used by tests).
	CCMGracePeriod time.Duration
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=alibabacloudclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=alibabacloudclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=alibabacloudclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;clusters/status,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch

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
			patch.WithOwnedConditions{Conditions: []string{
				clusterv1.ReadyCondition,
				infrav1.CloudControllerManagerReadyCondition,
				infrav1.ClusterAPICoreReadyCondition,
			}},
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

	if err := r.reconcileControlPlaneEndpoint(ctx, alibabaCluster); err != nil {
		conditions.Set(alibabaCluster, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  "ControlPlaneEndpointError",
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	}

	// CAPI infra-cluster contract: status.controlPlaneEndpoint must be set
	// before status.ready=true.  In BYO mode the endpoint comes from the spec
	// (provisioned out-of-band by ROS); without it, mark not-ready and requeue
	// rather than reporting a cluster that downstream controllers can't reach.
	if alibabaCluster.Status.ControlPlaneEndpoint.Host == "" {
		log.Info("controlPlaneEndpoint not yet available, requeueing")
		conditions.Set(alibabaCluster, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  "ControlPlaneEndpointMissing",
			Message: "spec.controlPlaneEndpoint is required (BYO infrastructure) and is not set",
		})
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	// Runtime preflight: CAPA never initializes Nodes — the CCM does. Surface a
	// missing/broken CCM as a dedicated condition + Event so workers stuck
	// uninitialized are diagnosable, instead of hanging silently (P3-CAPA.27).
	r.checkCloudControllerManager(ctx, alibabaCluster)

	// Runtime preflight: report which CAPI core we coexist with (self-bundled vs
	// OCP-hosted), and flag a dual-core conflict. The startup preflight already
	// fails fast on conflict; this is the observable steady-state mirror (P3-CAPA.29).
	r.checkClusterAPICore(ctx, alibabaCluster)

	conditions.Set(alibabaCluster, metav1.Condition{
		Type:   clusterv1.ReadyCondition,
		Status: metav1.ConditionTrue,
		Reason: "AlibabaCloudClusterReady",
	})
	alibabaCluster.Status.Ready = true
	// v1beta2 infra-cluster contract: CAPI core reads readiness from
	// status.initialization.provisioned (status.ready is ignored under v1beta2),
	// then sets Cluster.status.infrastructureReady and copies failureDomains.
	provisioned := true
	alibabaCluster.Status.Initialization = &infrav1.AlibabaCloudClusterInitializationStatus{Provisioned: &provisioned}
	log.Info("AlibabaCloudCluster reconciled successfully")
	return ctrl.Result{}, nil
}

// checkCloudControllerManager sets the CloudControllerManagerReady condition by
// looking for worker Nodes stuck with the cloud-provider uninitialized taint past
// the grace period — the canonical symptom of a missing/broken CCM. It never fails
// the reconcile (informational/degraded), and skips recently-joined Nodes so a CCM
// that simply hasn't caught up yet is not flagged.
func (r *AlibabaCloudClusterReconciler) checkCloudControllerManager(ctx context.Context, alibabaCluster *infrav1.AlibabaCloudCluster) {
	log := ctrl.LoggerFrom(ctx)

	nodes := &corev1.NodeList{}
	if err := r.List(ctx, nodes); err != nil {
		log.Info("CCM preflight: unable to list Nodes, skipping", "error", err.Error())
		return
	}

	cutoff := time.Now().Add(-r.CCMGracePeriod)
	var stuck []string
	for i := range nodes.Items {
		n := &nodes.Items[i]
		if n.CreationTimestamp.Time.After(cutoff) {
			continue // recently joined — give the CCM time to reach it
		}
		for _, t := range n.Spec.Taints {
			if t.Key == uninitializedTaintKey {
				stuck = append(stuck, n.Name)
				break
			}
		}
	}

	if len(stuck) > 0 {
		msg := fmt.Sprintf("%d Node(s) stuck with the %q taint for over %s — is the Alibaba "+
			"cloud-controller-manager (CCM) running? CAPA does not initialize Nodes; without the "+
			"CCM, workers never become Ready. Nodes: %v",
			len(stuck), uninitializedTaintKey, r.CCMGracePeriod, stuck)
		conditions.Set(alibabaCluster, metav1.Condition{
			Type:    infrav1.CloudControllerManagerReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  infrav1.NodesAwaitingCloudProviderReason,
			Message: msg,
		})
		if r.Recorder != nil {
			r.Recorder.Event(alibabaCluster, corev1.EventTypeWarning, infrav1.NodesAwaitingCloudProviderReason, msg)
		}
		log.Info("CCM preflight degraded", "stuckNodes", stuck)
		return
	}

	conditions.Set(alibabaCluster, metav1.Condition{
		Type:   infrav1.CloudControllerManagerReadyCondition,
		Status: metav1.ConditionTrue,
		Reason: infrav1.CloudControllerManagerHealthyReason,
	})
}

// checkClusterAPICore sets the ClusterAPICoreReady condition by detecting which
// CAPI core controller-managers are running (by the cluster.x-k8s.io/provider
// label). One core is healthy — Bundled (self-installed) or Reused (OCP-hosted);
// two or more across distinct namespaces is a mutual-exclusion conflict that gets
// a False condition + Warning Event. It never fails the reconcile (the startup
// preflight is the hard gate); a missing/unlistable core is left untouched so the
// existing CRD preflight remains the authority (P3-CAPA.29).
func (r *AlibabaCloudClusterReconciler) checkClusterAPICore(ctx context.Context, alibabaCluster *infrav1.AlibabaCloudCluster) {
	log := ctrl.LoggerFrom(ctx)

	namespaces, err := DetectCAPICoreNamespaces(ctx, r.Client)
	if err != nil {
		log.Info("CAPI core preflight: unable to list Deployments, skipping", "error", err.Error())
		return
	}

	switch ClassifyCAPICore(namespaces) {
	case CAPICoreConflict:
		msg := fmt.Sprintf("Cluster API core (cluster.x-k8s.io) detected in multiple namespaces %v — "+
			"a self-bundled core and the OCP-hosted core (cluster-capi-operator) are mutually exclusive "+
			"and will fight over the shared CRDs/webhooks/leader election. Run provider-only against the "+
			"OCP-hosted core, or remove the OCP-hosted core and keep the self-bundled one.", namespaces)
		conditions.Set(alibabaCluster, metav1.Condition{
			Type:    infrav1.ClusterAPICoreReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  infrav1.MultipleCAPICoresConflictReason,
			Message: msg,
		})
		if r.Recorder != nil {
			r.Recorder.Event(alibabaCluster, corev1.EventTypeWarning, infrav1.MultipleCAPICoresConflictReason, msg)
		}
		log.Info("CAPI core preflight conflict", "namespaces", namespaces)
	case CAPICoreReused:
		conditions.Set(alibabaCluster, metav1.Condition{
			Type:    infrav1.ClusterAPICoreReadyCondition,
			Status:  metav1.ConditionTrue,
			Reason:  infrav1.ReusingHostedCAPICoreReason,
			Message: fmt.Sprintf("reusing the OCP-hosted Cluster API core in %q (provider-only)", ocpHostedCAPINamespace),
		})
	case CAPICoreBundled:
		conditions.Set(alibabaCluster, metav1.Condition{
			Type:    infrav1.ClusterAPICoreReadyCondition,
			Status:  metav1.ConditionTrue,
			Reason:  infrav1.BundledCAPICoreReason,
			Message: fmt.Sprintf("using the self-bundled Cluster API core in %q", namespaces[0]),
		})
	case CAPICoreNone:
		// No core controller Deployment visible. Leave the condition unset: the
		// startup RESTMapping preflight already gates on the CAPI core CRDs, and a
		// restrictive RBAC could simply hide Deployments from us.
	}
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

// reconcileControlPlaneEndpoint mirrors the BYO Spec.ControlPlaneEndpoint into
// status so downstream controllers (Machine, control-plane provider) can reach
// the API server.  This provider does not provision an SLB — the control-plane
// endpoint (api-int NLB / PrivateZone) is created out-of-band by ROS, so the
// operator supplies it via Spec.ControlPlaneEndpoint.  When the spec endpoint
// is empty we leave status empty; reconcileNormal then keeps the cluster
// not-ready (see the ControlPlaneEndpointMissing gate).
func (r *AlibabaCloudClusterReconciler) reconcileControlPlaneEndpoint(ctx context.Context, alibabaCluster *infrav1.AlibabaCloudCluster) error {
	if alibabaCluster.Spec.ControlPlaneEndpoint.Host != "" {
		alibabaCluster.Status.ControlPlaneEndpoint = alibabaCluster.Spec.ControlPlaneEndpoint
		return nil
	}
	ctrl.LoggerFrom(ctx).Info("spec.controlPlaneEndpoint is empty; SLB provisioning is out-of-scope (BYO via ROS)")
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
