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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-seaweedfs/definition/components"
)

const (
	s3HTTPSSecretSuffix = "-s3-https"

	// seaweedfs-operator mounts tls.crt and tls.key and does not issue them.
	// A long lifetime avoids rotating a Secret the gateway re-reads on its
	// own schedule (default 5 hours).
	s3HTTPSCertLifetime = 10 * 365 * 24 * time.Hour
)

func s3HTTPSSecretName(instanceName string) string {
	return instanceName + s3HTTPSSecretSuffix
}

// ensureS3ServerTLS returns the Secret name to set on spec.s3.tlsSecret.
// An empty name leaves the gateway on HTTP. A user-supplied secretName is
// used as-is. Otherwise the provider creates Secret <instance>-s3-https
// once and keeps whatever certificate is already there.
func ensureS3ServerTLS(c *controller.Context, spec components.S3CustomSpec) (string, error) {
	if !s3HTTPSEnabled(spec) {
		return "", nil
	}
	if spec.HTTPS != nil && spec.HTTPS.SecretName != "" {
		return spec.HTTPS.SecretName, nil
	}

	name := s3HTTPSSecretName(c.Name())
	existing := &corev1.Secret{}
	err := c.Get(existing, name)
	switch {
	case err == nil:
		if len(existing.Data[corev1.TLSCertKey]) == 0 || len(existing.Data[corev1.TLSPrivateKeyKey]) == 0 {
			return "", fmt.Errorf("S3 TLS Secret %q is missing tls.crt or tls.key", name)
		}
		return name, nil
	case !controller.IsNotFound(err):
		return "", err
	}

	cert, key, err := generateS3ServerCert(c.Name(), c.Namespace())
	if err != nil {
		return "", fmt.Errorf("generating S3 TLS certificate: %w", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: c.ObjectMeta(name),
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       cert,
			corev1.TLSPrivateKeyKey: key,
		},
	}
	if err := controllerutil.SetControllerReference(c.Instance(), secret, c.Client().Scheme()); err != nil {
		return "", fmt.Errorf("setting owner on S3 TLS Secret %q: %w", name, err)
	}
	if err := c.Client().Create(c.Context(), secret); err != nil && !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("creating S3 TLS Secret %q: %w", name, err)
	}
	return name, nil
}

func generateS3ServerCert(instanceName, namespace string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}

	host := instanceName + "-s3"
	svc := host + "." + namespace + ".svc"
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: svc},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(s3HTTPSCertLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{
			host,
			host + "." + namespace,
			svc,
			svc + ".cluster.local",
		},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	return certPEM, keyPEM, nil
}
