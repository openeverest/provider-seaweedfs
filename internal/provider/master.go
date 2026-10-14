package provider

import (
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
	return nil
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

	if comp.Resources != nil {
		spec.ResourceRequirements = *comp.Resources
	}

	spec.Persistence = buildPersistenceSpec(comp.Storage)

	return spec
}
