package provider

import (
	"fmt"
	"strings"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func validateS3(comp corev1alpha1.ComponentSpec, present bool) error {
	return validateRequiredComponent(common.ComponentS3, comp, present)
}

func validateS3Parameters(spec components.S3CustomSpec) error {
	if spec.Port != nil && (*spec.Port < 1 || *spec.Port > MaxS3Port) {
		return fmt.Errorf("port must be between 1 and %d", MaxS3Port)
	}
	if err := validateS3DomainName(spec.DomainName); err != nil {
		return err
	}
	return nil
}

// validateS3DomainName checks each comma-separated domainName entry.
// empty string is treated as unset. Entries must be DNS-1123 subdomains.
func validateS3DomainName(domainName *string) error {
	if domainName == nil || *domainName == "" {
		return nil
	}
	for _, entry := range strings.Split(*domainName, ",") {
		if entry == "" {
			continue
		}
		if errs := validation.IsDNS1123Subdomain(entry); len(errs) > 0 {
			return fmt.Errorf("domainName %q is invalid: %s", entry, strings.Join(errs, "; "))
		}
	}
	return nil
}

func buildS3Spec(comp corev1alpha1.ComponentSpec, s3CustomSpec components.S3CustomSpec) *seaweedv1.S3GatewaySpec {
	spec := &seaweedv1.S3GatewaySpec{
		Replicas:   *comp.Replicas,
		Port:       s3CustomSpec.Port,
		DomainName: s3CustomSpec.DomainName,
	}

	if comp.Resources != nil {
		spec.ResourceRequirements = *comp.Resources
	}

	return spec
}
