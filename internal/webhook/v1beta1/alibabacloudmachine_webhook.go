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

// Package webhook holds the admission webhooks (defaulting + validation) for
// the AlibabaCloud* infrastructure resources.  Keeping webhook logic out of the
// api/ package follows the controller-runtime CustomValidator/CustomDefaulter
// pattern: the API types stay free of business logic and the webhook code can be
// unit-tested in isolation.
package webhook

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	infrav1 "github.com/SammZhu/openshift-capi-alicloud/api/v1beta1"
)

const (
	// defaultSystemDiskCategory is the disk category applied when a machine
	// omits spec.systemDisk. cloud_essd is the modern general-purpose ESSD tier.
	defaultSystemDiskCategory = "cloud_essd"
	// defaultSystemDiskSizeGiB is the system disk size applied when omitted. It
	// matches the CRD minimum-comfortable default used across the repo's smoke
	// manifests.
	defaultSystemDiskSizeGiB = 40
	// minDiskSizeGiB is the smallest disk Alibaba Cloud accepts for the disk
	// categories we expose (mirrors the CRD +kubebuilder:validation:Minimum).
	minDiskSizeGiB = 20
	// ecsInstanceTypePrefix is the mandatory prefix of every Alibaba Cloud ECS
	// instance type, e.g. ecs.g7.large.
	ecsInstanceTypePrefix = "ecs."
)

var machineGroupKind = schema.GroupKind{
	Group: infrav1.GroupVersion.Group,
	Kind:  "AlibabaCloudMachine",
}

// +kubebuilder:webhook:path=/mutate-infrastructure-cluster-x-k8s-io-v1beta1-alibabacloudmachine,mutating=true,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=alibabacloudmachines,verbs=create;update,versions=v1beta1,name=default.alibabacloudmachine.infrastructure.cluster.x-k8s.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate-infrastructure-cluster-x-k8s-io-v1beta1-alibabacloudmachine,mutating=false,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=alibabacloudmachines,verbs=create;update,versions=v1beta1,name=validation.alibabacloudmachine.infrastructure.cluster.x-k8s.io,admissionReviewVersions=v1

// AlibabaCloudMachineWebhook implements defaulting and validation for
// AlibabaCloudMachine.
type AlibabaCloudMachineWebhook struct{}

// SetupWebhookWithManager registers the defaulting + validating webhooks.
func (*AlibabaCloudMachineWebhook) SetupWebhookWithManager(mgr ctrl.Manager) error {
	w := &AlibabaCloudMachineWebhook{}
	return ctrl.NewWebhookManagedBy(mgr).
		For(&infrav1.AlibabaCloudMachine{}).
		WithDefaulter(w).
		WithValidator(w).
		Complete()
}

var (
	_ webhook.CustomDefaulter = &AlibabaCloudMachineWebhook{}
	_ webhook.CustomValidator = &AlibabaCloudMachineWebhook{}
)

// Default applies defaulting to an AlibabaCloudMachine.
func (*AlibabaCloudMachineWebhook) Default(_ context.Context, obj runtime.Object) error {
	m, ok := obj.(*infrav1.AlibabaCloudMachine)
	if !ok {
		return fmt.Errorf("expected an AlibabaCloudMachine but got a %T", obj)
	}

	if m.Spec.SystemDisk == nil {
		m.Spec.SystemDisk = &infrav1.SystemDisk{
			Category: defaultSystemDiskCategory,
			Size:     defaultSystemDiskSizeGiB,
		}
		return nil
	}
	if m.Spec.SystemDisk.Category == "" {
		m.Spec.SystemDisk.Category = defaultSystemDiskCategory
	}
	if m.Spec.SystemDisk.Size == 0 {
		m.Spec.SystemDisk.Size = defaultSystemDiskSizeGiB
	}
	return nil
}

// ValidateCreate validates an AlibabaCloudMachine on creation.
func (*AlibabaCloudMachineWebhook) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	m, ok := obj.(*infrav1.AlibabaCloudMachine)
	if !ok {
		return nil, fmt.Errorf("expected an AlibabaCloudMachine but got a %T", obj)
	}
	return nil, aggregateErrors(machineGroupKind, m.Name, validateMachineSpec(m))
}

// ValidateUpdate validates an AlibabaCloudMachine on update, enforcing both the
// spec rules and the immutability of fields that define the provisioned ECS.
func (*AlibabaCloudMachineWebhook) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	newM, ok := newObj.(*infrav1.AlibabaCloudMachine)
	if !ok {
		return nil, fmt.Errorf("expected an AlibabaCloudMachine but got a %T", newObj)
	}
	oldM, ok := oldObj.(*infrav1.AlibabaCloudMachine)
	if !ok {
		return nil, fmt.Errorf("expected an AlibabaCloudMachine but got a %T", oldObj)
	}
	allErrs := validateMachineSpec(newM)
	allErrs = append(allErrs, validateMachineImmutable(oldM, newM)...)
	return nil, aggregateErrors(machineGroupKind, newM.Name, allErrs)
}

// ValidateDelete is a no-op; deletion is always allowed.
func (*AlibabaCloudMachineWebhook) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func validateMachineSpec(m *infrav1.AlibabaCloudMachine) field.ErrorList {
	var allErrs field.ErrorList
	spec := field.NewPath("spec")

	switch {
	case strings.TrimSpace(m.Spec.InstanceType) == "":
		allErrs = append(allErrs, field.Required(spec.Child("instanceType"), "instanceType is required"))
	case !strings.HasPrefix(m.Spec.InstanceType, ecsInstanceTypePrefix):
		allErrs = append(allErrs, field.Invalid(spec.Child("instanceType"), m.Spec.InstanceType,
			"must be an Alibaba Cloud ECS instance type (e.g. ecs.g7.large)"))
	}

	// imageID is intentionally NOT required here: it may be resolved at reconcile
	// time from the owning cluster's spec.bootImageID, which the webhook (seeing
	// only the machine) cannot observe. The controller raises a terminal
	// NoBootImage error when neither is set.

	if d := m.Spec.SystemDisk; d != nil && d.Size != 0 && d.Size < minDiskSizeGiB {
		allErrs = append(allErrs, field.Invalid(spec.Child("systemDisk", "size"), d.Size,
			fmt.Sprintf("must be at least %d GiB", minDiskSizeGiB)))
	}

	for i, d := range m.Spec.DataDisks {
		if d.Size < minDiskSizeGiB {
			allErrs = append(allErrs, field.Invalid(spec.Child("dataDisks").Index(i).Child("size"), d.Size,
				fmt.Sprintf("must be at least %d GiB", minDiskSizeGiB)))
		}
	}

	if d := m.Spec.SystemDisk; d != nil {
		allErrs = append(allErrs, validateDiskExtras(d.Category, d.PerformanceLevel, d.Encrypted, d.KMSKeyID, spec.Child("systemDisk"))...)
	}
	for i, d := range m.Spec.DataDisks {
		allErrs = append(allErrs, validateDiskExtras(d.Category, d.PerformanceLevel, d.Encrypted, d.KMSKeyID, spec.Child("dataDisks").Index(i))...)
	}

	allErrs = append(allErrs, validateSpot(m, spec)...)
	allErrs = append(allErrs, validateTags(m.Spec.Tags, spec.Child("tags"))...)
	return allErrs
}

// validateSpot enforces the spotStrategy/spotPriceLimit relationship. Unknown
// spotStrategy values are already rejected by the CRD enum.
func validateSpot(m *infrav1.AlibabaCloudMachine, spec *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	plPath := spec.Child("spotPriceLimit")
	if m.Spec.SpotStrategy == "SpotWithPriceLimit" {
		switch {
		case m.Spec.SpotPriceLimit == nil:
			allErrs = append(allErrs, field.Required(plPath, "spotPriceLimit is required when spotStrategy is SpotWithPriceLimit"))
		case *m.Spec.SpotPriceLimit <= 0:
			allErrs = append(allErrs, field.Invalid(plPath, *m.Spec.SpotPriceLimit, "must be greater than 0"))
		}
		return allErrs
	}
	// Any non-SpotWithPriceLimit strategy (incl. empty/NoSpot/SpotAsPriceGo) must
	// not carry a price limit — it would be silently ignored.
	if m.Spec.SpotPriceLimit != nil {
		allErrs = append(allErrs, field.Invalid(plPath, *m.Spec.SpotPriceLimit,
			"spotPriceLimit is only allowed when spotStrategy is SpotWithPriceLimit"))
	}
	return allErrs
}

// validateDiskExtras checks the encryption + performance-level rules shared by
// system and data disks.
func validateDiskExtras(category, perfLevel string, encrypted *bool, kmsKeyID string, path *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if kmsKeyID != "" && (encrypted == nil || !*encrypted) {
		allErrs = append(allErrs, field.Invalid(path.Child("kmsKeyID"), kmsKeyID,
			"kmsKeyID requires encrypted=true"))
	}
	if perfLevel != "" && category != "cloud_essd" {
		allErrs = append(allErrs, field.Invalid(path.Child("performanceLevel"), perfLevel,
			"performanceLevel is only valid for the cloud_essd category"))
	}
	return allErrs
}

func validateMachineImmutable(oldM, newM *infrav1.AlibabaCloudMachine) field.ErrorList {
	var allErrs field.ErrorList
	spec := field.NewPath("spec")

	immutable := func(child *field.Path, oldVal, newVal interface{}) {
		if !reflect.DeepEqual(oldVal, newVal) {
			allErrs = append(allErrs, field.Forbidden(child, "field is immutable"))
		}
	}
	// immutableOnceSet allows the controller's one-time empty -> value write but
	// forbids changing or clearing an already-set value. zoneID/vSwitchID are
	// LEFT EMPTY in the MachineDeployment template and resolved at reconcile time
	// from the CAPI failure domain (resolveFailureDomain). A strict immutable()
	// here rejected that "" -> value spec write — and because the controller
	// patches spec atomically, it also blocked providerID from ever persisting,
	// so the CSR approver never saw a providerID and workers never joined.
	immutableOnceSet := func(child *field.Path, oldVal, newVal string) {
		if oldVal != "" && oldVal != newVal {
			allErrs = append(allErrs, field.Forbidden(child, "field is immutable once set"))
		}
	}
	immutable(spec.Child("instanceType"), oldM.Spec.InstanceType, newM.Spec.InstanceType)
	immutable(spec.Child("imageID"), oldM.Spec.ImageID, newM.Spec.ImageID)
	immutable(spec.Child("regionID"), oldM.Spec.RegionID, newM.Spec.RegionID)
	immutableOnceSet(spec.Child("zoneID"), oldM.Spec.ZoneID, newM.Spec.ZoneID)
	immutableOnceSet(spec.Child("vSwitchID"), oldM.Spec.VSwitchID, newM.Spec.VSwitchID)
	immutable(spec.Child("systemDisk"), oldM.Spec.SystemDisk, newM.Spec.SystemDisk)
	immutable(spec.Child("dataDisks"), oldM.Spec.DataDisks, newM.Spec.DataDisks)
	immutable(spec.Child("spotStrategy"), oldM.Spec.SpotStrategy, newM.Spec.SpotStrategy)
	immutable(spec.Child("spotPriceLimit"), oldM.Spec.SpotPriceLimit, newM.Spec.SpotPriceLimit)

	// providerID is populated by the controller (nil -> value). Allow that
	// one-time transition but forbid mutating or clearing an already-set value.
	if oldM.Spec.ProviderID != nil {
		if newM.Spec.ProviderID == nil || *newM.Spec.ProviderID != *oldM.Spec.ProviderID {
			allErrs = append(allErrs, field.Forbidden(spec.Child("providerID"), "providerID is immutable once set"))
		}
	}
	return allErrs
}
