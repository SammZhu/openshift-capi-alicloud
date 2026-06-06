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
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"

	infrav1 "github.com/SammZhu/openshift-capi-alicloud/api/v1beta1"
)

// aggregateErrors turns a field.ErrorList into the apierrors.StatusError the API
// server renders as a 422 Invalid with per-field causes. Returns nil when the
// list is empty so callers can `return nil, aggregateErrors(...)` unconditionally.
func aggregateErrors(gk schema.GroupKind, name string, allErrs field.ErrorList) error {
	if len(allErrs) == 0 {
		return nil
	}
	return apierrors.NewInvalid(gk, name, allErrs)
}

// validateTags enforces that every tag key is non-empty and unique within the
// slice. Duplicate keys are a silent footgun: Alibaba Cloud keeps only the last
// value, so the user's intent is quietly dropped.
func validateTags(tags []infrav1.Tag, parent *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	seen := make(map[string]struct{}, len(tags))
	for i, t := range tags {
		keyPath := parent.Index(i).Child("key")
		if strings.TrimSpace(t.Key) == "" {
			allErrs = append(allErrs, field.Required(keyPath, "tag key must not be empty"))
			continue
		}
		if _, dup := seen[t.Key]; dup {
			allErrs = append(allErrs, field.Duplicate(keyPath, t.Key))
		}
		seen[t.Key] = struct{}{}
	}
	return allErrs
}
