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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/topologies/standalone"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func TestBuildAdminSpec(t *testing.T) {
	assert.Nil(t, buildAdminSpec(standalone.StandaloneTopologyConfig{}))
	assert.Nil(t, buildAdminSpec(standalone.StandaloneTopologyConfig{Admin: &standalone.AdminConfig{}}))

	spec := buildAdminSpec(standalone.StandaloneTopologyConfig{
		Admin: &standalone.AdminConfig{CredentialsSecret: "seaweed-admin"},
	})
	require.NotNil(t, spec)
	require.NotNil(t, spec.CredentialsSecret)
	assert.Equal(t, "seaweed-admin", spec.CredentialsSecret.Name)
	assert.Nil(t, spec.Service)
	assert.Nil(t, spec.MetricsPort)
	assert.Nil(t, spec.Ingress)
}

func TestValidateAdminParameters(t *testing.T) {
	require.NoError(t, validateAdminParameters(standalone.StandaloneTopologyConfig{}))
	require.NoError(t, validateAdminParameters(standalone.StandaloneTopologyConfig{
		Admin: &standalone.AdminConfig{CredentialsSecret: "seaweed-admin"},
	}))

	err := validateAdminParameters(standalone.StandaloneTopologyConfig{
		Admin: &standalone.AdminConfig{CredentialsSecret: "Not a secret"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin.credentialsSecret")
}

func TestSyncAdminReferencesUserSecret(t *testing.T) {
	instance := newSyncInstance(validComponents())
	instance.Spec.Topology = &corev1alpha1.TopologySpec{
		Type:       "standalone",
		Parameters: &runtime.RawExtension{Raw: []byte(`{"admin":{"credentialsSecret":"seaweed-admin"}}`)},
	}
	cl := newSyncClient(t, instance)

	ctx, sw := syncSeaweed(t, cl, instance)
	require.NotNil(t, sw.Spec.Admin)
	require.NotNil(t, sw.Spec.Admin.CredentialsSecret)
	assert.Equal(t, "seaweed-admin", sw.Spec.Admin.CredentialsSecret.Name)
	assert.Equal(t, podLabels(instance.Name, "admin"), sw.Spec.Admin.Labels)
	assert.Contains(t, ctx.LabelledComponents(), "admin")
	assert.Nil(t, sw.Spec.Worker)

	secret := &corev1.Secret{}
	err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: "seaweed-admin"}, secret)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))

	instance.Spec.Topology = nil
	_, sw = syncSeaweed(t, cl, instance)
	assert.Nil(t, sw.Spec.Admin)
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
