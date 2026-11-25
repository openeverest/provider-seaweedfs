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
	"context"
	"encoding/json"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition"
	"github.com/openeverest/provider-seaweedfs/definition/topologies/standalone"
)

const (
	// TLS server secret suffix used by seaweedfs-operator (see TLSServerSecretName).
	tlsServerSecretSuffix = "-server-tls"

	// tlsKeyCA is the CA certificate key in certificate Secrets and in the
	// connection Secret.
	tlsKeyCA = "ca.crt"

	certManagerGroup = "cert-manager.io"
)

// certificateSource is where the S3 server certificate comes from.
type certificateSource int

const (
	// sourceManaged is a CA this provider creates for the instance.
	sourceManaged certificateSource = iota
	// sourceSecret is the user's certificateSecretName.
	sourceSecret
	// sourceIssuer is a cert-manager Certificate signed by the user's issuer.
	sourceIssuer
)

func tlsServerSecretName(instanceName string) string {
	return instanceName + tlsServerSecretSuffix
}

// tlsSecretToInstance maps a certificate Secret to its Instance by name: the
// operator's server Secret (<name>-server-tls) and the S3 server certificate
// (<name>-s3-https). cert-manager owns them, not the Instance or Seaweed.
func tlsSecretToInstance(_ context.Context, obj client.Object) []reconcile.Request {
	name := obj.GetName()
	var instanceName string
	switch {
	case strings.HasSuffix(name, tlsServerSecretSuffix):
		instanceName = strings.TrimSuffix(name, tlsServerSecretSuffix)
	case strings.HasSuffix(name, s3HTTPSSecretSuffix):
		instanceName = strings.TrimSuffix(name, s3HTTPSSecretSuffix)
	}
	if instanceName == "" {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Namespace: obj.GetNamespace(), Name: instanceName},
	}}
}

func isTLSSecretName(name string) bool {
	return strings.HasSuffix(name, tlsServerSecretSuffix) || strings.HasSuffix(name, s3HTTPSSecretSuffix)
}

// tlsParameters returns spec.parameters.tls of the reconciled Instance.
// Validate rejects parameters that don't decode.
func tlsParameters(c *controller.Context) definition.TLSParameters {
	params, _ := decodeTLSParameters(c.Instance())
	return params
}

func decodeTLSParameters(instance *corev1alpha1.Instance) (definition.TLSParameters, error) {
	if instance == nil || instance.Spec.Parameters == nil || len(instance.Spec.Parameters.Raw) == 0 {
		return definition.TLSParameters{}, nil
	}
	var params definition.Parameters
	if err := json.Unmarshal(instance.Spec.Parameters.Raw, &params); err != nil {
		return definition.TLSParameters{}, err
	}
	if params.TLS == nil {
		return definition.TLSParameters{}, nil
	}
	// The UI can send an empty issuer group when its fields are left empty.
	if ref := params.TLS.Issuer; ref != nil && *ref == (definition.IssuerReference{}) {
		params.TLS.Issuer = nil
	}
	return *params.TLS, nil
}

func tlsMode(params definition.TLSParameters) definition.TLSMode {
	if params.Mode == "" {
		return definition.TLSModePrefer
	}
	return params.Mode
}

// s3TLSEnabled reports whether the S3 gateway serves HTTPS.
func s3TLSEnabled(params definition.TLSParameters) bool {
	return tlsMode(params) != definition.TLSModeDisabled
}

func s3CertificateSource(params definition.TLSParameters) certificateSource {
	switch {
	case params.CertificateSecretName != "":
		return sourceSecret
	case params.Issuer != nil:
		return sourceIssuer
	default:
		return sourceManaged
	}
}

// issuerSigns reports whether the issuer signs any certificate: the S3 one
// or the component ones.
func issuerSigns(params definition.TLSParameters) bool {
	if params.Issuer == nil {
		return false
	}
	return params.InternalTLS || (s3TLSEnabled(params) && s3CertificateSource(params) == sourceIssuer)
}

func issuerKind(ref definition.IssuerReference) string {
	if ref.Kind == "" {
		return string(definition.IssuerKindIssuer)
	}
	return string(ref.Kind)
}

func issuerGroup(ref definition.IssuerReference) string {
	if ref.Group == "" {
		return certManagerGroup
	}
	return ref.Group
}

// buildTLSSpec turns on seaweedfs-operator's inter-component mTLS.
func buildTLSSpec(params definition.TLSParameters) *seaweedv1.TLSSpec {
	if !params.InternalTLS {
		return nil
	}
	spec := &seaweedv1.TLSSpec{Enabled: true}
	if ref := params.Issuer; ref != nil {
		spec.IssuerRef = &seaweedv1.TLSIssuerRef{
			Name:  ref.Name,
			Kind:  issuerKind(*ref),
			Group: issuerGroup(*ref),
		}
	}
	return spec
}

func buildSecurityConfigSpec(topo standalone.StandaloneTopologyConfig) *seaweedv1.SecurityConfigSpec {
	if topo.SecurityConfig == nil || topo.SecurityConfig.JWTSigning == nil {
		return nil
	}
	jwt := topo.SecurityConfig.JWTSigning
	if !jwt.VolumeWrite && !jwt.VolumeRead && !jwt.FilerWrite && !jwt.FilerRead {
		return nil
	}
	out := &seaweedv1.JWTSigningSpec{
		VolumeWrite: jwt.VolumeWrite,
		VolumeRead:  jwt.VolumeRead,
		FilerWrite:  jwt.FilerWrite,
		FilerRead:   jwt.FilerRead,
	}
	if exp := jwt.ExpiresAfterSeconds; exp != nil {
		out.ExpiresAfterSeconds = &seaweedv1.JWTExpiresAfterSecondsSpec{
			VolumeWrite: exp.VolumeWrite,
			VolumeRead:  exp.VolumeRead,
			FilerWrite:  exp.FilerWrite,
			FilerRead:   exp.FilerRead,
		}
	}
	return &seaweedv1.SecurityConfigSpec{JWTSigning: out}
}

// tlsEnabledOnSeaweed reports whether the Seaweed CR requested inter-component mTLS.
func tlsEnabledOnSeaweed(sw *seaweedv1.Seaweed) bool {
	return sw != nil && sw.Spec.TLS != nil && sw.Spec.TLS.Enabled
}

// waitingForTLSSecret is true when mTLS was requested but the operator has not
// yet produced the server TLS Secret (missing cert-manager, Certificate not
// Ready, etc.). seaweedfs-operator does not expose a TLSReady condition — it
// only logs when cert-manager CRDs are absent — so we gate Instance Ready on
// the Secret the operator mounts into every component.
func waitingForTLSSecret(c *controller.Context, sw *seaweedv1.Seaweed) (bool, error) {
	if !tlsEnabledOnSeaweed(sw) {
		return false, nil
	}
	sec := &corev1.Secret{}
	if err := c.Get(sec, tlsServerSecretName(sw.Name)); err != nil {
		if controller.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}
