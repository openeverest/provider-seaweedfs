// Package standalone contains custom spec types for the standalone topology.
//
// Add fields to StandaloneTopologyConfig and reference it via configSchema in
// topology.yaml when this topology needs custom configuration.
//
// +k8s:openapi-gen=true
package standalone

import (
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"
)

// StandaloneTopologyConfig defines configuration for the standalone topology.
// Add fields here when the standalone topology needs custom configuration
// beyond what the base Instance spec provides.
type StandaloneTopologyConfig struct {
	VolumeServerDiskCount *int32 `json:"volumeServerDiskCount,omitempty"`

	// TLS enables inter-component gRPC mTLS via the seaweedfs-operator.
	// This does not terminate HTTPS on the S3 gateway — see README.
	// +optional
	TLS *seaweedv1.TLSSpec `json:"tls,omitempty"`

	// SecurityConfig configures JWT signing keys in the operator-rendered
	// security.toml. Independent of TLS.
	// +optional
	SecurityConfig *seaweedv1.SecurityConfigSpec `json:"securityConfig,omitempty"`
}
