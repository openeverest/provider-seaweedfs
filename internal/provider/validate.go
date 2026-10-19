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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/definition/topologies/standalone"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

// dnsLabelMaxLength is the Kubernetes limit for Service and Pod names.
const dnsLabelMaxLength = 63

// volumePeerServiceSuffix is the longest suffix seaweedfs-operator appends
// to the Seaweed name for a DNS label this provider always creates.
// The volume and master headless Services are "<name>-volume-peer" and
// "<name>-master-peer". Volume pod names ("<name>-volume-<ordinal>") stay
// inside the same budget through ordinal 9999.
const volumePeerServiceSuffix = "-volume-peer"

// maxInstanceNameLength leaves room for that suffix under dnsLabelMaxLength.
const maxInstanceNameLength = dnsLabelMaxLength - len(volumePeerServiceSuffix)

var storageClassGVK = schema.GroupVersionKind{
	Group:   "storage.k8s.io",
	Version: "v1",
	Kind:    "StorageClass",
}

// Validate checks if the Instance spec is valid.
func (p *Provider) Validate(c *controller.Context) error {
	l := log.FromContext(c.Context())
	l.Info("Validating instance", "name", c.Name())

	if err := validateInstanceName(c.Name()); err != nil {
		return err
	}

	master, isMasterPresent := c.Instance().Spec.Components[common.ComponentMaster]
	if err := validateMaster(master, isMasterPresent); err != nil {
		return err
	}

	volume, isVolumePresent := c.Instance().Spec.Components[common.ComponentVolume]
	if err := validateVolume(volume, isVolumePresent); err != nil {
		return err
	}

	filer, isFilerPresent := c.Instance().Spec.Components[common.ComponentFiler]
	if err := validateFiler(filer, isFilerPresent); err != nil {
		return err
	}

	s3, isS3Present := c.Instance().Spec.Components[common.ComponentS3]
	if err := validateS3(s3, isS3Present); err != nil {
		return err
	}

	var masterCustomSpec components.MasterCustomSpec
	if c.TryDecodeComponentParameters(master, &masterCustomSpec) {
		if err := validateMasterParameters(masterCustomSpec); err != nil {
			return err
		}
	}

	var volumeCustomSpec components.VolumeCustomSpec
	if c.TryDecodeComponentParameters(volume, &volumeCustomSpec) {
		if err := validateVolumeParameters(volumeCustomSpec); err != nil {
			return err
		}
	}

	var filerCustomSpec components.FilerCustomSpec
	if c.TryDecodeComponentParameters(filer, &filerCustomSpec) {
		if err := validateFilerParameters(filerCustomSpec); err != nil {
			return err
		}
	}

	var s3CustomSpec components.S3CustomSpec
	if c.TryDecodeComponentParameters(s3, &s3CustomSpec) {
		if err := validateS3Parameters(s3CustomSpec); err != nil {
			return err
		}
	}

	var topo standalone.StandaloneTopologyConfig
	if c.TryDecodeTopologyParameters(&topo) {
		if err := validateTopologyParameters(topo); err != nil {
			return err
		}
	}
	if err := validateReplication(effectiveReplication(topo.Replication, *volume.Replicas), *volume.Replicas); err != nil {
		return err
	}
	if err := validateStorageClasses(c); err != nil {
		return err
	}

	return nil
}

func validateTopologyParameters(topo standalone.StandaloneTopologyConfig) error {
	if topo.VolumeServerDiskCount != nil && *topo.VolumeServerDiskCount < 1 {
		return fmt.Errorf("volumeServerDiskCount must be at least 1")
	}
	if err := validateTLSParameters(topo); err != nil {
		return err
	}
	return nil
}

// validateInstanceName requires an RFC-1035 label short enough that operator
// child names such as "<name>-volume-peer" stay within 63 characters.
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
func validateStorageClasses(c *controller.Context) error {
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
		if err := validateStorageClassExists(c, component, name); err != nil {
			return err
		}
	}
	return nil
}

func validateStorageClassExists(c *controller.Context, component, name string) error {
	sc := &unstructured.Unstructured{}
	sc.SetGroupVersionKind(storageClassGVK)
	if err := c.Client().Get(c.Context(), client.ObjectKey{Name: name}, sc); err != nil {
		if controller.IsNotFound(err) {
			return fmt.Errorf("%q component: storage class %q does not exist", component, name)
		}
		return fmt.Errorf("%q component: checking storage class %q: %w", component, name, err)
	}
	return nil
}
