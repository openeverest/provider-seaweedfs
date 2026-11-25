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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition"
	"github.com/openeverest/provider-seaweedfs/definition/topologies/standalone"
)

func TestBuildTLSSpec(t *testing.T) {
	t.Run("nil without internalTLS", func(t *testing.T) {
		assert.Nil(t, buildTLSSpec(definition.TLSParameters{}))
		assert.Nil(t, buildTLSSpec(definition.TLSParameters{
			Issuer: &definition.IssuerReference{Name: "my-ca"},
		}))
	})

	t.Run("internalTLS without issuer", func(t *testing.T) {
		spec := buildTLSSpec(definition.TLSParameters{InternalTLS: true})
		require.NotNil(t, spec)
		assert.True(t, spec.Enabled)
		assert.Nil(t, spec.IssuerRef)
	})

	t.Run("internalTLS with issuer and defaults", func(t *testing.T) {
		spec := buildTLSSpec(definition.TLSParameters{
			InternalTLS: true,
			Issuer:      &definition.IssuerReference{Name: "my-ca"},
		})
		require.NotNil(t, spec)
		require.NotNil(t, spec.IssuerRef)
		assert.Equal(t, seaweedv1.TLSIssuerRef{Name: "my-ca", Kind: "Issuer", Group: "cert-manager.io"}, *spec.IssuerRef)
	})

	t.Run("internalTLS works with mode disabled", func(t *testing.T) {
		spec := buildTLSSpec(definition.TLSParameters{Mode: definition.TLSModeDisabled, InternalTLS: true})
		require.NotNil(t, spec)
		assert.True(t, spec.Enabled)
	})
}

func TestDecodeTLSParameters(t *testing.T) {
	params, err := decodeTLSParameters(instanceWithParams(""))
	require.NoError(t, err)
	assert.Equal(t, definition.TLSParameters{}, params)
	assert.Equal(t, definition.TLSModePrefer, tlsMode(params))
	assert.True(t, s3TLSEnabled(params))

	params, err = decodeTLSParameters(instanceWithParams(`{"tls":{"mode":"requireTLS","issuer":{"name":""}}}`))
	require.NoError(t, err)
	assert.Nil(t, params.Issuer, "an empty issuer group is no issuer")
	assert.Equal(t, definition.TLSModeRequire, tlsMode(params))

	params, err = decodeTLSParameters(instanceWithParams(`{"tls":{"issuer":{"kind":"ClusterIssuer"}}}`))
	require.NoError(t, err)
	assert.NotNil(t, params.Issuer, "a kind without a name is reported, not dropped")

	params, err = decodeTLSParameters(instanceWithParams(`{"tls":{"mode":"disabled"}}`))
	require.NoError(t, err)
	assert.False(t, s3TLSEnabled(params))
}

func TestS3CertificateSource(t *testing.T) {
	issuer := &definition.IssuerReference{Name: "ca"}
	assert.Equal(t, sourceManaged, s3CertificateSource(definition.TLSParameters{}))
	assert.Equal(t, sourceIssuer, s3CertificateSource(definition.TLSParameters{Issuer: issuer}))
	assert.Equal(t, sourceSecret, s3CertificateSource(definition.TLSParameters{CertificateSecretName: "s", Issuer: issuer}))

	assert.False(t, issuerSigns(definition.TLSParameters{}))
	assert.True(t, issuerSigns(definition.TLSParameters{Issuer: issuer}))
	assert.False(t, issuerSigns(definition.TLSParameters{Issuer: issuer, CertificateSecretName: "s"}))
	assert.True(t, issuerSigns(definition.TLSParameters{Issuer: issuer, CertificateSecretName: "s", InternalTLS: true}))
	assert.False(t, issuerSigns(definition.TLSParameters{Issuer: issuer, Mode: definition.TLSModeDisabled}))
}

func instanceWithParams(params string) *corev1alpha1.Instance {
	inst := &corev1alpha1.Instance{ObjectMeta: metav1.ObjectMeta{Name: "sw", Namespace: "ns"}}
	if params != "" {
		inst.Spec.Parameters = &runtime.RawExtension{Raw: []byte(params)}
	}
	return inst
}

func TestBuildSecurityConfigSpec(t *testing.T) {
	t.Run("nil when unset", func(t *testing.T) {
		assert.Nil(t, buildSecurityConfigSpec(standalone.StandaloneTopologyConfig{}))
	})

	t.Run("nil when all jwt flags false", func(t *testing.T) {
		assert.Nil(t, buildSecurityConfigSpec(standalone.StandaloneTopologyConfig{
			SecurityConfig: &standalone.SecurityConfigSpec{
				JWTSigning: &standalone.JWTSigningSpec{},
			},
		}))
	})

	t.Run("passes jwt flags", func(t *testing.T) {
		spec := buildSecurityConfigSpec(standalone.StandaloneTopologyConfig{
			SecurityConfig: &standalone.SecurityConfigSpec{
				JWTSigning: &standalone.JWTSigningSpec{
					VolumeWrite: true,
					FilerWrite:  true,
				},
			},
		})
		require.NotNil(t, spec)
		require.NotNil(t, spec.JWTSigning)
		assert.True(t, spec.JWTSigning.VolumeWrite)
		assert.True(t, spec.JWTSigning.FilerWrite)
		assert.False(t, spec.JWTSigning.VolumeRead)
		assert.False(t, spec.JWTSigning.FilerRead)
	})
}

func TestTLSSecretToInstance(t *testing.T) {
	tests := []struct {
		name   string
		secret *corev1.Secret
		expect []reconcile.Request
	}{
		{
			name: "server TLS secret enqueues Instance",
			secret: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: "sw-server-tls", Namespace: "ns",
			}},
			expect: []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "sw"}}},
		},
		{
			name: "S3 certificate secret enqueues Instance",
			secret: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: "sw-s3-https", Namespace: "ns",
			}},
			expect: []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "sw"}}},
		},
		{
			name: "unrelated secret ignored",
			secret: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: "sw-s3", Namespace: "ns",
			}},
		},
		{
			name: "suffix-only name ignored",
			secret: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: "-server-tls", Namespace: "ns",
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, tlsSecretToInstance(context.Background(), tt.secret))
		})
	}
}

func TestTLSEnabledOnSeaweed(t *testing.T) {
	assert.False(t, tlsEnabledOnSeaweed(nil))
	assert.False(t, tlsEnabledOnSeaweed(&seaweedv1.Seaweed{}))
	assert.False(t, tlsEnabledOnSeaweed(&seaweedv1.Seaweed{
		Spec: seaweedv1.SeaweedSpec{TLS: &seaweedv1.TLSSpec{Enabled: false}},
	}))
	assert.True(t, tlsEnabledOnSeaweed(&seaweedv1.Seaweed{
		Spec: seaweedv1.SeaweedSpec{TLS: &seaweedv1.TLSSpec{Enabled: true}},
	}))
}
