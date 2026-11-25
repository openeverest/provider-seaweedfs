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
	"encoding/json"
	"net"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"

	"github.com/openeverest/provider-seaweedfs/definition"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

var tlsPath = field.NewPath("spec", "parameters", "tls")

// validateTLSParameters checks spec.parameters.tls alone and, on an update,
// against the old Instance. s3Ingress is whether the S3 Ingress is on.
func validateTLSParameters(instance, old *corev1alpha1.Instance, s3Ingress bool) field.ErrorList {
	params, err := decodeTLSParameters(instance)
	if err != nil {
		return field.ErrorList{field.Invalid(field.NewPath("spec", "parameters"), field.OmitValueType{}, err.Error())}
	}
	var errs field.ErrorList
	modes := []string{string(definition.TLSModeDisabled), string(definition.TLSModePrefer), string(definition.TLSModeRequire)}
	if params.Mode != "" && !slices.Contains(modes, string(params.Mode)) {
		errs = append(errs, field.NotSupported(tlsPath.Child("mode"), params.Mode, modes))
	}
	if tlsMode(params) == definition.TLSModeRequire && s3Ingress {
		errs = append(errs, field.Forbidden(tlsPath.Child("mode"),
			"requireTLS turns off the S3 HTTP listener the Ingress routes to: use preferTLS with an S3 Ingress"))
	}
	if !s3TLSEnabled(params) {
		if params.CertificateSecretName != "" {
			errs = append(errs, field.Forbidden(tlsPath.Child("certificateSecretName"), "needs mode preferTLS or requireTLS"))
		}
		if len(params.AdditionalNames) > 0 {
			errs = append(errs, field.Forbidden(tlsPath.Child("additionalNames"), "needs mode preferTLS or requireTLS"))
		}
	}
	if name := params.CertificateSecretName; name != "" {
		for _, msg := range validation.IsDNS1123Subdomain(name) {
			errs = append(errs, field.Invalid(tlsPath.Child("certificateSecretName"), name, msg))
		}
		if len(params.AdditionalNames) > 0 {
			errs = append(errs, field.Forbidden(tlsPath.Child("additionalNames"),
				"can't be used with certificateSecretName: that certificate carries its own names"))
		}
	}
	if params.Issuer != nil && !issuerSigns(params) {
		errs = append(errs, field.Forbidden(tlsPath.Child("issuer"),
			"signs nothing: it needs internalTLS, or S3 TLS without certificateSecretName"))
	}
	errs = append(errs, validateIssuer(params)...)
	errs = append(errs, validateAdditionalNames(params.AdditionalNames)...)
	if len(errs) > 0 || old == nil || legacyTLSPath(old) != nil {
		// An Instance still in the old shape is migrating: there is nothing
		// in the new shape to compare with.
		return errs
	}
	oldParams, err := decodeTLSParameters(old)
	if err != nil {
		return errs
	}
	if s3TLSEnabled(oldParams) && !s3TLSEnabled(params) {
		errs = append(errs, field.Forbidden(tlsPath.Child("mode"),
			"disabled can only be set when the Instance is created: S3 clients and BackupStorages use HTTPS"))
	}
	if s3TLSEnabled(oldParams) && s3TLSEnabled(params) && s3SourceKey(oldParams) != s3SourceKey(params) {
		path := tlsPath.Child("issuer")
		if oldParams.CertificateSecretName != params.CertificateSecretName {
			path = tlsPath.Child("certificateSecretName")
		}
		errs = append(errs, field.Forbidden(path,
			"the S3 certificate source can only be chosen when TLS is turned on: clients trust its CA"))
	}
	if oldParams.InternalTLS && params.InternalTLS && issuerKey(oldParams.Issuer) != issuerKey(params.Issuer) {
		errs = append(errs, field.Forbidden(tlsPath.Child("issuer"),
			"can't change while internalTLS is on: components with certificates from another CA can't talk"))
	}
	return errs
}

// legacyTLSPath returns where the Instance still has TLS in the fields
// provider versions up to 0.3 read, or nil. The schema check rejects those
// fields on a new object; an old object can still have them.
func legacyTLSPath(instance *corev1alpha1.Instance) *field.Path {
	if instance.Spec.Topology != nil && hasKey(instance.Spec.Topology.Parameters, "tls") {
		return field.NewPath("spec", "topology", "parameters", "tls")
	}
	if s3, ok := instance.Spec.Components[common.ComponentS3]; ok && hasKey(s3.Parameters, "https") {
		return componentsPath.Key(common.ComponentS3).Child("parameters", "https")
	}
	return nil
}

func hasKey(raw *runtime.RawExtension, key string) bool {
	if raw == nil || len(raw.Raw) == 0 {
		return false
	}
	var params map[string]json.RawMessage
	if json.Unmarshal(raw.Raw, &params) != nil {
		return false
	}
	_, ok := params[key]
	return ok
}

func issuerKey(ref *definition.IssuerReference) string {
	if ref == nil {
		return ""
	}
	return issuerGroup(*ref) + "/" + issuerKind(*ref) + "/" + ref.Name
}

// s3SourceKey identifies where the S3 certificate comes from.
func s3SourceKey(params definition.TLSParameters) string {
	switch s3CertificateSource(params) {
	case sourceSecret:
		return "secret/" + params.CertificateSecretName
	case sourceIssuer:
		return "issuer/" + issuerKey(params.Issuer)
	default:
		return "managed"
	}
}

func validateIssuer(params definition.TLSParameters) field.ErrorList {
	ref := params.Issuer
	if ref == nil {
		return nil
	}
	path := tlsPath.Child("issuer")
	var errs field.ErrorList
	if ref.Name == "" {
		errs = append(errs, field.Required(path.Child("name"), "the name of the cert-manager issuer"))
	}
	kinds := []string{string(definition.IssuerKindIssuer), string(definition.IssuerKindClusterIssuer)}
	switch {
	case issuerGroup(*ref) == certManagerGroup && !slices.Contains(kinds, issuerKind(*ref)):
		errs = append(errs, field.NotSupported(path.Child("kind"), ref.Kind, kinds))
	case params.InternalTLS && !slices.Contains(kinds, issuerKind(*ref)):
		errs = append(errs, field.Invalid(path.Child("kind"), ref.Kind,
			"seaweedfs-operator signs the internalTLS certificates only with an Issuer or ClusterIssuer"))
	}
	return errs
}

// validateAdditionalNames accepts DNS names, with an optional leading
// wildcard label. IP addresses are not offered.
func validateAdditionalNames(names []string) field.ErrorList {
	var errs field.ErrorList
	for i, name := range names {
		path := tlsPath.Child("additionalNames").Index(i)
		if net.ParseIP(name) != nil {
			errs = append(errs, field.Invalid(path, name, "must be a DNS name, not an IP address"))
			continue
		}
		msgs := validation.IsDNS1123Subdomain(name)
		if strings.HasPrefix(name, "*.") {
			msgs = validation.IsWildcardDNS1123Subdomain(name)
		}
		if strings.Contains(name, ",") {
			msgs = append(msgs, "must not contain a comma")
		}
		for _, msg := range msgs {
			errs = append(errs, field.Invalid(path, name, msg))
		}
	}
	return errs
}
