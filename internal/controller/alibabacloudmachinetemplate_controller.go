package controller

import (
	"context"
	"sync"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	infrav1 "github.com/SammZhu/openshift-capi-alicloud/api/v1beta1"
	alibabaClient "github.com/SammZhu/openshift-capi-alicloud/pkg/client"
)

// defaultMaxPodsPerNode is kubelet's default --max-pods, advertised as the node's
// pod capacity for scale-from-zero scheduling simulation.
const defaultMaxPodsPerNode = 110

// AlibabaCloudMachineTemplateReconciler populates AlibabaCloudMachineTemplate
// status.capacity (cpu / memory / pods / ephemeral-storage) from the template's
// instanceType. Cluster Autoscaler's clusterapi provider reads status.capacity to
// size a worker pool when scaling UP FROM ZERO (the CAPI scale-from-zero contract)
// — there is no live Node to copy from then. Instance-type specs are static, so
// the (rate-limited) DescribeInstanceTypes result is cached per instanceType.
type AlibabaCloudMachineTemplateReconciler struct {
	client.Client
	Scheme                    *runtime.Scheme
	Log                       logr.Logger
	AlibabaCloudClientBuilder alibabaClient.ClientBuilderFunc

	mu    sync.Mutex
	cache map[string]corev1.ResourceList // instanceType -> {cpu, memory}
}

func (r *AlibabaCloudMachineTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	tmpl := &infrav1.AlibabaCloudMachineTemplate{}
	if err := r.Get(ctx, req.NamespacedName, tmpl); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	spec := tmpl.Spec.Template.Spec
	if spec.InstanceType == "" {
		return ctrl.Result{}, nil // nothing to resolve
	}
	if spec.RegionID == "" {
		// Without a region we can't build a cloud client. The worker template sets
		// regionID; surface this instead of looping.
		log.Info("template has no spec.template.spec.regionID; cannot resolve scale-from-zero capacity")
		return ctrl.Result{}, nil
	}

	capacity, err := r.resolveCapacity(spec)
	if err != nil {
		log.Error(err, "failed to resolve instance type capacity", "instanceType", spec.InstanceType)
		return ctrl.Result{}, err // requeue with backoff
	}

	if equality.Semantic.DeepEqual(tmpl.Status.Capacity, capacity) {
		return ctrl.Result{}, nil
	}
	patch := client.MergeFrom(tmpl.DeepCopy())
	tmpl.Status.Capacity = capacity
	if err := r.Status().Patch(ctx, tmpl, patch); err != nil {
		return ctrl.Result{}, err
	}
	log.Info("set AlibabaCloudMachineTemplate status.capacity (scale-from-zero)",
		"instanceType", spec.InstanceType, "capacity", capacity)
	return ctrl.Result{}, nil
}

// resolveCapacity returns the node capacity for a template's instanceType, caching
// the static cpu/memory and adding the per-template pods + ephemeral-storage.
func (r *AlibabaCloudMachineTemplateReconciler) resolveCapacity(spec infrav1.AlibabaCloudMachineSpec) (corev1.ResourceList, error) {
	r.mu.Lock()
	base, ok := r.cache[spec.InstanceType]
	r.mu.Unlock()

	if !ok {
		cli, err := r.AlibabaCloudClientBuilder(r.Client, spec.RegionID)
		if err != nil {
			return nil, err
		}
		vcpu, memMiB, err := cli.InstanceTypeCapacity(spec.InstanceType)
		if err != nil {
			return nil, err
		}
		base = corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewQuantity(vcpu, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(memMiB*1024*1024, resource.BinarySI),
		}
		r.mu.Lock()
		if r.cache == nil {
			r.cache = map[string]corev1.ResourceList{}
		}
		r.cache[spec.InstanceType] = base
		r.mu.Unlock()
	}

	out := base.DeepCopy()
	out[corev1.ResourcePods] = *resource.NewQuantity(defaultMaxPodsPerNode, resource.DecimalSI)
	if spec.SystemDisk != nil && spec.SystemDisk.Size > 0 {
		out[corev1.ResourceEphemeralStorage] = *resource.NewQuantity(
			int64(spec.SystemDisk.Size)*1024*1024*1024, resource.BinarySI)
	}
	return out, nil
}

func (r *AlibabaCloudMachineTemplateReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, options controller.Options) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.AlibabaCloudMachineTemplate{}).
		WithOptions(options).
		Complete(r)
}
