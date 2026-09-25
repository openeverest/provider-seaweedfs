package provider

import (
	"fmt"

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
	return nil
}

func validateMaster(comp corev1alpha1.ComponentSpec, present bool) error {
	if err := validateRequiredComponent(common.ComponentMaster, comp, present); err != nil {
		return err
	}
	if *comp.Replicas%2 == 0 {
		return fmt.Errorf("%q component: the number of replicas must be odd", common.ComponentMaster)
	}
	return nil
}

func validateMasterParameters(spec components.MasterCustomSpec) error {
	if spec.MasterVolumeSizeLimitMB != nil && *spec.MasterVolumeSizeLimitMB < 1 {
		return fmt.Errorf("masterVolumeSizeLimitMB must be at least 1")
	}
	return nil
}

func buildMasterSpec(comp corev1alpha1.ComponentSpec, masterCustomSpec components.MasterCustomSpec) *seaweedv1.MasterSpec {
	spec := &seaweedv1.MasterSpec{
		Replicas:          *comp.Replicas,
		VolumeSizeLimitMB: pointer.ToInt32(DefaultMasterVolumeSizeLimitMB),
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
