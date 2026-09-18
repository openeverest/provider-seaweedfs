package provider

import (
	"fmt"
	"maps"

	"github.com/AlekSi/pointer"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func validateVolume(comp corev1alpha1.ComponentSpec, present bool) error {
	if err := validateRequiredComponent(common.ComponentVolume, comp, present); err != nil {
		return err
	}
	if comp.Storage == nil || comp.Storage.Size.IsZero() {
		return fmt.Errorf("%q component: storage.size is required", common.ComponentVolume)
	}
	return nil
}

func validateVolumeParameters(spec components.VolumeCustomSpec) error {
	if spec.MaxVolumeCounts != nil && *spec.MaxVolumeCounts < 1 {
		return fmt.Errorf("maxVolumeCounts must be at least 1")
	}
	return nil
}

func buildVolumeSpec(comp corev1alpha1.ComponentSpec, volumeCustomSpec components.VolumeCustomSpec) *seaweedv1.VolumeSpec {
	spec := &seaweedv1.VolumeSpec{
		Replicas: *comp.Replicas,
		VolumeServerConfig: seaweedv1.VolumeServerConfig{
			MaxVolumeCounts: pointer.ToInt32(DefaultMaxVolumeCounts),
		},
	}

	if volumeCustomSpec.MaxVolumeCounts != nil {
		spec.MaxVolumeCounts = volumeCustomSpec.MaxVolumeCounts
	}

	if comp.Resources != nil {
		spec.ResourceRequirements = *comp.Resources
	}

	if comp.Storage != nil && !comp.Storage.Size.IsZero() {
		// clone requests so we don't mutate the caller's ResourceList when
		// merging storage into an existing cpu/memory request map.
		requests := make(corev1.ResourceList, len(spec.Requests)+1)
		maps.Copy(requests, spec.Requests)
		requests[corev1.ResourceStorage] = comp.Storage.Size
		spec.Requests = requests

		if comp.Storage.StorageClass != nil && *comp.Storage.StorageClass != "" {
			spec.StorageClassName = comp.Storage.StorageClass
		}
	}

	return spec
}
