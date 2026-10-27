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
