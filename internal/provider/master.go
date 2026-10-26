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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"

	"github.com/AlekSi/pointer"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func validateRequiredComponent(name string, comp corev1alpha1.ComponentSpec, present bool) error {
	if !present {
		return fmt.Errorf("%q component is required", name)
	}
	if comp.Replicas == nil {
		return fmt.Errorf("%q component: replicas is required", name)
	}
	if *comp.Replicas < 1 {
		return fmt.Errorf("%q component: replicas must be at least 1", name)
	}
	return validateSchedulingPolicy(name, comp.SchedulingPolicy)
}

func validateMaster(comp corev1alpha1.ComponentSpec, present bool) error {
	if err := validateRequiredComponent(common.ComponentMaster, comp, present); err != nil {
		return err
	}
	if *comp.Replicas%2 == 0 {
		return fmt.Errorf("%q component: the number of replicas must be odd", common.ComponentMaster)
	}
	return validateNoService(common.ComponentMaster, comp)
}

func validateMasterParameters(spec components.MasterCustomSpec) error {
	if spec.MasterVolumeSizeLimitMB != nil && *spec.MasterVolumeSizeLimitMB < 1 {
		return fmt.Errorf("masterVolumeSizeLimitMB must be at least 1")
	}
	if spec.VolumeGrowthCount != nil && *spec.VolumeGrowthCount < 1 {
		return fmt.Errorf("volumeGrowthCount must be at least 1")
	}
	return nil
}

// masterConfigChecksumAnnotation rolls the master pods when master.toml
// changes: `weed master` reads it only at startup.
const masterConfigChecksumAnnotation = "seaweedfs.openeverest.io/master-config-checksum"

// volumeGrowthConfig renders the master.toml that sets how many logical
// volumes the master grows at once for one-, two- and three-copy placements.
// Placements with more copies keep the upstream copy_other default of 1.
func volumeGrowthConfig(count int32) string {
	return fmt.Sprintf("[master.volume_growth]\ncopy_1 = %d\ncopy_2 = %d\ncopy_3 = %d\n", count, count, count)
}

// effectiveReplication returns the placement written to master.defaultReplication.
// An empty value uses 001 when at least two volume servers are requested, and 000 otherwise.
func effectiveReplication(replication *string, volumeServers int32) string {
	if replication != nil && *replication != "" {
		return *replication
	}
	if volumeServers >= 2 {
		return DefaultReplication
	}
	return "000"
}

var replicationPattern = regexp.MustCompile(`^[0-9]{3}$`)

// validateReplication rejects placements the cluster cannot satisfy, volume servers have no rack/DC set.
func validateReplication(replication string, volumeServers int32) error {
	if !replicationPattern.MatchString(replication) {
		return fmt.Errorf("replication %q must be three digits such as 000, 001, or 002", replication)
	}
	if replication[0] != '0' || replication[1] != '0' {
		return fmt.Errorf("replication %q requires multiple data centers or racks, which are not supported", replication)
	}
	if copies := int32(replication[2]-'0') + 1; copies > volumeServers {
		return fmt.Errorf("replication %q needs at least %d volume servers, got %d", replication, copies, volumeServers)
	}
	return nil
}

func buildMasterSpec(comp corev1alpha1.ComponentSpec, masterCustomSpec components.MasterCustomSpec, replication string) *seaweedv1.MasterSpec {
	spec := &seaweedv1.MasterSpec{
		Replicas:           *comp.Replicas,
		VolumeSizeLimitMB:  pointer.ToInt32(DefaultMasterVolumeSizeLimitMB),
		DefaultReplication: pointer.ToString(replication),
	}

	if masterCustomSpec.MasterVolumeSizeLimitMB != nil {
		spec.VolumeSizeLimitMB = masterCustomSpec.MasterVolumeSizeLimitMB
	}

	growthCount := DefaultVolumeGrowthCount
	if masterCustomSpec.VolumeGrowthCount != nil {
		growthCount = *masterCustomSpec.VolumeGrowthCount
	}
	config := volumeGrowthConfig(growthCount)
	spec.Config = pointer.ToString(config)
	sum := sha256.Sum256([]byte(config))
	spec.Annotations = map[string]string{masterConfigChecksumAnnotation: hex.EncodeToString(sum[:])}

	if comp.Resources != nil {
		spec.ResourceRequirements = *comp.Resources
	}

	spec.Persistence = buildPersistenceSpec(comp.Storage)

	return spec
}
