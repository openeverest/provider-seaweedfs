package provider

import (
	"fmt"

	"github.com/openeverest/provider-seaweedfs/definition/components"
)

func validateVolumeParameters(spec components.VolumeCustomSpec) error {
	if spec.VolumeServerDiskCount != nil && *spec.VolumeServerDiskCount < 1 {
		return fmt.Errorf("volumeServerDiskCount must be at least 1")
	}
	return nil
}
