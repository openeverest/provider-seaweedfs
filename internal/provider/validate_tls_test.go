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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"

	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func TestValidateTLSParametersCreate(t *testing.T) {
	tests := []struct {
		name      string
		params    string
		ingress   bool
		expectErr string
	}{
		{name: "defaults"},
		{name: "disabled", params: `{"tls":{"mode":"disabled"}}`},
		{name: "internalTLS with disabled S3 TLS", params: `{"tls":{"mode":"disabled","internalTLS":true}}`},
		{name: "requireTLS", params: `{"tls":{"mode":"requireTLS"}}`},
		{name: "preferTLS with Ingress", params: `{"tls":{"mode":"preferTLS"}}`, ingress: true},
		{
			name:   "issuer with additional names",
			params: `{"tls":{"issuer":{"name":"corp","kind":"ClusterIssuer"},"additionalNames":["s3.example.com","*.s3.example.com"]}}`,
		},
		{name: "secret and issuer for internalTLS", params: `{"tls":{"certificateSecretName":"mine","issuer":{"name":"corp"},"internalTLS":true}}`},
		{name: "empty UI issuer is ignored", params: `{"tls":{"mode":"disabled","issuer":{"name":""}}}`},
		{name: "foreign issuer for S3", params: `{"tls":{"issuer":{"name":"x","kind":"AWSPCAIssuer","group":"awspca.cert-manager.io"}}}`},
		{
			name:      "unknown mode",
			params:    `{"tls":{"mode":"on"}}`,
			expectErr: `spec.parameters.tls.mode: Unsupported value: "on"`,
		},
		{
			name:      "requireTLS with Ingress",
			params:    `{"tls":{"mode":"requireTLS"}}`,
			ingress:   true,
			expectErr: "spec.parameters.tls.mode: Forbidden: requireTLS turns off the S3 HTTP listener",
		},
		{
			name:      "certificateSecretName when disabled",
			params:    `{"tls":{"mode":"disabled","certificateSecretName":"mine"}}`,
			expectErr: "spec.parameters.tls.certificateSecretName: Forbidden: needs mode preferTLS or requireTLS",
		},
		{
			name:      "additionalNames when disabled",
			params:    `{"tls":{"mode":"disabled","additionalNames":["a.example.com"]}}`,
			expectErr: "spec.parameters.tls.additionalNames: Forbidden: needs mode preferTLS or requireTLS",
		},
		{
			name:      "invalid certificateSecretName",
			params:    `{"tls":{"certificateSecretName":"Not_A_Name"}}`,
			expectErr: `spec.parameters.tls.certificateSecretName: Invalid value: "Not_A_Name"`,
		},
		{
			name:      "additionalNames with certificateSecretName",
			params:    `{"tls":{"certificateSecretName":"mine","additionalNames":["a.example.com"]}}`,
			expectErr: "spec.parameters.tls.additionalNames: Forbidden: can't be used with certificateSecretName",
		},
		{
			name:      "IP in additionalNames",
			params:    `{"tls":{"additionalNames":["10.0.0.1"]}}`,
			expectErr: `spec.parameters.tls.additionalNames[0]: Invalid value: "10.0.0.1": must be a DNS name`,
		},
		{
			name:      "comma in additionalNames",
			params:    `{"tls":{"additionalNames":["ok.example.com","a.com,b.com"]}}`,
			expectErr: `spec.parameters.tls.additionalNames[1]: Invalid value: "a.com,b.com"`,
		},
		{
			name:      "issuer without name",
			params:    `{"tls":{"issuer":{"kind":"ClusterIssuer"}}}`,
			expectErr: "spec.parameters.tls.issuer.name: Required value",
		},
		{
			name:      "unsupported cert-manager kind",
			params:    `{"tls":{"issuer":{"name":"x","kind":"Vault"}}}`,
			expectErr: `spec.parameters.tls.issuer.kind: Unsupported value: "Vault"`,
		},
		{
			name:      "foreign issuer for internalTLS",
			params:    `{"tls":{"internalTLS":true,"issuer":{"name":"x","kind":"AWSPCAIssuer","group":"awspca.cert-manager.io"}}}`,
			expectErr: "spec.parameters.tls.issuer.kind: Invalid value: \"AWSPCAIssuer\": seaweedfs-operator signs",
		},
		{
			name:      "issuer that signs nothing",
			params:    `{"tls":{"certificateSecretName":"mine","issuer":{"name":"corp"}}}`,
			expectErr: "spec.parameters.tls.issuer: Forbidden: signs nothing",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateTLSParameters(instanceWithParams(tt.params), nil, tt.ingress)
			if tt.expectErr == "" {
				require.Empty(t, errs)
				return
			}
			require.NotEmpty(t, errs)
			assert.Contains(t, errs.ToAggregate().Error(), tt.expectErr)
		})
	}
}

func TestValidateTLSParametersUpdate(t *testing.T) {
	tests := []struct {
		name      string
		old, new  string
		expectErr string
	}{
		{name: "unchanged", old: `{"tls":{"issuer":{"name":"corp"}}}`, new: `{"tls":{"issuer":{"name":"corp"}}}`},
		{name: "default to requireTLS", new: `{"tls":{"mode":"requireTLS"}}`},
		{name: "disabled to preferTLS", old: `{"tls":{"mode":"disabled"}}`, new: `{"tls":{"mode":"preferTLS"}}`},
		{name: "names change", old: `{"tls":{"additionalNames":["a.example.com"]}}`, new: `{"tls":{"additionalNames":["b.example.com"]}}`},
		{name: "turn internalTLS on", new: `{"tls":{"internalTLS":true}}`},
		{
			name: "issuer can be set when S3 TLS turns on",
			old:  `{"tls":{"mode":"disabled"}}`,
			new:  `{"tls":{"issuer":{"name":"corp"}}}`,
		},
		{
			name:      "TLS to disabled",
			new:       `{"tls":{"mode":"disabled"}}`,
			expectErr: "spec.parameters.tls.mode: Forbidden: disabled can only be set when the Instance is created",
		},
		{
			name:      "managed to certificateSecretName",
			new:       `{"tls":{"certificateSecretName":"mine"}}`,
			expectErr: "spec.parameters.tls.certificateSecretName: Forbidden: the S3 certificate source",
		},
		{
			name:      "certificateSecretName renamed",
			old:       `{"tls":{"certificateSecretName":"mine"}}`,
			new:       `{"tls":{"certificateSecretName":"other"}}`,
			expectErr: "spec.parameters.tls.certificateSecretName: Forbidden: the S3 certificate source",
		},
		{
			name:      "S3 issuer changed",
			old:       `{"tls":{"issuer":{"name":"corp"}}}`,
			new:       `{"tls":{"issuer":{"name":"corp","kind":"ClusterIssuer"}}}`,
			expectErr: "spec.parameters.tls.issuer: Forbidden: the S3 certificate source",
		},
		{
			name:      "internal issuer changed",
			old:       `{"tls":{"mode":"disabled","internalTLS":true,"issuer":{"name":"a"}}}`,
			new:       `{"tls":{"mode":"disabled","internalTLS":true,"issuer":{"name":"b"}}}`,
			expectErr: "spec.parameters.tls.issuer: Forbidden: can't change while internalTLS is on",
		},
		{
			name: "an Instance in the old shape migrates freely",
			new:  `{"tls":{"mode":"disabled"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := instanceWithParams(tt.old)
			if tt.name == "an Instance in the old shape migrates freely" {
				old.Spec.Components = map[string]corev1alpha1.ComponentSpec{
					common.ComponentS3: {Parameters: &runtime.RawExtension{Raw: []byte(`{"https":{"enabled":false}}`)}},
				}
			}
			errs := validateTLSParameters(instanceWithParams(tt.new), old, false)
			if tt.expectErr == "" {
				require.Empty(t, errs)
				return
			}
			require.NotEmpty(t, errs)
			assert.Contains(t, errs.ToAggregate().Error(), tt.expectErr)
		})
	}
}

func TestLegacyTLSPath(t *testing.T) {
	inst := instanceWithParams("")
	inst.Spec.Topology = &corev1alpha1.TopologySpec{
		Type:       "standalone",
		Parameters: &runtime.RawExtension{Raw: []byte(`{"tls":{"enabled":true}}`)},
	}
	assert.Equal(t, "spec.topology.parameters.tls", legacyTLSPath(inst).String())

	inst.Spec.Topology.Parameters = &runtime.RawExtension{Raw: []byte(`{"securityConfig":{}}`)}
	assert.Nil(t, legacyTLSPath(inst))
}
