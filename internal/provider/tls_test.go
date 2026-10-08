package provider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/topologies/standalone"
)

func TestBuildTLSSpec(t *testing.T) {
	t.Run("nil when TLS unset", func(t *testing.T) {
		assert.Nil(t, buildTLSSpec(standalone.StandaloneTopologyConfig{}))
	})

	t.Run("nil when disabled", func(t *testing.T) {
		assert.Nil(t, buildTLSSpec(standalone.StandaloneTopologyConfig{
			TLS: &seaweedv1.TLSSpec{Enabled: false},
		}))
	})

	t.Run("enabled without issuerRef", func(t *testing.T) {
		spec := buildTLSSpec(standalone.StandaloneTopologyConfig{
			TLS: &seaweedv1.TLSSpec{Enabled: true},
		})
		require.NotNil(t, spec)
		assert.True(t, spec.Enabled)
		assert.Nil(t, spec.IssuerRef)
	})

	t.Run("enabled with issuerRef", func(t *testing.T) {
		spec := buildTLSSpec(standalone.StandaloneTopologyConfig{
			TLS: &seaweedv1.TLSSpec{
				Enabled: true,
				IssuerRef: &seaweedv1.TLSIssuerRef{
					Name:  "my-ca",
					Kind:  "ClusterIssuer",
					Group: "cert-manager.io",
				},
			},
		})
		require.NotNil(t, spec)
		require.NotNil(t, spec.IssuerRef)
		assert.Equal(t, "my-ca", spec.IssuerRef.Name)
		assert.Equal(t, "ClusterIssuer", spec.IssuerRef.Kind)
		assert.Equal(t, "cert-manager.io", spec.IssuerRef.Group)
	})

	t.Run("issuerRef without name is omitted", func(t *testing.T) {
		spec := buildTLSSpec(standalone.StandaloneTopologyConfig{
			TLS: &seaweedv1.TLSSpec{
				Enabled:   true,
				IssuerRef: &seaweedv1.TLSIssuerRef{Kind: "Issuer"},
			},
		})
		require.NotNil(t, spec)
		assert.Nil(t, spec.IssuerRef)
	})
}

func TestBuildSecurityConfigSpec(t *testing.T) {
	t.Run("nil when unset", func(t *testing.T) {
		assert.Nil(t, buildSecurityConfigSpec(standalone.StandaloneTopologyConfig{}))
	})

	t.Run("nil when all jwt flags false", func(t *testing.T) {
		assert.Nil(t, buildSecurityConfigSpec(standalone.StandaloneTopologyConfig{
			SecurityConfig: &seaweedv1.SecurityConfigSpec{
				JWTSigning: &seaweedv1.JWTSigningSpec{},
			},
		}))
	})

	t.Run("passes jwt flags", func(t *testing.T) {
		spec := buildSecurityConfigSpec(standalone.StandaloneTopologyConfig{
			SecurityConfig: &seaweedv1.SecurityConfigSpec{
				JWTSigning: &seaweedv1.JWTSigningSpec{
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

func TestValidateTLSParameters(t *testing.T) {
	require.NoError(t, validateTLSParameters(standalone.StandaloneTopologyConfig{}))
	require.NoError(t, validateTLSParameters(standalone.StandaloneTopologyConfig{
		TLS: &seaweedv1.TLSSpec{Enabled: true},
	}))
	// Disabled TLS ignores a half-filled issuerRef left over from the UI.
	require.NoError(t, validateTLSParameters(standalone.StandaloneTopologyConfig{
		TLS: &seaweedv1.TLSSpec{
			Enabled:   false,
			IssuerRef: &seaweedv1.TLSIssuerRef{Kind: "Issuer"},
		},
	}))
	require.NoError(t, validateTLSParameters(standalone.StandaloneTopologyConfig{
		TLS: &seaweedv1.TLSSpec{
			Enabled: true,
			IssuerRef: &seaweedv1.TLSIssuerRef{
				Name: "my-issuer",
				Kind: "ClusterIssuer",
			},
		},
	}))

	err := validateTLSParameters(standalone.StandaloneTopologyConfig{
		TLS: &seaweedv1.TLSSpec{
			Enabled:   true,
			IssuerRef: &seaweedv1.TLSIssuerRef{Kind: "Issuer"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls.issuerRef.name is required")

	err = validateTLSParameters(standalone.StandaloneTopologyConfig{
		TLS: &seaweedv1.TLSSpec{
			Enabled: true,
			IssuerRef: &seaweedv1.TLSIssuerRef{
				Name: "x",
				Kind: "SomethingElse",
			},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Issuer or ClusterIssuer")
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
