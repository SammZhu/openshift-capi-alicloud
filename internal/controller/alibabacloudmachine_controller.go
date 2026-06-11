package controller

import (
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sdkerrors "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	infrav1 "github.com/SammZhu/openshift-capi-alicloud/api/v1beta1"
	alibabaClient "github.com/SammZhu/openshift-capi-alicloud/pkg/client"
)

// AlibabaCloudMachineReconciler reconciles an AlibabaCloudMachine object.
type AlibabaCloudMachineReconciler struct {
	client.Client
	Scheme                    *runtime.Scheme
	Log                       logr.Logger
	AlibabaCloudClientBuilder alibabaClient.ClientBuilderFunc
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=alibabacloudmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=alibabacloudmachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=alibabacloudmachines/finalizers,verbs=update
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;machines/status,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupWithManager sets up the controller with the Manager.
func (r *AlibabaCloudMachineReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, options controller.Options) error {
	log := ctrl.LoggerFrom(ctx)
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.AlibabaCloudMachine{}).
		WithOptions(options).
		WithEventFilter(predicates.ResourceNotPaused(mgr.GetScheme(), log)).
		Complete(r)
}

// Reconcile handles AlibabaCloudMachine reconciliation.
func (r *AlibabaCloudMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	log := ctrl.LoggerFrom(ctx)

	alibabaCloudMachine := &infrav1.AlibabaCloudMachine{}
	if err := r.Get(ctx, req.NamespacedName, alibabaCloudMachine); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	patchHelper, err := patch.NewHelper(alibabaCloudMachine, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if err := patchHelper.Patch(ctx, alibabaCloudMachine,
			patch.WithOwnedConditions{Conditions: []string{clusterv1.ReadyCondition}},
		); err != nil && reterr == nil {
			reterr = err
		}
	}()

	// P3-CAPA.11: deletion is self-contained — reconcileDelete needs only the
	// AlibabaCloudMachine itself (Status.InstanceID + region from Spec/
	// providerID), not the owner Machine or Cluster.  Handle it BEFORE those
	// lookups so the finalizer can always be removed.  Otherwise, if the owning
	// Machine was already deleted, GetOwnerMachine errors and we never reach
	// reconcileDelete — leaving the AlibabaCloudMachine (and its ECS) orphaned
	// with a stuck finalizer (observed 2026-06-06 in the PR2 delete smoke).
	if !alibabaCloudMachine.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, alibabaCloudMachine)
	}

	machine, err := util.GetOwnerMachine(ctx, r.Client, alibabaCloudMachine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if machine == nil {
		log.Info("Machine Controller has not yet set OwnerRef")
		return ctrl.Result{}, nil
	}

	cluster, err := util.GetClusterFromMetadata(ctx, r.Client, machine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("AlibabaCloudMachine owner Machine is missing cluster label or cluster does not exist: %w", err)
	}
	if cluster == nil {
		log.Info("Cluster not found, skipping")
		return ctrl.Result{}, nil
	}

	if annotations.IsPaused(cluster, alibabaCloudMachine) {
		log.Info("AlibabaCloudMachine or linked Cluster is marked as paused, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	alibabaCluster := &infrav1.AlibabaCloudCluster{}
	infraRef := client.ObjectKey{
		Namespace: alibabaCloudMachine.Namespace,
		Name:      cluster.Spec.InfrastructureRef.Name,
	}
	if err := r.Get(ctx, infraRef, alibabaCluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get AlibabaCloudCluster: %w", err)
	}

	return r.reconcileNormal(ctx, machine, alibabaCluster, alibabaCloudMachine)
}

func (r *AlibabaCloudMachineReconciler) reconcileNormal(
	ctx context.Context,
	machine *clusterv1.Machine,
	alibabaCluster *infrav1.AlibabaCloudCluster,
	alibabaCloudMachine *infrav1.AlibabaCloudMachine,
) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	controllerutil.AddFinalizer(alibabaCloudMachine, infrav1.MachineFinalizer)

	if !alibabaCluster.Status.Ready {
		log.Info("AlibabaCloudCluster is not ready yet, requeueing")
		conditions.Set(alibabaCloudMachine, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  "ClusterInfrastructureNotReady",
			Message: "cluster infrastructure not ready",
		})
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	// CAPI contract: do not create the instance until the bootstrap provider
	// has produced the data secret and the Machine controller has populated
	// Spec.Bootstrap.DataSecretName.  Booting an ECS without bootstrap data
	// would leave a node that never joins.  Skip this gate only when the
	// machine carries the legacy Spec.UserDataSecret (pre-CAPI-bootstrap use).
	if machine.Spec.Bootstrap.DataSecretName == nil && alibabaCloudMachine.Spec.UserDataSecret == nil {
		log.Info("Waiting for bootstrap data secret to be available")
		conditions.Set(alibabaCloudMachine, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  "WaitingForBootstrapData",
			Message: "bootstrap data secret reference is not yet available",
		})
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	// Resolve zone and VSwitch from the CAPI-assigned failure domain when not
	// explicitly set in the machine spec. This enables automatic multi-AZ
	// spreading when AlibabaCloudCluster.Spec.FailureDomains is configured.
	if err := r.resolveFailureDomain(machine, alibabaCluster, alibabaCloudMachine); err != nil {
		conditions.Set(alibabaCloudMachine, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  "FailureDomainError",
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	}

	region := resolveRegion(alibabaCloudMachine, alibabaCluster)

	alibabaSDKClient, err := r.AlibabaCloudClientBuilder(r.Client, region)
	if err != nil {
		conditions.Set(alibabaCloudMachine, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  "AlibabaClientError",
			Message: err.Error(),
		})
		return ctrl.Result{}, fmt.Errorf("failed to create Alibaba Cloud client: %w", err)
	}

	instance, err := r.findOrCreateInstance(ctx, alibabaSDKClient, machine, alibabaCluster, alibabaCloudMachine)
	if err != nil {
		// Terminal failures (instance vanished, instance entered a terminal
		// ECS state) are non-recoverable: record FailureReason/FailureMessage
		// per the CAPI contract and stop requeueing — retrying cannot make
		// progress, and the Machine controller will remediate.
		var termErr *terminalError
		if errors.As(err, &termErr) {
			r.setFailed(alibabaCloudMachine, termErr.reason, termErr.message)
			log.Error(err, "terminal failure provisioning AlibabaCloudMachine",
				"instanceID", ptrStr(alibabaCloudMachine.Status.InstanceID))
			return ctrl.Result{}, nil
		}
		conditions.Set(alibabaCloudMachine, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  "InstanceReconcileError",
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	}
	if instance == nil {
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	conditions.Set(alibabaCloudMachine, metav1.Condition{
		Type:   clusterv1.ReadyCondition,
		Status: metav1.ConditionTrue,
		Reason: "AlibabaCloudMachineReady",
	})
	alibabaCloudMachine.Status.Ready = true
	// v1beta2 infra-machine contract: CAPI core reads readiness from
	// status.initialization.provisioned (status.ready is ignored under v1beta2)
	// to mark the owning Machine's infrastructure ready (gates nodeRef/bootstrap).
	provisioned := true
	alibabaCloudMachine.Status.Initialization = &infrav1.AlibabaCloudMachineInitializationStatus{Provisioned: &provisioned}
	log.Info("AlibabaCloudMachine reconciled successfully", "instanceID", alibabaCloudMachine.Status.InstanceID)
	return ctrl.Result{}, nil
}

func (r *AlibabaCloudMachineReconciler) reconcileDelete(ctx context.Context, alibabaCloudMachine *infrav1.AlibabaCloudMachine) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling AlibabaCloudMachine delete")

	// Resolve the ECS handle from the most durable signal available — NOT just
	// Status.InstanceID. Status is a subresource write that can be lost to a patch
	// conflict after RunInstances succeeds (the very lost-write race the create
	// path guards against with adopt-by-tag). Spec.ProviderID
	// (alicloud://<region>.<id>) is written on the same create reconcile but lands
	// on the spec, so it survives a lost status write and still names the live ECS.
	// Keying the "nothing provisioned" shortcut off Status.InstanceID alone orphans
	// a billable instance whenever that single write was lost (G8).
	instanceID := ""
	if alibabaCloudMachine.Status.InstanceID != nil {
		instanceID = *alibabaCloudMachine.Status.InstanceID
	} else if alibabaCloudMachine.Spec.ProviderID != nil {
		instanceID = providerInstanceID(*alibabaCloudMachine.Spec.ProviderID)
	}

	region, regionErr := regionFromMachine(alibabaCloudMachine)

	// Neither an instance handle nor a region: createInstance persists BOTH
	// Spec.ProviderID (carrying region and ID) AND Spec.RegionID before RunInstances,
	// so reaching here means no instance was ever created. Nothing to clean up —
	// safe to drop the finalizer.
	if instanceID == "" && regionErr != nil {
		log.Info("no ECS instance recorded and no region resolvable; nothing was provisioned, removing finalizer")
		controllerutil.RemoveFinalizer(alibabaCloudMachine, infrav1.MachineFinalizer)
		return ctrl.Result{}, nil
	}
	// We know (or suspect) an instance exists but cannot resolve its region to reach
	// the API. Dropping the finalizer now would orphan it — keep it and requeue.
	if regionErr != nil {
		return ctrl.Result{}, fmt.Errorf("cannot determine region to delete instance %s: %w", instanceID, regionErr)
	}

	// Self-heal a lost Status.InstanceID write from the durable providerID so the
	// rest of the delete flow (and deleteInstance, which reads Status.InstanceID)
	// has a handle to act on.
	if alibabaCloudMachine.Status.InstanceID == nil && instanceID != "" {
		alibabaCloudMachine.Status.InstanceID = &instanceID
	}

	alibabaSDKClient, err := r.AlibabaCloudClientBuilder(r.Client, region)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create Alibaba Cloud client: %w", err)
	}

	// CAPI contract: the infrastructure must be actually gone before we drop the
	// finalizer, otherwise the owning Machine (and Node) is reported deleted
	// while a billable ECS instance lives on.  Poll DescribeInstance and only
	// release the finalizer once the instance no longer exists.
	var info *instanceInfo
	if instanceID != "" {
		info, err = r.describeInstance(ctx, alibabaSDKClient, instanceID)
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	if info == nil {
		// The recorded instance (if any) is gone. Before trusting that and releasing
		// the finalizer, sweep by the durable per-machine tag: a lost InstanceID
		// write could have left a tagged, billable ECS we never recorded (G8). This
		// mirrors the create path's adopt-by-tag idempotency — only release the
		// finalizer once the tag sweep is ALSO clear.
		orphanID, sweepErr := alibabaSDKClient.FindInstanceByTag(region, alibabaClient.MachineNameTagKey, alibabaCloudMachine.Name)
		if sweepErr != nil {
			return ctrl.Result{}, sweepErr
		}
		if orphanID != "" {
			log.Info("found orphan ECS by tag (Status.InstanceID write was lost); deleting before releasing finalizer",
				"instanceID", orphanID, "machine", alibabaCloudMachine.Name)
			if delErr := alibabaSDKClient.DeleteECSInstance(orphanID, true); delErr != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete orphan ECS %s: %w", orphanID, delErr)
			}
			// Re-describe on the next pass; do not drop the finalizer until gone.
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
		log.Info("ECS instance is gone, removing finalizer", "instanceID", instanceID)
		r.cleanupOffloadedIgnition(ctx, alibabaSDKClient, region, alibabaCloudMachine)
		controllerutil.RemoveFinalizer(alibabaCloudMachine, infrav1.MachineFinalizer)
		return ctrl.Result{}, nil
	}

	// Still present.  Surface progress on the Ready condition and keep the
	// instance state in status for observability.
	alibabaCloudMachine.Status.InstanceState = &info.State
	conditions.Set(alibabaCloudMachine, metav1.Condition{
		Type:    clusterv1.ReadyCondition,
		Status:  metav1.ConditionFalse,
		Reason:  "Deleting",
		Message: fmt.Sprintf("ECS instance %s is %q; waiting for termination", instanceID, info.State),
	})

	// Issue the (force) delete unless the instance is genuinely leaving on its
	// own.  Stopping is a transient state heading toward Stopped, and Deleted
	// means the release is already committed and the record will vanish shortly —
	// re-issuing DeleteInstance against either only yields IncorrectInstanceStatus
	// noise, so we just requeue and poll.
	//
	// Stopped, by contrast, is a *stable* resting state on Alibaba Cloud: an
	// instance that was force-stopped (or whose first force-delete only stopped
	// it instead of releasing it) sits in Stopped indefinitely and never
	// progresses to released on its own.  Stopped is also the canonical deletable
	// state — DeleteInstance against a Stopped instance is exactly how it is
	// released.  So Stopped MUST fall through to the delete branch; treating it as
	// "already terminating" deadlocks the finalizer forever (the ECS is never
	// freed and the owning Machine/Node hangs in Terminating).  See P3-CAPA.12.
	switch info.State {
	case infrav1.InstanceStateStopping, infrav1.InstanceStateDeleted:
		log.Info("ECS instance is terminating, waiting", "instanceID", instanceID, "state", info.State)
	default:
		if err := r.deleteInstance(ctx, alibabaSDKClient, alibabaCloudMachine); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// cleanupOffloadedIgnition best-effort deletes the machine's offloaded Ignition
// OSS object. Failures are logged, not fatal — an OSS bucket lifecycle rule on
// the capi-ignition/ prefix is the authoritative backstop against leaks.
func (r *AlibabaCloudMachineReconciler) cleanupOffloadedIgnition(
	ctx context.Context,
	c alibabaClient.Client,
	region string,
	m *infrav1.AlibabaCloudMachine,
) {
	ref := m.Status.IgnitionOSS
	if ref == nil {
		return
	}
	log := ctrl.LoggerFrom(ctx)
	if err := c.DeleteIgnitionObject(alibabaClient.IgnitionStoreParams{
		Bucket:   ref.Bucket,
		Endpoint: ref.Endpoint,
		RegionID: region,
		Key:      ref.Key,
	}); err != nil {
		log.Error(err, "best-effort: failed to delete offloaded ignition object", "key", ref.Key)
		return
	}
	log.Info("deleted offloaded ignition object", "key", ref.Key)
}

// resolveRegion returns the effective region for a machine: the explicit
// Spec.RegionID when set, otherwise the owning cluster's Spec.Region.
// AlibabaCloudMachine.Spec.RegionID is optional (region normally lives on the
// cluster), so callers that need a region for SDK calls / providerID MUST go
// through this — using Spec.RegionID directly yields "" and breaks both the
// ECS client and the providerID.
func resolveRegion(m *infrav1.AlibabaCloudMachine, c *infrav1.AlibabaCloudCluster) string {
	if m.Spec.RegionID != "" {
		return m.Spec.RegionID
	}
	if c != nil {
		return c.Spec.Region
	}
	return ""
}

// IMDS hardening defaults: a metadata endpoint that is reachable only with a
// session token (IMDSv2-equivalent). Applied when the machine does not specify
// MetadataOptions, or leaves individual fields empty.
const (
	defaultHTTPEndpoint = "enabled"
	defaultHTTPTokens   = "required"
)

// resolveMetadataOptions returns the effective (httpEndpoint, httpTokens,
// hopLimit) for RunInstances, defaulting to the secure IMDSv2 baseline. A nil
// spec, or empty individual fields, fall back to the defaults — so the only way
// to get tokenless metadata is to set httpTokens=optional explicitly.
func resolveMetadataOptions(o *infrav1.MetadataOptions) (endpoint, tokens string, hopLimit int) {
	endpoint, tokens = defaultHTTPEndpoint, defaultHTTPTokens
	if o == nil {
		return endpoint, tokens, 0
	}
	if o.HttpEndpoint != "" {
		endpoint = o.HttpEndpoint
	}
	if o.HttpTokens != "" {
		tokens = o.HttpTokens
	}
	return endpoint, tokens, o.HttpPutResponseHopLimit
}

// providerIDFor builds the machine's providerID. It MUST byte-for-byte match
// what the Alibaba cloud-controller-manager writes onto Node.spec.providerID,
// because CAPI core binds Machine.status.nodeRef by an EXACT string compare of
// Machine.spec.providerID == Node.spec.providerID (it does not normalise). The
// Alibaba CCM uses the DOT form "alicloud://<region>.<instanceID>" (observed on
// the master node: alicloud://cn-wulanchabu.i-...), so we emit the dot form too;
// the slash form we used before never matched a Node and left workers without a
// nodeRef. (regionFromMachine and csr_controller.providerInstanceID already
// tolerate both separators, so the dot form is safe everywhere else.)
// region must be the RESOLVED region (never the often-empty Spec.RegionID) so we
// never regress to the old "alicloud://.i-abc" empty-region parse failure.
func providerIDFor(region, instanceID string) string {
	return fmt.Sprintf("alicloud://%s.%s", region, instanceID)
}

// regionFromMachine returns the region for a machine for use on the delete
// path (where the owning cluster is not loaded).  Tries, in order:
//  1. Spec.RegionID
//  2. region segment of the providerID — accepts the current slash form
//     (alicloud://<region>/<id>) and the legacy dot form
//     (alicloud://<region>.<id>) for backward compatibility with machines
//     created before the format fix.
func regionFromMachine(m *infrav1.AlibabaCloudMachine) (string, error) {
	if m.Spec.RegionID != "" {
		return m.Spec.RegionID, nil
	}
	if m.Spec.ProviderID != nil {
		trimmed := strings.TrimPrefix(*m.Spec.ProviderID, "alicloud://")
		// Current form: <region>/<instanceID>
		if parts := strings.SplitN(trimmed, "/", 2); len(parts) == 2 && parts[0] != "" {
			return parts[0], nil
		}
		// Legacy form: <region>.<instanceID>
		if parts := strings.SplitN(trimmed, ".", 2); len(parts) == 2 && parts[0] != "" {
			return parts[0], nil
		}
	}
	return "", fmt.Errorf("Spec.RegionID is empty and ProviderID is unset or invalid")
}

// findOrCreateInstance looks up an existing ECS instance by InstanceID or creates a new one.
// Returns nil, nil when the instance exists but is not yet Running — the caller must requeue.
// Returns a non-nil *instanceInfo only when the instance is Running and ready to be marked Ready.
func (r *AlibabaCloudMachineReconciler) findOrCreateInstance(
	ctx context.Context,
	alibabaSDKClient alibabaClient.Client,
	machine *clusterv1.Machine,
	alibabaCluster *infrav1.AlibabaCloudCluster,
	alibabaCloudMachine *infrav1.AlibabaCloudMachine,
) (*instanceInfo, error) {
	log := ctrl.LoggerFrom(ctx)

	if alibabaCloudMachine.Status.InstanceID != nil {
		instanceID := *alibabaCloudMachine.Status.InstanceID
		info, err := r.describeInstance(ctx, alibabaSDKClient, instanceID)
		if err != nil {
			return nil, err
		}
		if info == nil {
			// Instance disappeared — terminal: CAPI must remediate (replace it).
			return nil, newTerminalError("InstanceDisappeared",
				fmt.Sprintf("ECS instance %s no longer exists", instanceID))
		}

		// Self-heal spec.providerID. createInstance sets it on the create reconcile,
		// but if that spec write was ever lost — e.g. it raced a Machine/MachineSet
		// update of labels/ownerRefs on the same object — the field stays nil
		// forever. That silently breaks node join: the CSR approver's
		// hasPendingMachine gate skips machines with a nil ProviderID, so the
		// worker's bootstrap CSR is never auto-approved and it never becomes a Node.
		// Re-derive it from the known instance ID here so every reconcile converges.
		if alibabaCloudMachine.Spec.ProviderID == nil {
			pid := providerIDFor(resolveRegion(alibabaCloudMachine, alibabaCluster), instanceID)
			alibabaCloudMachine.Spec.ProviderID = &pid
		}

		// Always sync addresses and state into status regardless of current state.
		r.syncInstanceStatus(alibabaCloudMachine, info)

		switch info.State {
		case infrav1.InstanceStateRunning:
			log.Info("ECS instance is Running", "instanceID", instanceID)
			r.maybeHardenMetadata(ctx, alibabaSDKClient, machine, alibabaCloudMachine, instanceID)
			return info, nil
		case infrav1.InstanceStateDeleted, infrav1.InstanceStateStopped:
			return nil, newTerminalError("InstanceTerminalState",
				fmt.Sprintf("ECS instance %s is in terminal state %q", instanceID, info.State))
		default:
			// Pending / Starting / Stopping — keep waiting.
			log.Info("ECS instance not yet Running, requeueing", "instanceID", instanceID, "state", info.State)
			return nil, nil
		}
	}

	log.Info("Creating ECS instance", "machine", machine.Name)
	if err := r.createInstance(ctx, alibabaSDKClient, machine, alibabaCluster, alibabaCloudMachine); err != nil {
		return nil, err
	}
	// Instance created but not yet Running — requeue to poll status.
	log.Info("ECS instance created, waiting for Running state", "instanceID", *alibabaCloudMachine.Status.InstanceID)
	return nil, nil
}

// maybeHardenMetadata applies spec.metadataOptions.httpTokensAfterBoot to the
// live instance, but only AFTER the node has joined (the owning Machine has a
// nodeRef). A worker boots with tokenless IMDS because RHCOS Ignition fetches
// its user-data without an IMDSv2 token; flipping httpTokens to "required" before
// Ignition finishes would lock out that fetch and brick the boot. The nodeRef is
// the signal that Ignition is done, so this closes the IMDSv1 window safely.
// Best-effort and idempotent: Status.MetadataHardened gates it to one successful
// call; a failure is logged and retried on the next reconcile. No-op when the
// field is unset, already applied, or the node has not joined yet.
func (r *AlibabaCloudMachineReconciler) maybeHardenMetadata(
	ctx context.Context,
	c alibabaClient.Client,
	machine *clusterv1.Machine,
	m *infrav1.AlibabaCloudMachine,
	instanceID string,
) {
	log := ctrl.LoggerFrom(ctx)
	if m.Spec.MetadataOptions == nil || m.Spec.MetadataOptions.HttpTokensAfterBoot == "" {
		return
	}
	if m.Status.MetadataHardened != nil && *m.Status.MetadataHardened {
		return
	}
	// Gate on the node having joined — Ignition's tokenless IMDS fetch is then
	// guaranteed complete, so changing httpTokens cannot interrupt an in-flight boot.
	if machine == nil || machine.Status.NodeRef.Name == "" {
		log.Info("deferring IMDS hardening until the node joins (nodeRef)", "instanceID", instanceID)
		return
	}
	httpEndpoint, _, hopLimit := resolveMetadataOptions(m.Spec.MetadataOptions)
	if err := c.ModifyInstanceMetadata(instanceID, httpEndpoint, m.Spec.MetadataOptions.HttpTokensAfterBoot, hopLimit); err != nil {
		log.Error(err, "best-effort: failed to harden instance IMDS options (will retry)",
			"instanceID", instanceID, "httpTokens", m.Spec.MetadataOptions.HttpTokensAfterBoot)
		return
	}
	hardened := true
	m.Status.MetadataHardened = &hardened
	log.Info("hardened instance IMDS options after node join",
		"instanceID", instanceID, "httpTokens", m.Spec.MetadataOptions.HttpTokensAfterBoot)
}

// instanceInfo holds normalised data about an ECS instance.
type instanceInfo struct {
	InstanceID string
	State      infrav1.InstanceState
	PrivateIP  string
	PublicIP   string
}

func (r *AlibabaCloudMachineReconciler) describeInstance(ctx context.Context, c alibabaClient.Client, instanceID string) (*instanceInfo, error) {
	resp, err := c.DescribeInstanceByID(instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to describe instance %s: %w", instanceID, err)
	}
	if resp == nil {
		return nil, nil
	}
	return &instanceInfo{
		InstanceID: resp.InstanceID,
		State:      infrav1.InstanceState(resp.Status),
		PrivateIP:  firstIP(resp.InnerIpAddress.IpAddress),
		PublicIP:   firstIP(resp.PublicIpAddress.IpAddress),
	}, nil
}

func (r *AlibabaCloudMachineReconciler) createInstance(
	ctx context.Context,
	c alibabaClient.Client,
	machine *clusterv1.Machine,
	alibabaCluster *infrav1.AlibabaCloudCluster,
	alibabaCloudMachine *infrav1.AlibabaCloudMachine,
) error {
	userData, err := r.getUserData(ctx, machine, alibabaCloudMachine)
	if err != nil {
		return err
	}

	diskCategory := "cloud_efficiency"
	diskSize := 40
	diskPerfLevel := ""
	var diskEncrypted *bool
	diskKMSKeyID := ""
	if sd := alibabaCloudMachine.Spec.SystemDisk; sd != nil {
		diskCategory = sd.Category
		diskSize = sd.Size
		diskPerfLevel = sd.PerformanceLevel
		diskEncrypted = sd.Encrypted
		diskKMSKeyID = sd.KMSKeyID
	}

	// Resolve the boot image: an explicit machine imageID wins, otherwise fall
	// back to the cluster's bootImageID (the imported RHCOS/discovery image that
	// lets the node join THIS cluster). With neither set there is nothing to boot
	// — that's a terminal misconfiguration, so the MachineSet can remediate
	// rather than the controller hot-looping.
	imageID := alibabaCloudMachine.Spec.ImageID
	if imageID == "" {
		imageID = alibabaCluster.Spec.BootImageID
	}
	if imageID == "" {
		return newTerminalError("NoBootImage",
			"spec.imageID is empty and the cluster has no spec.bootImageID to fall back to")
	}

	// Region resolves to the cluster's region when the machine omits it.
	// Both RunInstances and the providerID must use this resolved value —
	// never Spec.RegionID directly (it is usually empty; see resolveRegion).
	region := resolveRegion(alibabaCloudMachine, alibabaCluster)

	// Persist the resolved region onto the spec so the region is recoverable from
	// the AlibabaCloudMachine ALONE on the delete path (regionFromMachine), even if
	// both Spec.ProviderID and Status.InstanceID writes are later lost. Without a
	// resolvable region the delete path cannot build a client to sweep the tagged
	// orphan and would drop the finalizer with a billable ECS still running (G8).
	// The webhook allows this one-time empty -> value write (immutableOnceSet).
	if alibabaCloudMachine.Spec.RegionID == "" {
		alibabaCloudMachine.Spec.RegionID = region
	}

	// Metadata service hardening: default to IMDSv2 (token required) unless the
	// spec opts out. Done here (not only in the webhook) so the secure baseline
	// holds even when admission webhooks are disabled.
	httpEndpoint, httpTokens, hopLimit := resolveMetadataOptions(alibabaCloudMachine.Spec.MetadataOptions)

	// Offload the Ignition to OSS if it exceeds the ECS UserData limit, replacing
	// the payload with a tiny pointer Ignition that fetches the full config from a
	// presigned URL. No-op when the user-data is within the limit.
	userData, err = r.maybeOffloadUserData(c, alibabaCluster, alibabaCloudMachine, region, userData)
	if err != nil {
		return err
	}

	resp, createErr := c.CreateECSInstance(alibabaClient.CreateInstanceParams{
		RegionID:                   region,
		ZoneID:                     alibabaCloudMachine.Spec.ZoneID,
		InstanceType:               alibabaCloudMachine.Spec.InstanceType,
		ImageID:                    imageID,
		SecurityGroupIDs:           alibabaCloudMachine.Spec.SecurityGroupIDs,
		VSwitchID:                  alibabaCloudMachine.Spec.VSwitchID,
		SystemDiskCategory:         diskCategory,
		SystemDiskSize:             diskSize,
		SystemDiskPerformanceLevel: diskPerfLevel,
		SystemDiskEncrypted:        diskEncrypted,
		SystemDiskKMSKeyID:         diskKMSKeyID,
		DataDisks:                  toSDKDataDisks(alibabaCloudMachine.Spec.DataDisks),
		RAMRoleName:                alibabaCloudMachine.Spec.RAMRoleName,
		UserData:                   userData,
		Tags:                       toSDKTags(alibabaCloudMachine.Spec.Tags, machine.Name, alibabaCluster.Name),
		ResourceGroupID:            alibabaCluster.Spec.ResourceGroupID,
		SpotStrategy:               alibabaCloudMachine.Spec.SpotStrategy,
		SpotPriceLimit:             alibabaCloudMachine.Spec.SpotPriceLimit,
		HttpEndpoint:               httpEndpoint,
		HttpTokens:                 httpTokens,
		HttpPutResponseHopLimit:    hopLimit,
	})
	if createErr != nil {
		return classifyCreateError(createErr)
	}

	instanceID := resp.InstanceID
	providerID := providerIDFor(region, instanceID)
	alibabaCloudMachine.Spec.ProviderID = &providerID
	alibabaCloudMachine.Status.InstanceID = &instanceID
	alibabaCloudMachine.Status.InstanceState = &infrav1.InstanceStatePending
	return nil
}

func (r *AlibabaCloudMachineReconciler) deleteInstance(ctx context.Context, c alibabaClient.Client, alibabaCloudMachine *infrav1.AlibabaCloudMachine) error {
	instanceID := *alibabaCloudMachine.Status.InstanceID
	log := ctrl.LoggerFrom(ctx)
	log.Info("Deleting ECS instance", "instanceID", instanceID)

	if err := c.DeleteECSInstance(instanceID, true); err != nil {
		return fmt.Errorf("failed to delete ECS instance %s: %w", instanceID, err)
	}
	log.Info("ECS instance deleted", "instanceID", instanceID)
	return nil
}

// terminalError marks a non-recoverable reconcile condition.  When
// reconcileNormal unwraps one (via errors.As) it records
// Status.FailureReason/FailureMessage — the CAPI terminal-failure contract —
// and stops requeueing, because retrying cannot make progress.  The owning
// Machine controller observes the failure and remediates (e.g. replaces the
// Machine).  Only genuinely unrecoverable conditions should use this; transient
// API/SDK errors must stay plain errors so they are retried with backoff.
type terminalError struct {
	reason  string
	message string
}

func (e *terminalError) Error() string { return e.message }

func newTerminalError(reason, message string) *terminalError {
	return &terminalError{reason: reason, message: message}
}

// alibabaErrorCode returns the Alibaba Cloud server-side error code carried by
// err, or "" when err is not an SDK ServerError.
func alibabaErrorCode(err error) string {
	var srvErr *sdkerrors.ServerError
	if errors.As(err, &srvErr) {
		return srvErr.ErrorCode()
	}
	return ""
}

// classifyCreateError maps RunInstances failures to the right error kind:
//
//   - Capacity exhaustion in the target zone and a rejected spot configuration
//     are NON-recoverable for this Machine — no amount of retrying makes a
//     sold-out zone have stock, or an unsupported spot strategy supported. We
//     surface them as terminal errors so reconcileNormal records
//     FailureReason/FailureMessage and stops requeueing; the owning MachineSet
//     then remediates (deletes the Machine and recreates it in another
//     FailureDomain / VSwitch).
//   - Everything else (throttling, transient API/network errors) stays a plain
//     wrapped error so controller-runtime retries it with exponential backoff.
//     Do NOT add a custom retry/sleep loop here — the manager already backs off.
func classifyCreateError(err error) error {
	switch alibabaErrorCode(err) {
	case "Invalid.Zone.NotEnoughResource", "InsufficientCapacity", "OperationDenied.NoStock":
		return newTerminalError("InstanceCapacityExhausted", "Instance capacity exhausted in current zone")
	case "Invalid.SpotStrategy.NotSupported", "Invalid.SpotPriceLimit.Exceeded":
		return newTerminalError("SpotConfigurationRejected",
			fmt.Sprintf("spot configuration rejected by Alibaba Cloud: %s", alibabaErrorCode(err)))
	default:
		return fmt.Errorf("failed to create ECS instance: %w", err)
	}
}

// setFailed records a terminal failure on the machine status per the CAPI
// contract: FailureReason (machine-readable) + FailureMessage (human-readable),
// Ready=false, and a matching Ready condition.
func (r *AlibabaCloudMachineReconciler) setFailed(m *infrav1.AlibabaCloudMachine, reason, message string) {
	m.Status.FailureReason = &reason
	m.Status.FailureMessage = &message
	m.Status.Ready = false
	conditions.Set(m, metav1.Condition{
		Type:    clusterv1.ReadyCondition,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// ptrStr safely dereferences a *string for logging.
func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (r *AlibabaCloudMachineReconciler) syncInstanceStatus(m *infrav1.AlibabaCloudMachine, info *instanceInfo) *instanceInfo {
	m.Status.InstanceID = &info.InstanceID
	m.Status.InstanceState = &info.State
	addrs := []clusterv1.MachineAddress{}
	if info.PrivateIP != "" {
		addrs = append(addrs, clusterv1.MachineAddress{
			Type:    clusterv1.MachineInternalIP,
			Address: info.PrivateIP,
		})
	}
	if info.PublicIP != "" {
		addrs = append(addrs, clusterv1.MachineAddress{
			Type:    clusterv1.MachineExternalIP,
			Address: info.PublicIP,
		})
	}
	m.Status.Addresses = addrs
	return info
}

// getUserData resolves the cloud-init / Ignition payload for the instance.
// CAPI convention: a bootstrap provider writes the data to a Secret and sets
// the name on the owning Machine's Spec.Bootstrap.DataSecretName.  We read
// that first, falling back to the (legacy, machine-api-style)
// AlibabaCloudMachine.Spec.UserDataSecret for backward compatibility.
// The Secret stores the payload under the "value" key in both conventions.
func (r *AlibabaCloudMachineReconciler) getUserData(
	ctx context.Context,
	machine *clusterv1.Machine,
	m *infrav1.AlibabaCloudMachine,
) (string, error) {
	var secretName string
	switch {
	case machine.Spec.Bootstrap.DataSecretName != nil && *machine.Spec.Bootstrap.DataSecretName != "":
		secretName = *machine.Spec.Bootstrap.DataSecretName
	case m.Spec.UserDataSecret != nil:
		secretName = m.Spec.UserDataSecret.Name
	default:
		return "", nil
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: m.Namespace,
		Name:      secretName,
	}, secret); err != nil {
		return "", fmt.Errorf("failed to get bootstrap/user-data secret %q: %w", secretName, err)
	}
	raw, ok := secret.Data["value"]
	if !ok {
		return "", fmt.Errorf("bootstrap/user-data secret %q has no 'value' key", secretName)
	}
	// Secret.Data is already raw bytes (k8s base64-decodes it on retrieval).
	// ECS RunInstances expects a base64-encoded string.
	return base64.StdEncoding.EncodeToString(raw), nil
}

// defaultMaxUserDataBytes is the ECS RunInstances UserData size limit. Beyond
// this the API rejects the request, so the controller offloads to OSS.
const defaultMaxUserDataBytes = 16384

// maybeOffloadUserData returns userData unchanged when it is within the ECS
// UserData limit. When it exceeds the limit it offloads the full Ignition to OSS
// and returns a base64 pointer Ignition (ignition.config.replace with a presigned
// URL + sha512 verification). It records the OSS object on the machine status so
// reconcileDelete can clean it up. When offload is needed but no OSS bucket is
// configured, it returns a terminal error so the MachineSet remediates rather
// than the controller hot-looping on a request the API will always reject.
func (r *AlibabaCloudMachineReconciler) maybeOffloadUserData(
	c alibabaClient.Client,
	cluster *infrav1.AlibabaCloudCluster,
	m *infrav1.AlibabaCloudMachine,
	region, userData string,
) (string, error) {
	store := cluster.Spec.IgnitionStorage
	threshold := defaultMaxUserDataBytes
	if store != nil && store.MaxUserDataBytes > 0 {
		threshold = store.MaxUserDataBytes
	}
	if len(userData) <= threshold {
		return userData, nil
	}

	if store == nil || store.OSSBucket == "" {
		return "", newTerminalError("UserDataTooLarge", fmt.Sprintf(
			"user-data is %d bytes (> %d) and spec.ignitionStorage.ossBucket is not set to offload it to OSS",
			len(userData), threshold))
	}

	// userData is base64; OSS stores the raw Ignition JSON.
	raw, err := base64.StdEncoding.DecodeString(userData)
	if err != nil {
		return "", fmt.Errorf("decode user-data for OSS offload: %w", err)
	}

	key := ignitionObjectKey(cluster, m)
	expiry := time.Duration(store.ExpirySeconds) * time.Second
	url, err := c.PutIgnitionObject(alibabaClient.IgnitionStoreParams{
		Bucket:   store.OSSBucket,
		Endpoint: store.OSSEndpoint,
		RegionID: region,
		Key:      key,
		Data:     raw,
		Expiry:   expiry,
	})
	if err != nil {
		return "", fmt.Errorf("offload user-data to OSS: %w", err)
	}

	pointer, err := buildIgnitionPointer(url, raw)
	if err != nil {
		return "", err
	}

	m.Status.IgnitionOSS = &infrav1.IgnitionOSSRef{
		Bucket:   store.OSSBucket,
		Endpoint: store.OSSEndpoint,
		Key:      key,
	}
	return base64.StdEncoding.EncodeToString(pointer), nil
}

// ignitionObjectKey is the deterministic OSS key for a machine's offloaded
// Ignition. Deterministic so retries overwrite rather than orphan.
func ignitionObjectKey(cluster *infrav1.AlibabaCloudCluster, m *infrav1.AlibabaCloudMachine) string {
	return fmt.Sprintf("capi-ignition/%s/%s/%s.ign", cluster.Name, m.Namespace, m.Name)
}

// buildIgnitionPointer builds a minimal Ignition config that replaces itself
// with the full config fetched from url, pinned to the sha512 of raw so a
// truncated or tampered fetch fails closed.
func buildIgnitionPointer(url string, raw []byte) ([]byte, error) {
	sum := sha512.Sum512(raw)
	hash := "sha512-" + hex.EncodeToString(sum[:])
	cfg := map[string]any{
		"ignition": map[string]any{
			"version": "3.4.0",
			"config": map[string]any{
				"replace": map[string]any{
					"source":       url,
					"verification": map[string]any{"hash": hash},
				},
			},
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal pointer ignition: %w", err)
	}
	return b, nil
}

// resolveFailureDomain fills in ZoneID and VSwitchID on the machine from the
// CAPI-assigned failure domain when they are not explicitly set in the spec.
// CAPI sets machine.Spec.FailureDomain to one of the zone IDs published in
// AlibabaCloudCluster.Status.FailureDomains (populated by the cluster controller).
func (r *AlibabaCloudMachineReconciler) resolveFailureDomain(
	machine *clusterv1.Machine,
	alibabaCluster *infrav1.AlibabaCloudCluster,
	alibabaCloudMachine *infrav1.AlibabaCloudMachine,
) error {
	if machine.Spec.FailureDomain == "" {
		return nil
	}
	zoneID := machine.Spec.FailureDomain

	// Locate the matching FailureDomain entry (slice in v1beta2, keyed by Name = ZoneID).
	var fd *clusterv1.FailureDomain
	for i := range alibabaCluster.Status.FailureDomains {
		if alibabaCluster.Status.FailureDomains[i].Name == zoneID {
			fd = &alibabaCluster.Status.FailureDomains[i]
			break
		}
	}
	if fd == nil {
		return fmt.Errorf("failure domain %q not found in AlibabaCloudCluster status", zoneID)
	}

	// Only override if not explicitly set in the machine spec.
	if alibabaCloudMachine.Spec.ZoneID == "" {
		alibabaCloudMachine.Spec.ZoneID = zoneID
	}
	if alibabaCloudMachine.Spec.VSwitchID == "" {
		vSwitchID, ok := fd.Attributes["vSwitchID"]
		if !ok || vSwitchID == "" {
			return fmt.Errorf("failure domain %q has no vSwitchID attribute", zoneID)
		}
		alibabaCloudMachine.Spec.VSwitchID = vSwitchID
	}
	return nil
}

func firstIP(ips []string) string {
	if len(ips) > 0 {
		return ips[0]
	}
	return ""
}

func toSDKTags(tags []infrav1.Tag, machineName, clusterName string) []alibabaClient.Tag {
	result := []alibabaClient.Tag{
		{Key: "kubernetes.io/cluster/" + clusterName, Value: "owned"},
		{Key: "k8s.io/cluster-api-machine", Value: machineName},
	}
	for _, t := range tags {
		result = append(result, alibabaClient.Tag{Key: t.Key, Value: t.Value})
	}
	return result
}

// toSDKDataDisks maps the CRD data-disk specs to the client param type.
func toSDKDataDisks(disks []infrav1.DataDisk) []alibabaClient.DataDiskParam {
	if len(disks) == 0 {
		return nil
	}
	out := make([]alibabaClient.DataDiskParam, len(disks))
	for i, d := range disks {
		out[i] = alibabaClient.DataDiskParam{
			Category:         d.Category,
			Size:             d.Size,
			PerformanceLevel: d.PerformanceLevel,
			Encrypted:        d.Encrypted,
			KMSKeyID:         d.KMSKeyID,
		}
	}
	return out
}
