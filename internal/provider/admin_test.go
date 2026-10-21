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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func TestBuildAdminSpec(t *testing.T) {
	assert.Nil(t, buildAdminSpec(""))

	spec := buildAdminSpec("test-instance-admin-credentials")
	require.NotNil(t, spec)
	require.NotNil(t, spec.CredentialsSecret)
	assert.Equal(t, "test-instance-admin-credentials", spec.CredentialsSecret.Name)
	assert.Nil(t, spec.Service)
	assert.Nil(t, spec.MetricsPort)
	assert.Nil(t, spec.Ingress)
}

func TestGenerateAdminPassword(t *testing.T) {
	password, err := generateAdminPassword()
	require.NoError(t, err)
	assert.Len(t, password, adminPasswordLength)
	for _, r := range password {
		assert.Contains(t, adminPasswordAlphabet, string(r))
	}
}

func TestSyncAdminCreatesCredentials(t *testing.T) {
	instance := newSyncInstance(validComponents())
	instance.Spec.Topology = &corev1alpha1.TopologySpec{
		Type:       "standalone",
		Parameters: &runtime.RawExtension{Raw: []byte(`{"admin":{"enabled":true}}`)},
	}
	cl := newSyncClient(t, instance)

	ctx, sw := syncSeaweed(t, cl, instance)
	secretName := adminCredentialsSecretName(instance.Name)
	require.NotNil(t, sw.Spec.Admin)
	require.NotNil(t, sw.Spec.Admin.CredentialsSecret)
	assert.Equal(t, secretName, sw.Spec.Admin.CredentialsSecret.Name)
	assert.Equal(t, podLabels(instance.Name, "admin"), sw.Spec.Admin.Labels)
	assert.Contains(t, ctx.LabelledComponents(), "admin")
	assert.Nil(t, sw.Spec.Worker)

	secret := &corev1.Secret{}
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: secretName}, secret))
	assert.Equal(t, adminUsername, string(secret.Data[adminUserKey]))
	password := string(secret.Data[adminPasswordKey])
	assert.Len(t, password, adminPasswordLength)

	_, sw = syncSeaweed(t, cl, instance)
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: secretName}, secret))
	assert.Equal(t, adminUsername, string(secret.Data[adminUserKey]))
	assert.Equal(t, password, string(secret.Data[adminPasswordKey]))
	assert.Equal(t, secretName, sw.Spec.Admin.CredentialsSecret.Name)

	instance.Spec.Topology = nil
	_, sw = syncSeaweed(t, cl, instance)
	assert.Nil(t, sw.Spec.Admin)
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: secretName}, secret))
	assert.Equal(t, password, string(secret.Data[adminPasswordKey]))
}

func TestSyncAdminKeepsExistingPasswordAndForcesUsername(t *testing.T) {
	instance := newSyncInstance(validComponents())
	instance.Spec.Topology = &corev1alpha1.TopologySpec{
		Type:       "standalone",
		Parameters: &runtime.RawExtension{Raw: []byte(`{"admin":{"enabled":true}}`)},
	}
	secretName := adminCredentialsSecretName(instance.Name)
	cl := newSyncClient(t, instance, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: instance.Namespace},
		Data: map[string][]byte{
			adminUserKey:     []byte("someone-else"),
			adminPasswordKey: []byte("keep-me"),
		},
	})

	_, sw := syncSeaweed(t, cl, instance)
	require.NotNil(t, sw.Spec.Admin)
	assert.Equal(t, secretName, sw.Spec.Admin.CredentialsSecret.Name)

	secret := &corev1.Secret{}
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: secretName}, secret))
	assert.Equal(t, adminUsername, string(secret.Data[adminUserKey]))
	assert.Equal(t, "keep-me", string(secret.Data[adminPasswordKey]))
}

func TestAddAdminConnectionDetails(t *testing.T) {
	instance := newSyncInstance(validComponents())
	secretName := adminCredentialsSecretName(instance.Name)
	sw := &seaweedv1.Seaweed{
		ObjectMeta: metav1.ObjectMeta{Name: instance.Name, Namespace: instance.Namespace},
		Spec: seaweedv1.SeaweedSpec{
			Admin: &seaweedv1.AdminSpec{
				CredentialsSecret: &corev1.LocalObjectReference{Name: secretName},
			},
		},
	}
	ctx := controllerContext(t, instance)
	props := map[string]string{}
	addAdminConnectionDetails(ctx, sw, props)
	assert.Equal(t, "http://test-instance-admin.default.svc:23646", props["adminURL"])
	assert.Equal(t, adminUsername, props["adminUser"])
	assert.Equal(t, secretName, props["adminCredentialsSecret"])
	assert.NotContains(t, props, "adminPassword")

	empty := map[string]string{}
	addAdminConnectionDetails(ctx, &seaweedv1.Seaweed{}, empty)
	assert.Empty(t, empty)
}

func TestSyncWithoutAdminLeavesItUnset(t *testing.T) {
	instance := newSyncInstance(validComponents())
	_, sw := syncSeaweed(t, newSyncClient(t, instance), instance)
	assert.Nil(t, sw.Spec.Admin)
}

func TestAdminNotReadyMessage(t *testing.T) {
	instance := newSyncInstance(validComponents())
	sw := &seaweedv1.Seaweed{
		ObjectMeta: metav1.ObjectMeta{Name: instance.Name, Namespace: instance.Namespace},
		Spec: seaweedv1.SeaweedSpec{
			Admin: &seaweedv1.AdminSpec{
				CredentialsSecret: &corev1.LocalObjectReference{Name: "seaweed-admin"},
			},
		},
	}

	t.Run("missing secret", func(t *testing.T) {
		ctx := controllerContext(t, instance)
		msg, err := adminNotReadyMessage(ctx, sw)
		require.NoError(t, err)
		assert.Contains(t, msg, "seaweed-admin")
		assert.Contains(t, msg, adminPasswordKey)
	})

	t.Run("secret without password", func(t *testing.T) {
		ctx := controllerContext(t, instance, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "seaweed-admin", Namespace: instance.Namespace},
			Data:       map[string][]byte{adminUserKey: []byte("admin")},
		})
		msg, err := adminNotReadyMessage(ctx, sw)
		require.NoError(t, err)
		assert.Contains(t, msg, adminPasswordKey)
	})

	t.Run("secret with password", func(t *testing.T) {
		ctx := controllerContext(t, instance, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "seaweed-admin", Namespace: instance.Namespace},
			Data:       map[string][]byte{adminPasswordKey: []byte("s3cret")},
		})
		msg, err := adminNotReadyMessage(ctx, sw)
		require.NoError(t, err)
		assert.Empty(t, msg)
	})
}

func controllerContext(t *testing.T, instance *corev1alpha1.Instance, objs ...client.Object) *controller.Context {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1alpha1.AddToScheme(scheme))
	require.NoError(t, seaweedv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance)
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	return controller.NewContext(context.Background(), builder.Build(), instance, common.ProviderName)
}

// staleSecretCache simulates an informer cache that has not yet observed the
// admin credentials Secret: Get reports NotFound while writes still hit the
// API server (the fake store).
type staleSecretCache struct {
	client.Client
	name string
}

func (c staleSecretCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*corev1.Secret); ok && key.Name == c.name {
		return apierrors.NewNotFound(corev1.Resource("secrets"), key.Name)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func TestSyncAdminKeepsPasswordWhenCacheIsStale(t *testing.T) {
	instance := newSyncInstance(validComponents())
	instance.Spec.Topology = &corev1alpha1.TopologySpec{
		Type:       "standalone",
		Parameters: &runtime.RawExtension{Raw: []byte(`{"admin":{"enabled":true}}`)},
	}
	cl := newSyncClient(t, instance)
	secretName := adminCredentialsSecretName(instance.Name)
	key := types.NamespacedName{Namespace: instance.Namespace, Name: secretName}

	syncSeaweed(t, cl, instance)
	secret := &corev1.Secret{}
	require.NoError(t, cl.Get(context.Background(), key, secret))
	password := string(secret.Data[adminPasswordKey])
	require.NotEmpty(t, password)

	syncSeaweed(t, staleSecretCache{Client: cl, name: secretName}, instance)
	require.NoError(t, cl.Get(context.Background(), key, secret))
	assert.Equal(t, password, string(secret.Data[adminPasswordKey]))
}
