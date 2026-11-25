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
	"fmt"
	"strings"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/openeverest/provider-seaweedfs/definition"
	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func validateS3(comp corev1alpha1.ComponentSpec, present bool) error {
	if err := validateRequiredComponent(common.ComponentS3, comp, present); err != nil {
		return err
	}
	return validateService(common.ComponentS3, comp.Service)
}

// validateS3Parameters checks the S3 parameters. tlsOn is whether the
// gateway serves HTTPS on defaultS3HTTPSPort.
func validateS3Parameters(spec components.S3CustomSpec, tlsOn bool) error {
	if tlsOn {
		port := int32(defaultS3Port)
		if spec.Port != nil {
			port = *spec.Port
		}
		if port == defaultS3HTTPSPort {
			return fmt.Errorf("S3 port cannot be %d while TLS is on", defaultS3HTTPSPort)
		}
	}
	if err := validateS3DomainName(spec.DomainName); err != nil {
		return err
	}
	if err := validateS3Ingress(spec.Ingress); err != nil {
		return err
	}
	return nil
}

// s3IngressEnabled is true when Ingress is turned on explicitly, or when enabled
// is omitted and a host is set (UI toggleable group). Explicit false wins over host.
func s3IngressEnabled(ing *components.S3IngressSpec) bool {
	if ing == nil {
		return false
	}
	if ing.Enabled != nil {
		return *ing.Enabled
	}
	return ing.Host != ""
}

func validateS3Ingress(ing *components.S3IngressSpec) error {
	if !s3IngressEnabled(ing) {
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

func buildS3Spec(comp corev1alpha1.ComponentSpec, s3CustomSpec components.S3CustomSpec, tlsSecretName string, mode definition.TLSMode) *seaweedv1.S3GatewaySpec {
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

	// seaweedfs-operator requires httpsPort and tlsSecret together. It does
	// not create the Secret. httpsOnly drops the HTTP listener; the CRD
	// rejects that together with Ingress, which routes to the HTTP port, so
	// preferTLS keeps HTTP for an Ingress and requireTLS rejects one.
	if tlsSecretName != "" {
		httpsPort := defaultS3HTTPSPort
		spec.HTTPSPort = &httpsPort
		spec.TLSSecret = &corev1.LocalObjectReference{Name: tlsSecretName}
		spec.HTTPSOnly = mode == definition.TLSModeRequire || spec.Ingress == nil
	}

	return spec
}

func buildS3IngressSpec(ing *components.S3IngressSpec) *seaweedv1.IngressSpec {
	if !s3IngressEnabled(ing) {
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
