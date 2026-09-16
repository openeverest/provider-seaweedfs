package provider

import (
	"fmt"

	"github.com/AlekSi/pointer"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func validateMaster(comp corev1alpha1.ComponentSpec, present bool) error {
	if !present {
		return fmt.Errorf("%q component is required", common.ComponentMaster)
	}
	if comp.Replicas == nil {
		return fmt.Errorf("%q component: replicas is required", common.ComponentMaster)
	}
	if *comp.Replicas < 1 {
		return fmt.Errorf("%q component: replicas must be at least 1", common.ComponentMaster)
	}
	if *comp.Replicas % 2 == 1 {
		return fmt.Errorf("%q component: the number of replicas must be odd", common.ComponentMaster)
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

	if comp.Storage != nil && !comp.Storage.Size.IsZero() {
		persistence := &seaweedv1.PersistenceSpec{
			Enabled: true,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: comp.Storage.Size,
				},
			},
		}
		if comp.Storage.StorageClass != nil && *comp.Storage.StorageClass != "" {
			persistence.StorageClassName = comp.Storage.StorageClass
		}
		spec.Persistence = persistence
	}

	return spec
}
