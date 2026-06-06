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

package webhook

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	infrav1 "github.com/SammZhu/openshift-capi-alicloud/api/v1beta1"
)

var clusterGroupKind = schema.GroupKind{
	Group: infrav1.GroupVersion.Group,
	Kind:  "AlibabaCloudCluster",
}

// +kubebuilder:webhook:path=/validate-infrastructure-cluster-x-k8s-io-v1beta1-alibabacloudcluster,mutating=false,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=alibabacloudclusters,verbs=create;update,versions=v1beta1,name=validation.alibabacloudcluster.infrastructure.cluster.x-k8s.io,admissionReviewVersions=v1

// AlibabaCloudClusterWebhook implements validation for AlibabaCloudCluster.
// No defaulting is needed: every optional field has a sensible zero value and
// region is mandatory.
type AlibabaCloudClusterWebhook struct{}

// SetupWebhookWithManager registers the validating webhook.
func (*AlibabaCloudClusterWebhook) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&infrav1.AlibabaCloudCluster{}).
		WithValidator(&AlibabaCloudClusterWebhook{}).
		Complete()
}

var _ webhook.CustomValidator = &AlibabaCloudClusterWebhook{}

// ValidateCreate validates an AlibabaCloudCluster on creation.
func (*AlibabaCloudClusterWebhook) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	c, ok := obj.(*infrav1.AlibabaCloudCluster)
	if !ok {
		return nil, fmt.Errorf("expected an AlibabaCloudCluster but got a %T", obj)
	}
	return nil, aggregateErrors(clusterGroupKind, c.Name, validateClusterSpec(c))
}

// ValidateUpdate validates an AlibabaCloudCluster on update and enforces that
// the region cannot change once set (it pins every cloud resource's location).
func (*AlibabaCloudClusterWebhook) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	newC, ok := newObj.(*infrav1.AlibabaCloudCluster)
	if !ok {
		return nil, fmt.Errorf("expected an AlibabaCloudCluster but got a %T", newObj)
	}
	oldC, ok := oldObj.(*infrav1.AlibabaCloudCluster)
	if !ok {
		return nil, fmt.Errorf("expected an AlibabaCloudCluster but got a %T", oldObj)
	}
	allErrs := validateClusterSpec(newC)
	if oldC.Spec.Region != newC.Spec.Region {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "region"), "region is immutable"))
	}
	return nil, aggregateErrors(clusterGroupKind, newC.Name, allErrs)
}

// ValidateDelete is a no-op; deletion is always allowed.
func (*AlibabaCloudClusterWebhook) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func validateClusterSpec(c *infrav1.AlibabaCloudCluster) field.ErrorList {
	var allErrs field.ErrorList
	spec := field.NewPath("spec")

	if strings.TrimSpace(c.Spec.Region) == "" {
		allErrs = append(allErrs, field.Required(spec.Child("region"), "region is required"))
	}

	// controlPlaneEndpoint is optional (the controller fills it in for managed
	// clusters), but a BYO endpoint must be internally consistent: a host with
	// no port, or a port with no host, is always a mistake.
	ep := c.Spec.ControlPlaneEndpoint
	epPath := spec.Child("controlPlaneEndpoint")
	if ep.Host != "" || ep.Port != 0 {
		if ep.Host == "" {
			allErrs = append(allErrs, field.Required(epPath.Child("host"), "host is required when port is set"))
		}
		if ep.Port <= 0 || ep.Port > 65535 {
			allErrs = append(allErrs, field.Invalid(epPath.Child("port"), ep.Port, "must be between 1 and 65535"))
		}
	}

	// failureDomains: each zone must be named uniquely (duplicate zones break
	// CAPI's round-robin placement) and carry a VSwitch.
	seen := make(map[string]struct{}, len(c.Spec.FailureDomains))
	for i, fd := range c.Spec.FailureDomains {
		fdPath := spec.Child("failureDomains").Index(i)
		if strings.TrimSpace(fd.ZoneID) == "" {
			allErrs = append(allErrs, field.Required(fdPath.Child("zoneID"), "zoneID is required"))
		} else if _, dup := seen[fd.ZoneID]; dup {
			allErrs = append(allErrs, field.Duplicate(fdPath.Child("zoneID"), fd.ZoneID))
		} else {
			seen[fd.ZoneID] = struct{}{}
		}
		if strings.TrimSpace(fd.VSwitchID) == "" {
			allErrs = append(allErrs, field.Required(fdPath.Child("vSwitchID"), "vSwitchID is required"))
		}
	}
	return allErrs
}
