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
	"strings"
	"testing"

	"github.com/AlekSi/pointer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller/validationtest"

	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/internal/common"
)

const providerSpecPath = "../../charts/provider-seaweedfs/generated/provider-spec.yaml"

// TestValidateFixtures runs the checks the runtime runs before every Sync and
// at admission: the schemas, supportedFields and catalog this provider
// publishes, then Validate.
func TestValidateFixtures(t *testing.T) {
	t.Parallel()
	v := validationtest.New(t, New(), providerSpecPath)

	tests := []struct {
		fixture string
		// old is the stored Instance of an update at admission.
		old string
		// want lists the field paths of the expected errors.
		want []string
	}{
		{fixture: "testdata/validate/minimal.yaml"},
		{fixture: "testdata/validate/minimal.yaml", old: "testdata/validate/minimal.yaml"},
		{
			fixture: "testdata/validate/even-masters.yaml",
			want:    []string{"spec.components[master]"},
		},
		{
			fixture: "testdata/validate/s3-port-too-high.yaml",
			want:    []string{"spec.components[s3].parameters.port"},
		},
		{
			fixture: "testdata/validate/replication-needs-servers.yaml",
			want:    []string{"spec.topology.parameters.replication"},
		},
		{
			// The stored size may never have been applied, as after a rejected
			// expansion: shrinks are checked against the Seaweed cluster.
			fixture: "testdata/validate/volume-shrink.yaml",
			old:     "testdata/validate/minimal.yaml",
		},
		{
			fixture: "testdata/validate/tls-issuer-empty-name.yaml",
			want:    []string{"spec.parameters.tls.issuer.name"},
		},
		{
			fixture: "testdata/validate/tls-legacy-topology.yaml",
			want:    []string{"spec.topology.parameters.tls"},
		},
		{fixture: "testdata/validate/tls-disabled.yaml"},
		{
			fixture: "testdata/validate/tls-disabled.yaml",
			old:     "testdata/validate/minimal.yaml",
			want:    []string{"spec.parameters.tls.mode"},
		},
		{
			fixture: "testdata/validate/master-storage-added.yaml",
			old:     "testdata/validate/minimal.yaml",
			want:    []string{"spec.components[master].storage"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.fixture+" from "+tt.old, func(t *testing.T) {
			t.Parallel()
			var old *corev1alpha1.Instance
			if tt.old != "" {
				old = validationtest.Load(t, tt.old)
			}
			res := v.Run(validationtest.Load(t, tt.fixture), old)

			var got []string
			for _, err := range res.Errors {
				got = append(got, err.Field)
			}
			assert.Equal(t, tt.want, got, "errors: %v", res.Errors)
		})
	}
}

func TestValidateInstanceName(t *testing.T) {
	require.Equal(t, 45, maxInstanceNameLength)
	require.LessOrEqual(t, maxInstanceNameLength+len(statefulSetSuffix), statefulSetNameMaxLength)

	require.NoError(t, validateInstanceName("a"))
	require.NoError(t, validateInstanceName("seaweedfs-standalone"))
	require.NoError(t, validateInstanceName(strings.Repeat("a", maxInstanceNameLength)))

	err := validateInstanceName(strings.Repeat("a", maxInstanceNameLength+1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds max length of 45")

	err = validateInstanceName("1invalid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RFC-1035")

	err = validateInstanceName("Invalid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RFC-1035")

	err = validateInstanceName("has.dot")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RFC-1035")
}

func TestValidateStorageClasses(t *testing.T) {
	withClass := func(class string) map[string]corev1alpha1.ComponentSpec {
		c := validComponents()
		volume := c[common.ComponentVolume]
		volume.Storage.StorageClass = pointer.ToString(class)
		c[common.ComponentVolume] = volume
		return c
	}

	t.Run("unset class is not looked up", func(t *testing.T) {
		err := validateAll(newValidateContext(t, "test-instance", validComponents()))
		require.NoError(t, err)
	})

	t.Run("empty class is not looked up", func(t *testing.T) {
		err := validateAll(newValidateContext(t, "test-instance", withClass("")))
		require.NoError(t, err)
	})

	t.Run("missing class", func(t *testing.T) {
		err := validateAll(newValidateContext(t, "test-instance", withClass("missing")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `storage class "missing" does not exist`)
	})

	t.Run("existing class", func(t *testing.T) {
		ctx := newValidateContext(t, "test-instance", withClass("fast"), storageClass("fast"))
		require.NoError(t, validateAll(ctx))
	})

	t.Run("same class on two components is accepted once", func(t *testing.T) {
		components := withClass("fast")
		filer := components[common.ComponentFiler]
		filer.Storage.StorageClass = pointer.ToString("fast")
		components[common.ComponentFiler] = filer
		ctx := newValidateContext(t, "test-instance", components, storageClass("fast"))
		require.NoError(t, validateAll(ctx))
	})

	t.Run("name is rejected before the storage class lookup", func(t *testing.T) {
		err := validateAll(newValidateContext(t, "1invalid", withClass("missing")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "RFC-1035")
	})

	t.Run("missing class is ignored once the cluster exists", func(t *testing.T) {
		sw := &seaweedv1.Seaweed{ObjectMeta: metav1.ObjectMeta{Name: "test-instance", Namespace: "default"}}
		err := validateAll(newValidateContext(t, "test-instance", withClass("missing"), sw))
		require.NoError(t, err)
	})
}

// validateAll runs Validate on the Instance of c, then ValidateReferences, as
// the runtime does before Sync.
func validateAll(c *controller.Context) error {
	p := New()
	if errs := p.Validate(controller.ValidationInput{Instance: c.Instance()}).Errors; len(errs) > 0 {
		return errs.ToAggregate()
	}
	errs, err := p.ValidateReferences(c)
	if err != nil {
		return err
	}
	return errs.ToAggregate()
}

func newValidateContext(t *testing.T, name string, components map[string]corev1alpha1.ComponentSpec, extras ...client.Object) *controller.Context {
	t.Helper()
	instance := &corev1alpha1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       corev1alpha1.InstanceSpec{Components: components},
	}
	scheme := runtime.NewScheme()
	require.NoError(t, corev1alpha1.AddToScheme(scheme))
	require.NoError(t, seaweedv1.AddToScheme(scheme))
	objs := append([]client.Object{instance}, extras...)
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return controller.NewContext(context.Background(), cl, instance, common.ProviderName)
}

func storageClass(name string) *unstructured.Unstructured {
	sc := &unstructured.Unstructured{}
	sc.SetGroupVersionKind(storageClassGVK)
	sc.SetName(name)
	return sc
}
