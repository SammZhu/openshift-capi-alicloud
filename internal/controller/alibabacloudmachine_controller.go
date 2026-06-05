package controller

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

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

	if !alibabaCloudMachine.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, alibabaCloudMachine)
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
	log.Info("AlibabaCloudMachine reconciled successfully", "instanceID", alibabaCloudMachine.Status.InstanceID)
	return ctrl.Result{}, nil
}

func (r *AlibabaCloudMachineReconciler) reconcileDelete(ctx context.Context, alibabaCloudMachine *infrav1.AlibabaCloudMachine) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling AlibabaCloudMachine delete")

	// Nothing was ever provisioned — drop the finalizer immediately.
	if alibabaCloudMachine.Status.InstanceID == nil {
		controllerutil.RemoveFinalizer(alibabaCloudMachine, infrav1.MachineFinalizer)
		return ctrl.Result{}, nil
	}

	region, err := regionFromMachine(alibabaCloudMachine)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("cannot determine region for deletion: %w", err)
	}
	alibabaSDKClient, err := r.AlibabaCloudClientBuilder(r.Client, region)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create Alibaba Cloud client: %w", err)
	}

	instanceID := *alibabaCloudMachine.Status.InstanceID

	// CAPI contract: the infrastructure must be actually gone before we drop the
	// finalizer, otherwise the owning Machine (and Node) is reported deleted
	// while a billable ECS instance lives on.  Poll DescribeInstance and only
	// release the finalizer once the instance no longer exists.
	info, err := r.describeInstance(ctx, alibabaSDKClient, instanceID)
	if err != nil {
		return ctrl.Result{}, err
	}
	if info == nil {
		log.Info("ECS instance is gone, removing finalizer", "instanceID", instanceID)
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

	// Issue the (force) delete only while the instance is not already on its way
	// down; re-issuing against a Stopping/Stopped instance just generates
	// IncorrectInstanceStatus noise.  Then requeue to poll until it disappears.
	switch info.State {
	case infrav1.InstanceStateStopping, infrav1.InstanceStateStopped, infrav1.InstanceStateDeleted:
		log.Info("ECS instance is terminating, waiting", "instanceID", instanceID, "state", info.State)
	default:
		if err := r.deleteInstance(ctx, alibabaSDKClient, alibabaCloudMachine); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
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

// providerIDFor builds a CAPI-conformant providerID.  The Cluster API
// convention is alicloud://<region>/<instanceID> (slash-separated).  An
// earlier build used a dot separator AND Spec.RegionID (often empty),
// producing "alicloud://.i-abc" — which regionFromMachine could not parse,
// so deletion failed with "cannot determine region" and the finalizer hung.
func providerIDFor(region, instanceID string) string {
	return fmt.Sprintf("alicloud://%s/%s", region, instanceID)
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

		// Always sync addresses and state into status regardless of current state.
		r.syncInstanceStatus(alibabaCloudMachine, info)

		switch info.State {
		case infrav1.InstanceStateRunning:
			log.Info("ECS instance is Running", "instanceID", instanceID)
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
	if alibabaCloudMachine.Spec.SystemDisk != nil {
		diskCategory = alibabaCloudMachine.Spec.SystemDisk.Category
		diskSize = alibabaCloudMachine.Spec.SystemDisk.Size
	}

	// Region resolves to the cluster's region when the machine omits it.
	// Both RunInstances and the providerID must use this resolved value —
	// never Spec.RegionID directly (it is usually empty; see resolveRegion).
	region := resolveRegion(alibabaCloudMachine, alibabaCluster)

	resp, err := c.CreateECSInstance(alibabaClient.CreateInstanceParams{
		RegionID:           region,
		ZoneID:             alibabaCloudMachine.Spec.ZoneID,
		InstanceType:       alibabaCloudMachine.Spec.InstanceType,
		ImageID:            alibabaCloudMachine.Spec.ImageID,
		SecurityGroupIDs:   alibabaCloudMachine.Spec.SecurityGroupIDs,
		VSwitchID:          alibabaCloudMachine.Spec.VSwitchID,
		SystemDiskCategory: diskCategory,
		SystemDiskSize:     diskSize,
		RAMRoleName:        alibabaCloudMachine.Spec.RAMRoleName,
		UserData:           userData,
		Tags:               toSDKTags(alibabaCloudMachine.Spec.Tags, machine.Name, alibabaCluster.Name),
		ResourceGroupID:    alibabaCluster.Spec.ResourceGroupID,
	})
	if err != nil {
		return fmt.Errorf("failed to create ECS instance: %w", err)
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
