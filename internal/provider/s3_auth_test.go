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
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"
)

func getS3ConfigSecret(t *testing.T, c *controller.Context) *corev1.Secret {
	t.Helper()
	secret := &corev1.Secret{}
	require.NoError(t, c.Get(secret, s3ConfigSecretName(c.Name())))
	return secret
}

func decodeS3IdentitiesConfig(t *testing.T, secret *corev1.Secret) s3IdentitiesConfig {
	t.Helper()
	var cfg s3IdentitiesConfig
	require.NoError(t, json.Unmarshal(secret.Data[s3ConfigKey], &cfg))
	return cfg
}

func TestSyncS3Auth(t *testing.T) {
	t.Run("generates admin credentials and points the gateway at them", func(t *testing.T) {
		instance := newSyncInstance(validComponents())
		ctx, sw := syncSeaweed(t, newSyncClient(t, instance), instance)

		secret := getS3ConfigSecret(t, ctx)
		require.Len(t, secret.OwnerReferences, 1)
		assert.Equal(t, "Instance", secret.OwnerReferences[0].Kind)
		assert.Equal(t, instance.Name, secret.OwnerReferences[0].Name)

		accessKey := string(secret.Data[s3AccessKeyIDKey])
		secretKey := string(secret.Data[s3SecretAccessKeyKey])
		assert.Regexp(t, regexp.MustCompile(`^[A-Z0-9]{20}$`), accessKey)
		assert.Regexp(t, regexp.MustCompile(`^[A-Za-z0-9]{40}$`), secretKey)

		cfg := decodeS3IdentitiesConfig(t, secret)
		require.Len(t, cfg.Identities, 1)
		assert.Equal(t, s3AdminIdentityName, cfg.Identities[0].Name)
		assert.Equal(t, []s3Credential{{AccessKey: accessKey, SecretKey: secretKey}}, cfg.Identities[0].Credentials)
		assert.Contains(t, cfg.Identities[0].Actions, "Admin")

		require.NotNil(t, sw.Spec.S3.ConfigSecret)
		assert.Equal(t, "test-instance-s3-config", sw.Spec.S3.ConfigSecret.Name)
		assert.Equal(t, s3ConfigKey, sw.Spec.S3.ConfigSecret.Key)
		assert.Len(t, sw.Spec.S3.Annotations[s3ConfigChecksumAnnotation], 64)
	})

	t.Run("keeps credentials stable across syncs", func(t *testing.T) {
		instance := newSyncInstance(validComponents())
		cl := newSyncClient(t, instance)
		ctx, first := syncSeaweed(t, cl, instance)
		firstSecret := getS3ConfigSecret(t, ctx)

		ctx, second := syncSeaweed(t, cl, instance)
		secondSecret := getS3ConfigSecret(t, ctx)

		assert.Equal(t, firstSecret.Data, secondSecret.Data)
		assert.Equal(t,
			first.Spec.S3.Annotations[s3ConfigChecksumAnnotation],
			second.Spec.S3.Annotations[s3ConfigChecksumAnnotation])
	})

	t.Run("uses credentials from a pre-created Secret", func(t *testing.T) {
		instance := newSyncInstance(validComponents())
		cl := newSyncClient(t, instance)
		require.NoError(t, cl.Create(t.Context(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "test-instance-s3-config", Namespace: "default"},
			Data: map[string][]byte{
				s3AccessKeyIDKey:     []byte("MYACCESSKEY"),
				s3SecretAccessKeyKey: []byte("my-secret-key"),
			},
		}))

		ctx, _ := syncSeaweed(t, cl, instance)
		secret := getS3ConfigSecret(t, ctx)
		assert.Equal(t, "MYACCESSKEY", string(secret.Data[s3AccessKeyIDKey]))
		assert.Equal(t, "my-secret-key", string(secret.Data[s3SecretAccessKeyKey]))
		assert.Equal(t,
			[]s3Credential{{AccessKey: "MYACCESSKEY", SecretKey: "my-secret-key"}},
			decodeS3IdentitiesConfig(t, secret).Identities[0].Credentials)
	})

	t.Run("fills in a missing secret key", func(t *testing.T) {
		instance := newSyncInstance(validComponents())
		cl := newSyncClient(t, instance)
		require.NoError(t, cl.Create(t.Context(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "test-instance-s3-config", Namespace: "default"},
			Data:       map[string][]byte{s3AccessKeyIDKey: []byte("MYACCESSKEY")},
		}))

		ctx, _ := syncSeaweed(t, cl, instance)
		secret := getS3ConfigSecret(t, ctx)
		assert.Equal(t, "MYACCESSKEY", string(secret.Data[s3AccessKeyIDKey]))
		assert.Len(t, secret.Data[s3SecretAccessKeyKey], s3SecretAccessKeyLength)
	})

	t.Run("changed credentials change the pod checksum", func(t *testing.T) {
		instance := newSyncInstance(validComponents())
		cl := newSyncClient(t, instance)
		ctx, before := syncSeaweed(t, cl, instance)

		secret := getS3ConfigSecret(t, ctx)
		secret.Data[s3SecretAccessKeyKey] = []byte("rotated-secret-key")
		require.NoError(t, cl.Update(t.Context(), secret))

		ctx, after := syncSeaweed(t, cl, instance)
		assert.NotEqual(t,
			before.Spec.S3.Annotations[s3ConfigChecksumAnnotation],
			after.Spec.S3.Annotations[s3ConfigChecksumAnnotation])
		assert.Equal(t, "rotated-secret-key",
			decodeS3IdentitiesConfig(t, getS3ConfigSecret(t, ctx)).Identities[0].Credentials[0].SecretKey)
	})
}

func TestApplyS3Auth(t *testing.T) {
	spec := &seaweedv1.S3GatewaySpec{}
	spec.Annotations = map[string]string{"keep": "me"}

	applyS3Auth(spec, "sw", "abc")

	require.NotNil(t, spec.ConfigSecret)
	assert.Equal(t, "sw-s3-config", spec.ConfigSecret.Name)
	assert.Equal(t, "s3.json", spec.ConfigSecret.Key)
	assert.Equal(t, map[string]string{"keep": "me", s3ConfigChecksumAnnotation: "abc"}, spec.Annotations)
}

func TestWithS3Credentials(t *testing.T) {
	details := withS3Credentials(controller.ConnectionDetails{Type: "s3"}, s3Credentials{
		AccessKeyID: "AK", SecretAccessKey: "SK",
	})

	assert.Equal(t, "s3", details.Type)
	assert.Equal(t, "AK", details.Username)
	assert.Equal(t, "SK", details.Password)
	assert.Equal(t, map[string]string{"AWS_ACCESS_KEY_ID": "AK", "AWS_SECRET_ACCESS_KEY": "SK"}, details.AdditionalProperties)
}

func TestRandomString(t *testing.T) {
	a, err := randomString(s3SecretAccessKeyLength, s3SecretAccessKeyAlphabet)
	require.NoError(t, err)
	b, err := randomString(s3SecretAccessKeyLength, s3SecretAccessKeyAlphabet)
	require.NoError(t, err)
	assert.Len(t, a, s3SecretAccessKeyLength)
	assert.NotEqual(t, a, b)
}
