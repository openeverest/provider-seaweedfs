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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition"
)

const (
	// tlsNamesAnnotation records on the Seaweed CR the additional names the
	// S3 server certificate is issued for, so a change on a running instance
	// waits for maintenance approval.
	tlsNamesAnnotation = "seaweedfs.openeverest.io/tls-additional-names"

	// s3TLSNamesAnnotation is an S3 pod annotation that changes with the
	// additional names the served certificate carries, so the gateway
	// restarts to serve it instead of waiting for its periodic reload.
	s3TLSNamesAnnotation = "seaweedfs.openeverest.io/tls-names"
)

// additionalNames returns the additional names the S3 certificate should
// carry, sorted. Only certificates this provider issues or requests carry them.
func additionalNames(params definition.TLSParameters) []string {
	if !s3TLSEnabled(params) || s3CertificateSource(params) == sourceSecret {
		return nil
	}
	names := slices.Clone(params.AdditionalNames)
	slices.Sort(names)
	return slices.Compact(names)
}

// appliedAdditionalNames returns the additional names to issue the S3
// certificate for. A new instance takes them as they are; on a running one a
// change reissues the certificate and restarts the S3 gateway, so it waits for
// maintenance approval and keeps the recorded names until then.
func appliedAdditionalNames(c *controller.Context, params definition.TLSParameters) ([]string, error) {
	desired := additionalNames(params)
	existing := &seaweedv1.Seaweed{}
	found, err := c.Exists(existing, c.Name())
	if err != nil {
		return nil, fmt.Errorf("get seaweed: %w", err)
	}
	if !found {
		return desired, nil
	}
	recorded := splitNames(existing.Annotations[tlsNamesAnnotation])
	if slices.Equal(desired, recorded) {
		return desired, nil
	}
	description := "Reissue the S3 certificate without additional names and restart the S3 gateway"
	if len(desired) > 0 {
		description = fmt.Sprintf("Reissue the S3 certificate for the additional names %s and restart the S3 gateway",
			strings.Join(desired, ", "))
	}
	if c.RequestMaintenance("tls-names-"+namesHash(desired), description, controller.MaintenanceRollingRestart) {
		return desired, nil
	}
	return recorded, nil
}

// recordTLSNames keeps the applied names on the Seaweed CR.
func recordTLSNames(sw *seaweedv1.Seaweed, applied []string) {
	if len(applied) == 0 {
		return
	}
	if sw.Annotations == nil {
		sw.Annotations = map[string]string{}
	}
	sw.Annotations[tlsNamesAnnotation] = strings.Join(applied, ",")
}

// addS3TLSNamesRestart sets the S3 pod annotation from the additional names
// in the served certificate, so the gateway restarts once a certificate with
// new names is in its Secret (cert-manager reissues asynchronously). Without
// additional names there is no annotation, so instances without them don't
// restart when the provider is upgraded.
func addS3TLSNamesRestart(c *controller.Context, spec *seaweedv1.S3GatewaySpec, params definition.TLSParameters) error {
	if spec == nil || spec.TLSSecret == nil || s3CertificateSource(params) == sourceSecret {
		return nil
	}
	secret := &corev1.Secret{}
	found, err := c.Exists(secret, spec.TLSSecret.Name)
	if err != nil || !found || len(secret.Data[corev1.TLSCertKey]) == 0 {
		return err
	}
	cert, err := parseCertificate(secret.Data[corev1.TLSCertKey])
	if err != nil {
		return fmt.Errorf("reading the certificate in Secret %q: %w", secret.Name, err)
	}
	own := s3ServerDNSNames(c.Name(), c.Namespace(), nil)
	var served []string
	for _, name := range cert.DNSNames {
		if !slices.Contains(own, name) {
			served = append(served, name)
		}
	}
	if len(served) == 0 {
		return nil
	}
	slices.Sort(served)
	if spec.Annotations == nil {
		spec.Annotations = map[string]string{}
	}
	spec.Annotations[s3TLSNamesAnnotation] = namesHash(slices.Compact(served))
	return nil
}

func splitNames(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func namesHash(names []string) string {
	sum := sha256.Sum256([]byte(strings.Join(names, ",")))
	return hex.EncodeToString(sum[:])[:10]
}
