package provider

import (
	"fmt"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func validateS3(comp corev1alpha1.ComponentSpec, present bool) error {
	return validateRequiredComponent(common.ComponentS3, comp, present)
}

func validateS3Parameters(spec components.S3CustomSpec) error {
	if spec.Port != nil && (*spec.Port < 1 || *spec.Port > 65535) {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

func buildS3Spec(comp corev1alpha1.ComponentSpec, s3CustomSpec components.S3CustomSpec) *seaweedv1.S3GatewaySpec {
	spec := &seaweedv1.S3GatewaySpec{
		Replicas: *comp.Replicas,
	}

	if comp.Resources != nil {
		spec.ResourceRequirements = *comp.Resources
	}

	if s3CustomSpec.Port != nil {
		spec.Port = s3CustomSpec.Port
	}

	if s3CustomSpec.DomainName != nil {
		spec.DomainName = s3CustomSpec.DomainName
	}

	return spec
}
