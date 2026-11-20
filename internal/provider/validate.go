// Copyright (C) 2026 The OpenEverest Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package provider

import (
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/definition/topologies/standalone"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

// statefulSetNameMaxLength keeps the StatefulSet pod label
// controller-revision-hash ("<statefulset>-<hash>", hash up to 10 chars)
// within the 63-character label value limit.
const statefulSetNameMaxLength = 52

// statefulSetSuffix is the longest suffix seaweedfs-operator appends for a
// StatefulSet ("<name>-volume", "<name>-master"). Service names such as
// "<name>-volume-peer" and volume pod names stay under 63 at this length too.
const statefulSetSuffix = "-volume"

// maxInstanceNameLength is 45.
const maxInstanceNameLength = statefulSetNameMaxLength - len(statefulSetSuffix)

var storageClassGVK = schema.GroupVersionKind{
	Group:   "storage.k8s.io",
	Version: "v1",
	Kind:    "StorageClass",
}

var componentsPath = field.NewPath("spec", "components")

var topologyParametersPath = field.NewPath("spec", "topology", "parameters")

// invalid wraps a rule's error as a field error on path.
func invalid(path *field.Path, err error) *field.Error {
	return field.Invalid(path, field.OmitValueType{}, err.Error())
}

// Validate checks the rules about the Instance alone and returns every error
// with its field path. Rules that read the cluster are in ValidateReferences.
func (p *Provider) Validate(in controller.ValidationInput) controller.ValidationResult {
	return controller.ValidationResult{
		Errors:   validateInstance(in),
		Warnings: metricsWarnings(in.Instance),
	}
}

func validateInstance(in controller.ValidationInput) field.ErrorList {
	instance := in.Instance
	var errs field.ErrorList

	if err := validateInstanceName(instance.Name); err != nil {
		errs = append(errs, invalid(field.NewPath("metadata", "name"), err))
	}

	master, isMasterPresent := instance.Spec.Components[common.ComponentMaster]
	volume, isVolumePresent := instance.Spec.Components[common.ComponentVolume]
	filer, isFilerPresent := instance.Spec.Components[common.ComponentFiler]
	s3, isS3Present := instance.Spec.Components[common.ComponentS3]
	componentChecks := []struct {
		name string
		err  error
	}{
		{common.ComponentMaster, validateMaster(master, isMasterPresent)},
		{common.ComponentVolume, validateVolume(volume, isVolumePresent)},
		{common.ComponentFiler, validateFiler(filer, isFilerPresent)},
		{common.ComponentS3, validateS3(s3, isS3Present)},
	}
	for _, check := range componentChecks {
		if check.err != nil {
			errs = append(errs, invalid(componentsPath.Key(check.name), check.err))
		}
	}
	// The rules below read replicas and storage, which the checks above require.
	if len(errs) > 0 {
		return errs
	}

	// An image without a release tag is not checked.
	if image, ok := componentImage(in.Provider, common.ComponentMaster, master); ok {
		if err := validateFilerVersion(image, *filer.Replicas); err != nil {
			errs = append(errs, invalid(componentsPath.Key(common.ComponentFiler).Child("replicas"), err))
		}
	}

	var s3CustomSpec components.S3CustomSpec
	if decodeParameters(s3.Parameters, &s3CustomSpec) {
		if err := validateS3Parameters(s3CustomSpec); err != nil {
			errs = append(errs, invalid(componentsPath.Key(common.ComponentS3).Child("parameters"), err))
		}
	}

	var topo standalone.StandaloneTopologyConfig
	if instance.Spec.Topology != nil && decodeParameters(instance.Spec.Topology.Parameters, &topo) {
		if err := validateTopologyParameters(topo); err != nil {
			errs = append(errs, invalid(topologyParametersPath, err))
		}
	}
	if err := validateReplication(effectiveReplication(topo.Replication, *volume.Replicas), *volume.Replicas); err != nil {
		errs = append(errs, invalid(topologyParametersPath.Child("replication"), err))
	}
	if err := validateMetricsPort(s3CustomSpec); err != nil {
		errs = append(errs, invalid(componentsPath.Key(common.ComponentS3).Child("parameters", "port"), err))
	}

	if in.Old != nil {
		errs = append(errs, validateStorageChanges(in.Old, instance)...)
	}

	return errs
}

// ValidateReferences checks the rules that read the cluster: the
// StorageClasses a new cluster would use, and the storage changes against the
// applied Seaweed cluster, for writes admission didn't see.
func (p *Provider) ValidateReferences(c *controller.Context) (field.ErrorList, error) {
	// The StorageClass only matters when PVCs are created. Checking it on a
	// provisioned instance would block it if the class is later removed.
	// Growth of an existing claim is checked separately.
	sw := &seaweedv1.Seaweed{}
	if err := c.Get(sw, c.Name()); err != nil {
		if !controller.IsNotFound(err) {
			return nil, err
		}
		return validateStorageClasses(c)
	}
	return validateStorageExpansion(c, sw)
}

// decodeParameters decodes raw parameters into target and reports whether
// they were set and decoded.
func decodeParameters(raw *runtime.RawExtension, target any) bool {
	if raw == nil || raw.Raw == nil {
		return false
	}
	return json.Unmarshal(raw.Raw, target) == nil
}

func validateTopologyParameters(topo standalone.StandaloneTopologyConfig) error {
	if err := validateTLSParameters(topo); err != nil {
		return err
	}
	return nil
}

// validateInstanceName requires an RFC-1035 label short enough for the
// operator's StatefulSets ("<name>-volume") and their pod labels.
func validateInstanceName(name string) error {
	if len(name) > maxInstanceNameLength {
		return fmt.Errorf("name %q exceeds max length of %d", name, maxInstanceNameLength)
	}
	if errs := validation.IsDNS1035Label(name); len(errs) > 0 {
		return fmt.Errorf("name %q must be an RFC-1035 label: %s", name, strings.Join(errs, "; "))
	}
	return nil
}

// validateStorageClasses checks that each named StorageClass exists.
// An unset or empty class is left to the cluster default. The lookup is one
// API GET per distinct name (unstructured, so it is not cached).
func validateStorageClasses(c *controller.Context) (field.ErrorList, error) {
	var errs field.ErrorList
	seen := map[string]struct{}{}
	for _, component := range []string{common.ComponentMaster, common.ComponentVolume, common.ComponentFiler} {
		comp, ok := c.Instance().Spec.Components[component]
		if !ok || comp.Storage == nil || comp.Storage.StorageClass == nil || *comp.Storage.StorageClass == "" {
			continue
		}
		name := *comp.Storage.StorageClass
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		exists, err := storageClassExists(c, name)
		if err != nil {
			return nil, fmt.Errorf("%q component: checking storage class %q: %w", component, name, err)
		}
		if !exists {
			errs = append(errs, field.Invalid(
				componentsPath.Key(component).Child("storage", "storageClass"), name,
				fmt.Sprintf("%q component: storage class %q does not exist", component, name),
			))
		}
	}
	return errs, nil
}

func storageClassExists(c *controller.Context, name string) (bool, error) {
	sc := &unstructured.Unstructured{}
	sc.SetGroupVersionKind(storageClassGVK)
	if err := c.Client().Get(c.Context(), client.ObjectKey{Name: name}, sc); err != nil {
		if controller.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
