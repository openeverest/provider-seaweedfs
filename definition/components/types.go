// Package components contains custom spec types for provider component types.
//
// Each struct here corresponds to a component type defined in versions.yaml
// and is converted to an OpenAPI schema during generation.
// Add fields when a component type needs custom configuration beyond
// what the base Instance spec provides.
//
// +k8s:openapi-gen=true
package components


type MasterCustomSpec struct {
	MasterVolumeSizeLimitMB *int32 `json:"masterVolumeSizeLimitMB,omitempty"`
}

type VolumeCustomSpec struct {
	MaxVolumeCounts *int32 `json:"maxVolumeCounts,omitempty"`
}

type FilerCustomSpec struct {
	MaxMB *int32 `json:"maxMB,omitempty"`
}

type S3CustomSpec struct {
	Port       *int32  `json:"port,omitempty"`
	DomainName *string `json:"domainName,omitempty"`

	// Ingress exposes the S3 gateway via a Kubernetes Ingress. With TLS,
	// clients reach the API at https://<host> the in-cluster Service stays HTTP.
	// +optional
	Ingress *S3IngressSpec `json:"ingress,omitempty"`
}

// S3IngressSpec mirrors seaweedfs-operator IngressSpec for the standalone S3 gateway.
type S3IngressSpec struct {
	// Enabled turns on Ingress generation. Host is required when enabled.
	Enabled bool `json:"enabled,omitempty"`

	// Host is the hostname the Ingress listens on.
	Host string `json:"host,omitempty"`

	// ClassName is the IngressClassName (e.g. nginx).
	// +optional
	ClassName *string `json:"className,omitempty"`

	// Annotations applied to the Ingress (cert-manager issuer, nginx body size, …).
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// TLS terminates HTTPS at the Ingress controller.
	// +optional
	TLS *S3IngressTLS `json:"tls,omitempty"`
}

// S3IngressTLS configures Ingress TLS termination for the S3 hostname.
type S3IngressTLS struct {
	// SecretName is the kubernetes.io/tls Secret holding the certificate.
	// Create it by hand or via cert-manager (set an issuer annotation on the Ingress).
	SecretName string `json:"secretName"`

	// VerifyTLS is the hint for clients using the HTTPS external endpoint.
	// Defaults to true when TLS is configured. Set false for self-signed certs.
	// +optional
	VerifyTLS *bool `json:"verifyTLS,omitempty"`
}
