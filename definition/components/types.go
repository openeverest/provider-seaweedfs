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

	// HTTPS serves the S3 gateway over TLS (seaweedfs-operator httpsPort and
	// tlsSecret). Nil, or enabled left unset, means HTTPS-only on port 8334.
	// Set enabled to false for plain HTTP. An Ingress keeps the HTTP listener:
	// the operator rejects HTTPS-only together with Ingress.
	// +optional
	HTTPS *S3HTTPSSpec `json:"https,omitempty"`

	// Ingress exposes the S3 gateway via a Kubernetes Ingress. With TLS,
	// clients reach the API at https://<host>.
	// +optional
	Ingress *S3IngressSpec `json:"ingress,omitempty"`
}

// S3HTTPSSpec is native TLS on the standalone S3 gateway.
type S3HTTPSSpec struct {
	// Enabled defaults to true. Set false to serve plain HTTP on port.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// SecretName is an existing kubernetes.io/tls Secret (tls.crt and tls.key).
	// Empty means this provider creates Secret <instance>-s3-https.
	// +optional
	SecretName string `json:"secretName,omitempty"`

	// VerifyTLS is the hint for clients of the in-cluster HTTPS endpoint.
	// Defaults to false for the provider-created certificate, and true when
	// secretName is set.
	// +optional
	VerifyTLS *bool `json:"verifyTLS,omitempty"`
}

// S3IngressSpec mirrors seaweedfs-operator IngressSpec for the standalone S3 gateway.
type S3IngressSpec struct {
	// Enabled turns on Ingress generation. Optional when Host is set: the UI
	// toggleable Ingress group omits this field and only sends nested values.
	Enabled bool `json:"enabled,omitempty"`

	// Host is the hostname the Ingress listens on. Required when Ingress is on;
	// a non-empty host also enables Ingress when Enabled is omitted.
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
