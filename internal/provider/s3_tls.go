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
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-seaweedfs/definition"
)

const (
	s3HTTPSSecretSuffix = "-s3-https"
	s3CASecretSuffix    = "-s3-ca"

	// seaweedfs-operator mounts tls.crt and tls.key and does not issue them.
	// A long lifetime avoids rotating a Secret the gateway re-reads on its
	// own schedule (default 5 hours).
	s3HTTPSCertLifetime = 10 * 365 * 24 * time.Hour

	// The CA outlives every certificate it signs.
	s3CALifetime = 2 * s3HTTPSCertLifetime
)

var certificateGVK = schema.GroupVersionKind{Group: certManagerGroup, Version: "v1", Kind: "Certificate"}

func s3HTTPSSecretName(instanceName string) string {
	return instanceName + s3HTTPSSecretSuffix
}

func s3CASecretName(instanceName string) string {
	return instanceName + s3CASecretSuffix
}

// s3TLSSecretName is the Secret the S3 gateway serves HTTPS with, or "" when
// TLS is disabled.
func s3TLSSecretName(instanceName string, params definition.TLSParameters) string {
	if !s3TLSEnabled(params) {
		return ""
	}
	if s3CertificateSource(params) == sourceSecret {
		return params.CertificateSecretName
	}
	return s3HTTPSSecretName(instanceName)
}

// s3ServerDNSNames are the names of the S3 Service, then the additional ones.
func s3ServerDNSNames(instanceName, namespace string, additional []string) []string {
	host := s3ServiceName(instanceName)
	svc := host + "." + namespace + ".svc"
	return append([]string{host, host + "." + namespace, svc, svc + ".cluster.local"}, additional...)
}

// ensureS3ServerTLS makes sure the S3 server certificate exists and returns
// the Secret to set on spec.s3.tlsSecret, or "" when TLS is disabled.
//   - certificateSecretName is passed through; Status waits for the Secret.
//   - issuer: a cert-manager Certificate writes Secret <instance>-s3-https.
//   - otherwise the provider signs Secret <instance>-s3-https with its CA.
func ensureS3ServerTLS(c *controller.Context, params definition.TLSParameters, names []string) (string, error) {
	if !s3TLSEnabled(params) {
		return "", nil
	}
	switch s3CertificateSource(params) {
	case sourceSecret:
		return params.CertificateSecretName, nil
	case sourceIssuer:
		return s3HTTPSSecretName(c.Name()), applyS3Certificate(c, *params.Issuer, names)
	default:
		return ensureManagedS3Certificate(c, names)
	}
}

func applyS3Certificate(c *controller.Context, ref definition.IssuerReference, names []string) error {
	name := s3HTTPSSecretName(c.Name())
	meta := c.ObjectMeta(name)
	cert := &unstructured.Unstructured{}
	cert.SetGroupVersionKind(certificateGVK)
	cert.SetName(meta.Name)
	cert.SetNamespace(meta.Namespace)
	cert.SetLabels(meta.Labels)
	dnsNames := make([]any, 0, len(names))
	for _, n := range names {
		dnsNames = append(dnsNames, n)
	}
	cert.Object["spec"] = map[string]any{
		"secretName": name,
		"dnsNames":   dnsNames,
		"issuerRef": map[string]any{
			"name":  ref.Name,
			"kind":  issuerKind(ref),
			"group": issuerGroup(ref),
		},
		"privateKey": map[string]any{
			"algorithm":      "ECDSA",
			"size":           int64(256),
			"rotationPolicy": "Always",
		},
		"usages": []any{"server auth", "digital signature", "key encipherment"},
	}
	if err := c.Apply(cert); err != nil {
		return fmt.Errorf("applying cert-manager Certificate %q (is cert-manager installed?): %w", name, err)
	}
	return nil
}

// ensureManagedS3Certificate keeps Secret <instance>-s3-https valid for
// names, signed by the CA in Secret <instance>-s3-ca. A certificate that
// already carries the names is kept, including the self-signed ones earlier
// releases created.
func ensureManagedS3Certificate(c *controller.Context, names []string) (string, error) {
	name := s3HTTPSSecretName(c.Name())
	existing := &corev1.Secret{}
	found, err := c.Exists(existing, name)
	if err != nil {
		return "", err
	}
	if found {
		if len(existing.Data[corev1.TLSCertKey]) == 0 || len(existing.Data[corev1.TLSPrivateKeyKey]) == 0 {
			return "", fmt.Errorf("S3 TLS Secret %q is missing tls.crt or tls.key", name)
		}
		served, err := parseCertificate(existing.Data[corev1.TLSCertKey])
		if err != nil {
			return "", fmt.Errorf("reading the certificate in Secret %q: %w", name, err)
		}
		if sameNames(served.DNSNames, names) {
			return name, nil
		}
	}

	caCert, caKey, err := ensureS3CA(c)
	if err != nil {
		return "", err
	}
	certPEM, keyPEM, err := issueS3ServerCert(caCert, caKey, names)
	if err != nil {
		return "", fmt.Errorf("issuing the S3 TLS certificate: %w", err)
	}
	data := map[string][]byte{
		corev1.TLSCertKey:       certPEM,
		corev1.TLSPrivateKeyKey: keyPEM,
		tlsKeyCA:                pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw}),
	}
	if found {
		existing.Data = data
		if err := c.Client().Update(c.Context(), existing); err != nil {
			return "", fmt.Errorf("updating S3 TLS Secret %q: %w", name, err)
		}
		return name, nil
	}
	if err := createOwnedSecret(c, name, data); err != nil {
		return "", fmt.Errorf("creating S3 TLS Secret %q: %w", name, err)
	}
	return name, nil
}

// ensureS3CA returns the instance's S3 CA, creating it on first use.
func ensureS3CA(c *controller.Context) (*x509.Certificate, crypto.Signer, error) {
	name := s3CASecretName(c.Name())
	sec := &corev1.Secret{}
	found, err := c.Exists(sec, name)
	if err != nil {
		return nil, nil, err
	}
	if found {
		cert, err := parseCertificate(sec.Data[corev1.TLSCertKey])
		if err != nil {
			return nil, nil, fmt.Errorf("reading the CA in Secret %q: %w", name, err)
		}
		key, err := parsePrivateKey(sec.Data[corev1.TLSPrivateKeyKey])
		if err != nil {
			return nil, nil, fmt.Errorf("reading the CA key in Secret %q: %w", name, err)
		}
		return cert, key, nil
	}
	certPEM, keyPEM, err := generateCA(c.Name())
	if err != nil {
		return nil, nil, fmt.Errorf("generating the S3 CA: %w", err)
	}
	if err := createOwnedSecret(c, name, map[string][]byte{
		corev1.TLSCertKey:       certPEM,
		corev1.TLSPrivateKeyKey: keyPEM,
	}); err != nil {
		return nil, nil, fmt.Errorf("creating S3 CA Secret %q: %w", name, err)
	}
	cert, _ := parseCertificate(certPEM)
	key, _ := parsePrivateKey(keyPEM)
	return cert, key, nil
}

func createOwnedSecret(c *controller.Context, name string, data map[string][]byte) error {
	secret := &corev1.Secret{
		ObjectMeta: c.ObjectMeta(name),
		Type:       corev1.SecretTypeTLS,
		Data:       data,
	}
	if err := controllerutil.SetControllerReference(c.Instance(), secret, c.Client().Scheme()); err != nil {
		return err
	}
	if err := c.Client().Create(c.Context(), secret); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Another reconcile won the race; the next one reads it.
			return fmt.Errorf("secret %q was created concurrently, retrying", name)
		}
		return err
	}
	return nil
}

// waitingForS3Certificate explains why the S3 gateway can't start yet: the
// certificate Secret the user named, or the one cert-manager writes, is
// missing or lacks tls.crt/tls.key. Without this the S3 pods sit in
// ContainerCreating and the Instance only shows a generic not-ready message.
func waitingForS3Certificate(c *controller.Context, params definition.TLSParameters) (msg string, problem bool, err error) {
	if !s3TLSEnabled(params) {
		return "", false, nil
	}
	source := s3CertificateSource(params)
	if source == sourceManaged {
		return "", false, nil
	}
	name := s3TLSSecretName(c.Name(), params)
	sec := &corev1.Secret{}
	found, err := c.Exists(sec, name)
	if err != nil {
		return "", false, err
	}
	if found && len(sec.Data[corev1.TLSCertKey]) > 0 && len(sec.Data[corev1.TLSPrivateKeyKey]) > 0 {
		return "", false, nil
	}
	if source == sourceSecret {
		return fmt.Sprintf("Waiting for S3 TLS Secret %q (tls.crt and tls.key)", name), true, nil
	}
	return fmt.Sprintf("Waiting for cert-manager to issue the S3 certificate into Secret %q (issuer %s %q)",
		name, issuerKind(*params.Issuer), params.Issuer.Name), false, nil
}

// s3ClientCA returns the CA clients verify the S3 gateway with: ca.crt of
// the certificate Secret. The self-signed certificates of earlier releases
// have none and are their own trust anchor.
func s3ClientCA(c *controller.Context, params definition.TLSParameters, secretName string) ([]byte, error) {
	if secretName == "" {
		return nil, nil
	}
	sec := &corev1.Secret{}
	found, err := c.Exists(sec, secretName)
	if err != nil || !found {
		return nil, err
	}
	if ca := sec.Data[tlsKeyCA]; len(ca) > 0 {
		return ca, nil
	}
	if s3CertificateSource(params) != sourceManaged {
		return nil, nil
	}
	cert, err := parseCertificate(sec.Data[corev1.TLSCertKey])
	if err != nil || !selfSignedCert(cert) {
		return nil, nil //nolint:nilerr // Not self-signed: no CA to publish.
	}
	return sec.Data[corev1.TLSCertKey], nil
}

// selfSignedCert reports whether cert signed itself. The leaf certificates of
// earlier releases are not CAs, so CheckSignatureFrom would refuse them.
func selfSignedCert(cert *x509.Certificate) bool {
	return bytes.Equal(cert.RawIssuer, cert.RawSubject) &&
		cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature) == nil
}

func sameNames(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}

func parseCertificate(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("no PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parsePrivateKey(keyPEM []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("no PEM private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, errors.New("private key can't sign")
	}
	return signer, nil
}

func newSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

func encodeKey(key *ecdsa.PrivateKey) ([]byte, error) {
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}), nil
}

func generateCA(instanceName string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: instanceName + " S3 CA", Organization: []string{"OpenEverest"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(s3CALifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err = encodeKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), keyPEM, nil
}

// issueS3ServerCert signs a server certificate for names with the CA. The
// first names are the S3 Service's; its FQDN without the cluster domain is
// the common name.
func issueS3ServerCert(ca *x509.Certificate, caKey crypto.Signer, names []string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: names[2]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(s3HTTPSCertLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     names,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err = encodeKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), keyPEM, nil
}
