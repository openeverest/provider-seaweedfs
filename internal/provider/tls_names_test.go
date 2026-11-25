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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition"
)

func TestAdditionalNames(t *testing.T) {
	names := []string{"b.example.com", "a.example.com", "b.example.com"}
	assert.Equal(t, []string{"a.example.com", "b.example.com"},
		additionalNames(definition.TLSParameters{AdditionalNames: names}))
	assert.Nil(t, additionalNames(definition.TLSParameters{Mode: definition.TLSModeDisabled, AdditionalNames: names}))
	assert.Nil(t, additionalNames(definition.TLSParameters{CertificateSecretName: "mine", AdditionalNames: names}))
}

func TestAppliedAdditionalNames(t *testing.T) {
	params := definition.TLSParameters{AdditionalNames: []string{"s3.example.com"}}
	seaweed := func(recorded string) *seaweedv1.Seaweed {
		sw := &seaweedv1.Seaweed{ObjectMeta: metav1.ObjectMeta{Name: "sw", Namespace: "ns"}}
		if recorded != "" {
			sw.Annotations = map[string]string{tlsNamesAnnotation: recorded}
		}
		return sw
	}

	t.Run("a new instance takes the names", func(t *testing.T) {
		ctx := controllerContext(t, instanceWithParams(""))
		names, err := appliedAdditionalNames(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, []string{"s3.example.com"}, names)
		assert.Empty(t, ctx.GetPendingMaintenance())
	})

	t.Run("recorded names need nothing", func(t *testing.T) {
		ctx := controllerContext(t, instanceWithParams(""), seaweed("s3.example.com"))
		names, err := appliedAdditionalNames(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, []string{"s3.example.com"}, names)
		assert.Empty(t, ctx.GetPendingMaintenance())
	})

	t.Run("a change waits for approval", func(t *testing.T) {
		ctx := controllerContext(t, instanceWithParams(""), seaweed("old.example.com"))
		names, err := appliedAdditionalNames(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, []string{"old.example.com"}, names)
		pending := ctx.GetPendingMaintenance()
		require.Len(t, pending, 1)
		assert.Equal(t, "tls-names-"+namesHash([]string{"s3.example.com"}), pending[0].ApprovalToken)
		assert.Equal(t, controller.MaintenanceRollingRestart, pending[0].Severity)
		assert.Contains(t, pending[0].Description, "s3.example.com")
	})

	t.Run("an approved change applies", func(t *testing.T) {
		inst := instanceWithParams("")
		inst.Spec.Maintenance = &corev1alpha1.MaintenanceSpec{Approved: "tls-names-" + namesHash([]string{"s3.example.com"})}
		ctx := controllerContext(t, inst, seaweed("old.example.com"))
		names, err := appliedAdditionalNames(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, []string{"s3.example.com"}, names)
	})

	t.Run("removing the names waits too", func(t *testing.T) {
		ctx := controllerContext(t, instanceWithParams(""), seaweed("old.example.com"))
		names, err := appliedAdditionalNames(ctx, definition.TLSParameters{})
		require.NoError(t, err)
		assert.Equal(t, []string{"old.example.com"}, names)
		require.Len(t, ctx.GetPendingMaintenance(), 1)
	})

	t.Run("an instance without names upgrades without maintenance", func(t *testing.T) {
		ctx := controllerContext(t, instanceWithParams(""), seaweed(""))
		names, err := appliedAdditionalNames(ctx, definition.TLSParameters{})
		require.NoError(t, err)
		assert.Empty(t, names)
		assert.Empty(t, ctx.GetPendingMaintenance())
	})
}

func TestRecordTLSNames(t *testing.T) {
	sw := &seaweedv1.Seaweed{}
	recordTLSNames(sw, nil)
	assert.Nil(t, sw.Annotations)
	recordTLSNames(sw, []string{"a.example.com", "b.example.com"})
	assert.Equal(t, "a.example.com,b.example.com", sw.Annotations[tlsNamesAnnotation])
}

func TestAddS3TLSNamesRestart(t *testing.T) {
	plain, plainKey := selfSigned(t, s3ServerDNSNames("sw", "ns", nil))
	extra, extraKey := selfSigned(t, s3ServerDNSNames("sw", "ns", []string{"s3.example.com"}))
	ctx := controllerContext(t, instanceWithParams(""),
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "plain", Namespace: "ns"},
			Data:       map[string][]byte{corev1.TLSCertKey: plain, corev1.TLSPrivateKeyKey: plainKey},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "extra", Namespace: "ns"},
			Data:       map[string][]byte{corev1.TLSCertKey: extra, corev1.TLSPrivateKeyKey: extraKey},
		})
	spec := func(secret string) *seaweedv1.S3GatewaySpec {
		s := &seaweedv1.S3GatewaySpec{TLSSecret: &corev1.LocalObjectReference{Name: secret}}
		s.Annotations = map[string]string{s3ConfigChecksumAnnotation: "sum"}
		return s
	}

	s := spec("plain")
	require.NoError(t, addS3TLSNamesRestart(ctx, s, definition.TLSParameters{}))
	assert.Equal(t, map[string]string{s3ConfigChecksumAnnotation: "sum"}, s.Annotations,
		"no additional names: no annotation, so an upgrade restarts nothing")

	s = spec("extra")
	require.NoError(t, addS3TLSNamesRestart(ctx, s, definition.TLSParameters{}))
	assert.Equal(t, "sum", s.Annotations[s3ConfigChecksumAnnotation])
	assert.Equal(t, namesHash([]string{"s3.example.com"}), s.Annotations[s3TLSNamesAnnotation])

	s = spec("extra")
	require.NoError(t, addS3TLSNamesRestart(ctx, s, definition.TLSParameters{CertificateSecretName: "extra"}))
	assert.NotContains(t, s.Annotations, s3TLSNamesAnnotation, "the user's certificate is theirs to roll")

	s = spec("missing")
	require.NoError(t, addS3TLSNamesRestart(ctx, s, definition.TLSParameters{}))
	assert.NotContains(t, s.Annotations, s3TLSNamesAnnotation)
}
