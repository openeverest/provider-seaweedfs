// Package definition contains shared types used across the provider definition.
//
// +k8s:openapi-gen=true
package definition

// Parameters defines instance-wide parameters (Instance.spec.parameters).
type Parameters struct {
	// TLS configures encryption in transit: HTTPS on the S3 gateway, and
	// optionally mutual TLS between the SeaweedFS components.
	// +optional
	TLS *TLSParameters `json:"tls,omitempty"`
}

// TLSMode controls whether the S3 gateway serves HTTPS and whether it also
// serves plain HTTP.
// +enum
type TLSMode string

const (
	// TLSModeDisabled serves S3 over plain HTTP only. It can only be chosen
	// when the Instance is created.
	TLSModeDisabled TLSMode = "disabled"
	// TLSModePrefer serves S3 over HTTPS. With an S3 Ingress the gateway also
	// serves plain HTTP, which the Ingress routes to. The default.
	TLSModePrefer TLSMode = "preferTLS"
	// TLSModeRequire serves S3 over HTTPS only. Not supported with an S3
	// Ingress, which routes to the HTTP listener.
	TLSModeRequire TLSMode = "requireTLS"
)

// TLSParameters configures TLS for the instance. The S3 server certificate
// comes from, in order of precedence: certificateSecretName, the issuer, or a
// CA the provider creates for the instance. The certificate source is chosen
// when TLS is turned on and can't be changed afterwards.
type TLSParameters struct {
	// Mode is disabled, preferTLS or requireTLS. Defaults to preferTLS.
	// disabled can only be set when the Instance is created.
	// +default="preferTLS"
	// +optional
	Mode TLSMode `json:"mode,omitempty"`

	// CertificateSecretName is an existing Secret with the S3 server
	// certificate (tls.crt, tls.key, and ca.crt for clients to verify it).
	// +optional
	CertificateSecretName string `json:"certificateSecretName,omitempty"`

	// Issuer is the cert-manager issuer that signs the S3 server certificate,
	// unless certificateSecretName is set, and the component certificates
	// when internalTLS is on. It must put its CA into the certificate Secrets
	// (ca.crt), as CA and Vault issuers do.
	// +optional
	Issuer *IssuerReference `json:"issuer,omitempty"`

	// AdditionalNames are DNS names the S3 server certificate is valid for
	// besides those of the S3 Service, such as an external name clients
	// connect to. Changing them on a running instance reissues the
	// certificate and restarts the S3 gateway, after maintenance approval.
	// +optional
	AdditionalNames []string `json:"additionalNames,omitempty"`

	// InternalTLS turns on mutual TLS for gRPC between master, volume, filer
	// and S3. It needs cert-manager. The certificates are signed by the issuer
	// when set, and by a CA seaweedfs-operator creates otherwise.
	// +optional
	InternalTLS bool `json:"internalTLS,omitempty"`
}

// IssuerKind is the kind of a cert-manager issuer.
// +enum
type IssuerKind string

const (
	// IssuerKindIssuer is an Issuer in the instance namespace.
	IssuerKindIssuer IssuerKind = "Issuer"
	// IssuerKindClusterIssuer is a cluster-wide ClusterIssuer.
	IssuerKindClusterIssuer IssuerKind = "ClusterIssuer"
)

// IssuerReference refers to a cert-manager issuer.
type IssuerReference struct {
	// Name of the issuer.
	Name string `json:"name"`

	// Kind of the issuer: Issuer or ClusterIssuer, or the kind of an external
	// issuer of group.
	// +default="Issuer"
	// +optional
	Kind IssuerKind `json:"kind,omitempty"`

	// Group is the API group of an external issuer, such as
	// awspca.cert-manager.io. External issuers sign only the S3 certificate:
	// seaweedfs-operator accepts Issuer and ClusterIssuer for the component
	// certificates.
	// +default="cert-manager.io"
	// +optional
	Group string `json:"group,omitempty"`
}
