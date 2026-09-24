package provider

import (
	"fmt"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func validateFiler(comp corev1alpha1.ComponentSpec, present bool) error {
	if err := validateRequiredComponent(common.ComponentFiler, comp, present); err != nil {
		return err
	}
	if comp.Storage == nil || comp.Storage.Size.IsZero() {
		return fmt.Errorf("%q component: storage.size is required", common.ComponentFiler)
	}
	return nil
}

func validateFilerParameters(spec components.FilerCustomSpec) error {
	if spec.MaxMB != nil && *spec.MaxMB < 1 {
		return fmt.Errorf("maxMB must be at least 1")
	}
	return nil
}

func buildFilerSpec(comp corev1alpha1.ComponentSpec, filerCustomSpec components.FilerCustomSpec) *seaweedv1.FilerSpec {
	spec := &seaweedv1.FilerSpec{
		Replicas: *comp.Replicas,
	}

	if filerCustomSpec.MaxMB != nil {
		spec.MaxMB = filerCustomSpec.MaxMB
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
