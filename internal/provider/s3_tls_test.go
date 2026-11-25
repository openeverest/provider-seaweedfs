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
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func TestS3ServerDNSNames(t *testing.T) {
	assert.Equal(t, []string{
		"sw-s3", "sw-s3.ns", "sw-s3.ns.svc", "sw-s3.ns.svc.cluster.local", "s3.example.com",
	}, s3ServerDNSNames("sw", "ns", []string{"s3.example.com"}))
}

func TestEnsureManagedS3Certificate(t *testing.T) {
	names := s3ServerDNSNames("sw", "ns", nil)
	ctx := controllerContext(t, instanceWithParams(""))

	secretName, err := ensureS3ServerTLS(ctx, definition.TLSParameters{}, names)
	require.NoError(t, err)
	assert.Equal(t, "sw-s3-https", secretName)

	ca := &corev1.Secret{}
	require.NoError(t, ctx.Get(ca, "sw-s3-ca"))
	leaf := &corev1.Secret{}
	require.NoError(t, ctx.Get(leaf, "sw-s3-https"))
	assert.Equal(t, ca.Data[corev1.TLSCertKey], leaf.Data["ca.crt"])
	assert.Equal(t, "sw", leaf.OwnerReferences[0].Name)
	assertSignedFor(t, leaf, "sw-s3.ns.svc")
	first := leaf.Data[corev1.TLSCertKey]

	t.Run("same names keep the certificate", func(t *testing.T) {
		_, err := ensureS3ServerTLS(ctx, definition.TLSParameters{}, names)
		require.NoError(t, err)
		require.NoError(t, ctx.Get(leaf, "sw-s3-https"))
		assert.Equal(t, first, leaf.Data[corev1.TLSCertKey])
	})

	t.Run("new names reissue from the same CA", func(t *testing.T) {
		withExtra := s3ServerDNSNames("sw", "ns", []string{"s3.example.com"})
		_, err := ensureS3ServerTLS(ctx, definition.TLSParameters{}, withExtra)
		require.NoError(t, err)
		require.NoError(t, ctx.Get(leaf, "sw-s3-https"))
		assert.NotEqual(t, first, leaf.Data[corev1.TLSCertKey])
		assert.Equal(t, ca.Data[corev1.TLSCertKey], leaf.Data["ca.crt"])
		assertSignedFor(t, leaf, "s3.example.com")
	})

	t.Run("disabled serves no certificate", func(t *testing.T) {
		name, err := ensureS3ServerTLS(ctx, definition.TLSParameters{Mode: definition.TLSModeDisabled}, names)
		require.NoError(t, err)
		assert.Empty(t, name)
	})

	t.Run("certificateSecretName is passed through", func(t *testing.T) {
		name, err := ensureS3ServerTLS(ctx, definition.TLSParameters{CertificateSecretName: "mine"}, names)
		require.NoError(t, err)
		assert.Equal(t, "mine", name)
	})
}

func TestEnsureManagedS3CertificateKeepsLegacySelfSigned(t *testing.T) {
	names := s3ServerDNSNames("sw", "ns", nil)
	certPEM, keyPEM := selfSigned(t, names)
	legacy := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sw-s3-https", Namespace: "ns"},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{corev1.TLSCertKey: certPEM, corev1.TLSPrivateKeyKey: keyPEM},
	}
	ctx := controllerContext(t, instanceWithParams(""), legacy)

	_, err := ensureS3ServerTLS(ctx, definition.TLSParameters{}, names)
	require.NoError(t, err)
	sec := &corev1.Secret{}
	require.NoError(t, ctx.Get(sec, "sw-s3-https"))
	assert.Equal(t, certPEM, sec.Data[corev1.TLSCertKey], "an upgrade must not reissue and restart the gateway")
	assert.False(t, apierrorsIsFound(ctx, "sw-s3-ca"))

	ca, err := s3ClientCA(ctx, definition.TLSParameters{}, "sw-s3-https")
	require.NoError(t, err)
	assert.Equal(t, certPEM, ca, "a self-signed certificate is its own CA")
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(ca))
	cert, err := parseCertificate(certPEM)
	require.NoError(t, err)
	_, err = cert.Verify(x509.VerifyOptions{DNSName: "sw-s3.ns.svc", Roots: roots})
	require.NoError(t, err, "clients can verify the legacy certificate with ca.crt")
}

func TestS3ClientCA(t *testing.T) {
	names := s3ServerDNSNames("sw", "ns", nil)
	caPEM, _ := selfSigned(t, []string{"ca"})
	leafPEM, keyPEM := selfSigned(t, names)
	user := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "mine", Namespace: "ns"},
		Data:       map[string][]byte{corev1.TLSCertKey: leafPEM, corev1.TLSPrivateKeyKey: keyPEM},
	}
	withCA := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "mine-ca", Namespace: "ns"},
		Data:       map[string][]byte{corev1.TLSCertKey: leafPEM, corev1.TLSPrivateKeyKey: keyPEM, "ca.crt": caPEM},
	}
	ctx := controllerContext(t, instanceWithParams(""), user, withCA)

	ca, err := s3ClientCA(ctx, definition.TLSParameters{CertificateSecretName: "mine-ca"}, "mine-ca")
	require.NoError(t, err)
	assert.Equal(t, caPEM, ca)

	// The user's chain is theirs to publish: no guessing from tls.crt.
	ca, err = s3ClientCA(ctx, definition.TLSParameters{CertificateSecretName: "mine"}, "mine")
	require.NoError(t, err)
	assert.Nil(t, ca)

	ca, err = s3ClientCA(ctx, definition.TLSParameters{}, "missing")
	require.NoError(t, err)
	assert.Nil(t, ca)
}

func TestApplyS3Certificate(t *testing.T) {
	var applied *unstructured.Unstructured
	scheme := runtime.NewScheme()
	require.NoError(t, corev1alpha1.AddToScheme(scheme))
	require.NoError(t, seaweedv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	inst := instanceWithParams("")
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(inst).WithInterceptorFuncs(interceptor.Funcs{
		Apply: func(_ context.Context, _ client.WithWatch, obj runtime.ApplyConfiguration, _ ...client.ApplyOption) error {
			raw, err := json.Marshal(obj)
			require.NoError(t, err)
			applied = &unstructured.Unstructured{}
			return applied.UnmarshalJSON(raw)
		},
	}).Build()
	ctx := controller.NewContext(context.Background(), cl, inst, common.ProviderName)

	params := definition.TLSParameters{Issuer: &definition.IssuerReference{Name: "corp", Kind: "ClusterIssuer"}}
	name, err := ensureS3ServerTLS(ctx, params, s3ServerDNSNames("sw", "ns", []string{"s3.example.com"}))
	require.NoError(t, err)
	assert.Equal(t, "sw-s3-https", name)

	require.NotNil(t, applied)
	assert.Equal(t, "cert-manager.io/v1", applied.GetAPIVersion())
	assert.Equal(t, "Certificate", applied.GetKind())
	assert.Equal(t, "sw-s3-https", applied.GetName())
	assert.Equal(t, "sw", applied.GetOwnerReferences()[0].Name)
	spec := applied.Object["spec"].(map[string]any)
	assert.Equal(t, "sw-s3-https", spec["secretName"])
	assert.Equal(t, map[string]any{"name": "corp", "kind": "ClusterIssuer", "group": "cert-manager.io"}, spec["issuerRef"])
	assert.Equal(t, []any{"sw-s3", "sw-s3.ns", "sw-s3.ns.svc", "sw-s3.ns.svc.cluster.local", "s3.example.com"}, spec["dnsNames"])
	assert.False(t, apierrorsIsFound(ctx, "sw-s3-ca"), "an issuer needs no provider CA")
}

func TestWaitingForS3Certificate(t *testing.T) {
	complete := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ready", Namespace: "ns"},
		Data:       map[string][]byte{corev1.TLSCertKey: []byte("c"), corev1.TLSPrivateKeyKey: []byte("k")},
	}
	partial := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "partial", Namespace: "ns"},
		Data:       map[string][]byte{corev1.TLSCertKey: []byte("c")},
	}
	ctx := controllerContext(t, instanceWithParams(""), complete, partial)
	issuer := &definition.IssuerReference{Name: "corp"}

	tests := []struct {
		name    string
		params  definition.TLSParameters
		msg     string
		problem bool
	}{
		{name: "managed does not wait"},
		{name: "disabled does not wait", params: definition.TLSParameters{Mode: definition.TLSModeDisabled, CertificateSecretName: "missing"}},
		{name: "complete user secret", params: definition.TLSParameters{CertificateSecretName: "ready"}},
		{
			name:    "missing user secret is a problem",
			params:  definition.TLSParameters{CertificateSecretName: "missing"},
			msg:     `Waiting for S3 TLS Secret "missing" (tls.crt and tls.key)`,
			problem: true,
		},
		{
			name:    "user secret without key is a problem",
			params:  definition.TLSParameters{CertificateSecretName: "partial"},
			msg:     `Waiting for S3 TLS Secret "partial" (tls.crt and tls.key)`,
			problem: true,
		},
		{
			name:   "issuer waits for cert-manager",
			params: definition.TLSParameters{Issuer: issuer},
			msg:    `Waiting for cert-manager to issue the S3 certificate into Secret "sw-s3-https" (issuer Issuer "corp")`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, problem, err := waitingForS3Certificate(ctx, tt.params)
			require.NoError(t, err)
			assert.Equal(t, tt.msg, msg)
			assert.Equal(t, tt.problem, problem)
		})
	}
}

func apierrorsIsFound(ctx *controller.Context, name string) bool {
	found, err := ctx.Exists(&corev1.Secret{}, name)
	return err == nil && found
}

// assertSignedFor checks the Secret's certificate chains to its ca.crt for name.
func assertSignedFor(t *testing.T, sec *corev1.Secret, name string) {
	t.Helper()
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(sec.Data["ca.crt"]))
	leaf, err := parseCertificate(sec.Data[corev1.TLSCertKey])
	require.NoError(t, err)
	_, err = leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: roots})
	require.NoError(t, err)
	_, err = parsePrivateKey(sec.Data[corev1.TLSPrivateKeyKey])
	require.NoError(t, err)
}

// selfSigned makes a certificate like the ones releases up to 0.3 created.
func selfSigned(t *testing.T, names []string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: names[0]},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		DNSNames:              names,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyPEM, err = encodeKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), keyPEM
}
