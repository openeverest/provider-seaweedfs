// Package standalone contains custom spec types for the standalone topology.
//
// Add fields to StandaloneTopologyConfig and reference it via configSchema in
// topology.yaml when this topology needs custom configuration.
//
// +k8s:openapi-gen=true
package standalone

// StandaloneTopologyConfig defines configuration for the standalone topology.
// Add fields here when the standalone topology needs custom configuration
// beyond what the base Instance spec provides.
type StandaloneTopologyConfig struct {
	VolumeServerDiskCount *int32 `json:"volumeServerDiskCount,omitempty"`

	// Replication is SeaweedFS master.defaultReplication: three digits XYZ.
	// X is extra copies in other data centers, Y extra copies on other racks
	// in the same data center, Z extra copies on other volume servers in the
	// same rack.
	// Volume servers are not assigned a rack or data center, so X and Y must
	// be 0, and Z+1 must not exceed the volume server count (000, 001, 002, …).
	// Empty uses 001 when volume replicas are at least 2, and 000 otherwise.
	// +optional
	Replication *string `json:"replication,omitempty"`

	// TLS enables inter-component gRPC mTLS via the seaweedfs-operator.
	// This does not terminate HTTPS on the S3 gateway — see README.
	// +optional
	TLS *TLSSpec `json:"tls,omitempty"`

	// SecurityConfig configures JWT signing keys in the operator-rendered
	// security.toml. Independent of TLS; YAML/API only (no UI fields yet).
	// +optional
	SecurityConfig *SecurityConfigSpec `json:"securityConfig,omitempty"`

	// Admin runs the SeaweedFS admin UI when CredentialsSecret names an
	// existing Secret. This provider does not create that Secret.
	// +optional
	Admin *AdminConfig `json:"admin,omitempty"`
}

// AdminConfig points the admin server at a user-supplied credentials Secret.
type AdminConfig struct {
	// CredentialsSecret is the name of an existing Secret in the Instance
	// namespace. The operator reads adminUser and adminPassword, and
	// optionally readOnlyUser and readOnlyPassword.
	CredentialsSecret string `json:"credentialsSecret,omitempty"`
}

// TLSSpec mirrors seaweedfs-operator TLSSpec for inter-component gRPC mTLS.
type TLSSpec struct {
	// Enabled turns on mTLS. Defaults to false.
	Enabled bool `json:"enabled,omitempty"`

	// IssuerRef optionally references an existing cert-manager Issuer or
	// ClusterIssuer. When empty the operator provisions a self-signed CA.
	// +optional
	IssuerRef *TLSIssuerRef `json:"issuerRef,omitempty"`
}

// TLSIssuerRef mirrors seaweedfs-operator TLSIssuerRef.
type TLSIssuerRef struct {
	Name string `json:"name"`
	// Kind is Issuer (namespaced) or ClusterIssuer. Empty defaults to Issuer.
	// +optional
	Kind string `json:"kind,omitempty"`
	// Group defaults to cert-manager.io in the operator when empty.
	// +optional
	Group string `json:"group,omitempty"`
}

// SecurityConfigSpec mirrors seaweedfs-operator SecurityConfigSpec.
type SecurityConfigSpec struct {
	// +optional
	JWTSigning *JWTSigningSpec `json:"jwtSigning,omitempty"`
}

// JWTSigningSpec mirrors seaweedfs-operator JWTSigningSpec.
type JWTSigningSpec struct {
	// +optional
	VolumeWrite bool `json:"volumeWrite,omitempty"`
	// +optional
	VolumeRead bool `json:"volumeRead,omitempty"`
	// +optional
	FilerWrite bool `json:"filerWrite,omitempty"`
	// +optional
	FilerRead bool `json:"filerRead,omitempty"`
	// +optional
	ExpiresAfterSeconds *JWTExpiresAfterSecondsSpec `json:"expiresAfterSeconds,omitempty"`
}

// JWTExpiresAfterSecondsSpec mirrors seaweedfs-operator JWTExpiresAfterSecondsSpec.
type JWTExpiresAfterSecondsSpec struct {
	// +optional
	VolumeWrite int32 `json:"volumeWrite,omitempty"`
	// +optional
	VolumeRead int32 `json:"volumeRead,omitempty"`
	// +optional
	FilerWrite int32 `json:"filerWrite,omitempty"`
	// +optional
	FilerRead int32 `json:"filerRead,omitempty"`
}
