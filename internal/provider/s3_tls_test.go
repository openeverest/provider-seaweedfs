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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-seaweedfs/definition/components"
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

func TestWaitingForS3HTTPSSecret(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, corev1alpha1.AddToScheme(scheme))

	inst := &corev1alpha1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "sw", Namespace: "ns"},
	}
	newCtx := func(objs ...runtime.Object) *controller.Context {
		cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(append(objs, inst)...).Build()
		return controller.NewContext(t.Context(), cl, inst, "test")
	}

	t.Run("provider-managed cert does not wait", func(t *testing.T) {
		waiting, err := waitingForS3HTTPSSecret(newCtx(), components.S3CustomSpec{})
		require.NoError(t, err)
		assert.False(t, waiting)
	})

	t.Run("missing user secret waits", func(t *testing.T) {
		waiting, err := waitingForS3HTTPSSecret(newCtx(), components.S3CustomSpec{
			HTTPS: &components.S3HTTPSSpec{SecretName: "my-s3-tls"},
		})
		require.NoError(t, err)
		assert.True(t, waiting)
	})

	t.Run("secret missing keys waits", func(t *testing.T) {
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "my-s3-tls", Namespace: "ns"},
			Type:       corev1.SecretTypeTLS,
			Data:       map[string][]byte{corev1.TLSCertKey: []byte("cert")},
		}
		waiting, err := waitingForS3HTTPSSecret(newCtx(sec), components.S3CustomSpec{
			HTTPS: &components.S3HTTPSSpec{SecretName: "my-s3-tls"},
		})
		require.NoError(t, err)
		assert.True(t, waiting)
	})

	t.Run("complete user secret is ready", func(t *testing.T) {
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "my-s3-tls", Namespace: "ns"},
			Type:       corev1.SecretTypeTLS,
			Data: map[string][]byte{
				corev1.TLSCertKey:       []byte("cert"),
				corev1.TLSPrivateKeyKey: []byte("key"),
			},
		}
		waiting, err := waitingForS3HTTPSSecret(newCtx(sec), components.S3CustomSpec{
			HTTPS: &components.S3HTTPSSpec{SecretName: "my-s3-tls"},
		})
		require.NoError(t, err)
		assert.False(t, waiting)
	})
}
