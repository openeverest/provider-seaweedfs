package provider

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/topologies/standalone"
)

// TLS server secret suffix used by seaweedfs-operator (see TLSServerSecretName).
const tlsServerSecretSuffix = "-server-tls"

func tlsServerSecretName(instanceName string) string {
	return instanceName + tlsServerSecretSuffix
}

// tlsSecretToInstance maps the operator/cert-manager server TLS Secret to its
// Instance. The Secret is owned by a cert-manager Certificate (not Seaweed), so
// we match by the well-known "<name>-server-tls" naming convention.
func tlsSecretToInstance(_ context.Context, obj client.Object) []reconcile.Request {
	name := obj.GetName()
	if !strings.HasSuffix(name, tlsServerSecretSuffix) {
		return nil
	}
	instanceName := strings.TrimSuffix(name, tlsServerSecretSuffix)
	if instanceName == "" {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Namespace: obj.GetNamespace(), Name: instanceName},
	}}
}

func buildTLSSpec(topo standalone.StandaloneTopologyConfig) *seaweedv1.TLSSpec {
	if topo.TLS == nil || !topo.TLS.Enabled {
		return nil
	}
	spec := &seaweedv1.TLSSpec{Enabled: true}
	if ref := topo.TLS.IssuerRef; ref != nil && ref.Name != "" {
		spec.IssuerRef = &seaweedv1.TLSIssuerRef{
			Name:  ref.Name,
			Kind:  ref.Kind,
			Group: ref.Group,
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
	return &seaweedv1.SecurityConfigSpec{
		JWTSigning: &seaweedv1.JWTSigningSpec{
			VolumeWrite:          jwt.VolumeWrite,
			VolumeRead:           jwt.VolumeRead,
			FilerWrite:           jwt.FilerWrite,
			FilerRead:            jwt.FilerRead,
			ExpiresAfterSeconds:  jwt.ExpiresAfterSeconds,
		},
	}
}

func validateTLSParameters(topo standalone.StandaloneTopologyConfig) error {
	if topo.TLS == nil || !topo.TLS.Enabled {
		return nil
	}
	if ref := topo.TLS.IssuerRef; ref != nil {
		if ref.Name == "" {
			return fmt.Errorf("tls.issuerRef.name is required when issuerRef is set")
		}
		switch ref.Kind {
		case "", "Issuer", "ClusterIssuer":
			// ok — empty kind defaults to Issuer in the operator
		default:
			return fmt.Errorf("tls.issuerRef.kind must be Issuer or ClusterIssuer")
		}
	}
	return nil
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
