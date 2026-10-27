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
	"crypto/tls"
	"crypto/x509"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateS3ServerCert(t *testing.T) {
	certPEM, keyPEM, err := generateS3ServerCert("sw", "ns")
	require.NoError(t, err)

	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	parsed, err := x509.ParseCertificate(pair.Certificate[0])
	require.NoError(t, err)
	assert.Equal(t, "sw-s3.ns.svc", parsed.Subject.CommonName)
	assert.ElementsMatch(t, []string{
		"sw-s3",
		"sw-s3.ns",
		"sw-s3.ns.svc",
		"sw-s3.ns.svc.cluster.local",
	}, parsed.DNSNames)
}
