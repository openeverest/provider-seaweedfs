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
	if err := validateRequiredComponent(common.ComponentS3, comp, present); err != nil {
		return err
	}
	return validateService(common.ComponentS3, comp.Service)
}

func validateS3Parameters(spec components.S3CustomSpec) error {
	if spec.Port != nil && (*spec.Port < 1 || *spec.Port > MaxS3Port) {
		return fmt.Errorf("port must be between 1 and %d", MaxS3Port)
	}
	if err := validateS3DomainName(spec.DomainName); err != nil {
		return err
	}
	if err := validateS3Ingress(spec.Ingress); err != nil {
		return err
	}
	return nil
}

func validateS3Ingress(ing *components.S3IngressSpec) error {
	if ing == nil || !ing.Enabled {
		return nil
	}
	if ing.Host == "" {
		return fmt.Errorf("ingress.host is required when ingress is enabled")
	}
	if errs := validation.IsDNS1123Subdomain(ing.Host); len(errs) > 0 {
		return fmt.Errorf("ingress.host %q is invalid: %s", ing.Host, strings.Join(errs, "; "))
	}
	if tls := ing.TLS; tls != nil {
		if tls.SecretName == "" {
			return fmt.Errorf("ingress.tls.secretName is required when ingress.tls is set")
		}
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
		Service:    buildServiceSpec(comp.Service),
		Ingress:    buildS3IngressSpec(s3CustomSpec.Ingress),
	}

	if comp.Resources != nil {
		spec.ResourceRequirements = *comp.Resources
	}

	return spec
}

func buildS3IngressSpec(ing *components.S3IngressSpec) *seaweedv1.IngressSpec {
	if ing == nil || !ing.Enabled {
		return nil
	}
	out := &seaweedv1.IngressSpec{
		Enabled:     true,
		Host:        ing.Host,
		ClassName:   ing.ClassName,
		Annotations: ing.Annotations,
	}
	if tls := ing.TLS; tls != nil && tls.SecretName != "" {
		out.TLS = []seaweedv1.IngressTLS{{
			Hosts:      []string{ing.Host},
			SecretName: tls.SecretName,
		}}
	}
	return out
}
