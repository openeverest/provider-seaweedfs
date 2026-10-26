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
	"time"

	"github.com/AlekSi/pointer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func TestValidateStorageExpansion(t *testing.T) {
	class := pointer.ToString("fast")

	t.Run("same size does not consult the storage class", func(t *testing.T) {
		components := validComponents()
		volume := components[common.ComponentVolume]
		volume.Storage.StorageClass = class
		components[common.ComponentVolume] = volume
		sw := seaweedWithStorage("10Gi", "1Gi", class, nil)
		err := New().Validate(newValidateContext(t, "test-instance", components, sw))
		require.NoError(t, err)
	})

	t.Run("volume shrink", func(t *testing.T) {
		components := validComponents()
		volume := components[common.ComponentVolume]
		volume.Storage.Size = resource.MustParse("1Gi")
		components[common.ComponentVolume] = volume
		sw := seaweedWithStorage("10Gi", "1Gi", class, nil)
		ctx := newValidateContext(t, "test-instance", components, sw)
		err := New().Validate(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `storage.size cannot shrink from 10Gi to 1Gi`)
		assert.Equal(t, err.Error(), ctx.Instance().Status.Message)
	})

	t.Run("filer shrink", func(t *testing.T) {
		components := validComponents()
		filer := components[common.ComponentFiler]
		filer.Storage.Size = resource.MustParse("512Mi")
		components[common.ComponentFiler] = filer
		sw := seaweedWithStorage("10Gi", "1Gi", nil, nil)
		err := New().Validate(newValidateContext(t, "test-instance", components, sw))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"filer" component: storage.size cannot shrink`)
	})

	t.Run("master shrink", func(t *testing.T) {
		components := validComponents()
		master := components[common.ComponentMaster]
		master.Storage = &corev1alpha1.Storage{Size: resource.MustParse("1Gi")}
		components[common.ComponentMaster] = master
		sw := seaweedWithStorage("10Gi", "1Gi", nil, nil)
		sw.Spec.Master = &seaweedv1.MasterSpec{
			Persistence: &seaweedv1.PersistenceSpec{
				Enabled: true,
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
				},
			},
		}
		err := New().Validate(newValidateContext(t, "test-instance", components, sw))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"master" component: storage.size cannot shrink from 5Gi to 1Gi`)
	})

	t.Run("growth rejected when the class disallows expansion", func(t *testing.T) {
		components := sizedComponents("20Gi", "1Gi")
		sw := seaweedWithStorage("10Gi", "1Gi", class, nil)
		err := New().Validate(newValidateContext(t, "test-instance", components, sw, expandableClass("fast", false)))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `storage class "fast" does not allow volume expansion`)
	})

	t.Run("growth rejected when the named class is missing", func(t *testing.T) {
		components := sizedComponents("20Gi", "1Gi")
		sw := seaweedWithStorage("10Gi", "1Gi", class, nil)
		err := New().Validate(newValidateContext(t, "test-instance", components, sw))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `storage class "fast" does not exist`)
	})

	t.Run("growth allowed", func(t *testing.T) {
		components := sizedComponents("20Gi", "2Gi")
		filer := components[common.ComponentFiler]
		filer.Storage.StorageClass = pointer.ToString("slow")
		components[common.ComponentFiler] = filer
		sw := seaweedWithStorage("10Gi", "1Gi", class, pointer.ToString("slow"))
		ctx := newValidateContext(t, "test-instance", components, sw, expandableClass("fast", true), expandableClass("slow", true))
		require.NoError(t, New().Validate(ctx))
	})

	t.Run("growth uses the applied class when the instance omits it", func(t *testing.T) {
		components := sizedComponents("20Gi", "1Gi")
		volume := components[common.ComponentVolume]
		volume.Storage.StorageClass = nil
		components[common.ComponentVolume] = volume
		sw := seaweedWithStorage("10Gi", "1Gi", class, nil)
		ctx := newValidateContext(t, "test-instance", components, sw, expandableClass("fast", true))
		require.NoError(t, New().Validate(ctx))
	})

	t.Run("growth uses the newest default class", func(t *testing.T) {
		components := sizedComponents("20Gi", "1Gi")
		volume := components[common.ComponentVolume]
		volume.Storage.StorageClass = nil
		components[common.ComponentVolume] = volume
		sw := seaweedWithStorage("10Gi", "1Gi", nil, nil)
		older := defaultClass("old", false, time.Now().Add(-time.Hour))
		newer := defaultClass("new", true, time.Now())
		ctx := newValidateContext(t, "test-instance", components, sw, older, newer)
		require.NoError(t, New().Validate(ctx))
	})

	t.Run("growth rejected when no default class allows expansion", func(t *testing.T) {
		components := sizedComponents("20Gi", "1Gi")
		volume := components[common.ComponentVolume]
		volume.Storage.StorageClass = nil
		components[common.ComponentVolume] = volume
		sw := seaweedWithStorage("10Gi", "1Gi", nil, nil)
		err := New().Validate(newValidateContext(t, "test-instance", components, sw, defaultClass("local", false, time.Now())))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not allow volume expansion")
	})

	t.Run("growth rejected when there is no default class", func(t *testing.T) {
		components := sizedComponents("20Gi", "1Gi")
		volume := components[common.ComponentVolume]
		volume.Storage.StorageClass = nil
		components[common.ComponentVolume] = volume
		sw := seaweedWithStorage("10Gi", "1Gi", nil, nil)
		err := New().Validate(newValidateContext(t, "test-instance", components, sw, expandableClass("fast", true)))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no StorageClass allows volume expansion")
	})

	t.Run("growth rejected when the storage class changes", func(t *testing.T) {
		components := sizedComponents("20Gi", "1Gi")
		volume := components[common.ComponentVolume]
		volume.Storage.StorageClass = pointer.ToString("other")
		components[common.ComponentVolume] = volume
		// "other" allows expansion. The applied class is "fast" and is not consulted:
		// the operator refuses a class change even when the new class can grow.
		sw := seaweedWithStorage("10Gi", "1Gi", class, nil)
		err := New().Validate(newValidateContext(t, "test-instance", components, sw, expandableClass("other", true)))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"volume" component: storage.storageClass cannot change while storage.size grows`)
	})

	t.Run("master storage cannot be enabled after creation", func(t *testing.T) {
		components := validComponents()
		master := components[common.ComponentMaster]
		master.Storage = &corev1alpha1.Storage{Size: resource.MustParse("5Gi")}
		components[common.ComponentMaster] = master
		sw := seaweedWithStorage("10Gi", "1Gi", nil, nil)
		err := New().Validate(newValidateContext(t, "test-instance", components, sw))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"master" component: storage cannot be enabled after creation`)
	})
}

func TestStorageExpansionStatus(t *testing.T) {
	ready := readySeaweed()

	t.Run("no pvcs stays ready", func(t *testing.T) {
		status, err := New().Status(expansionStatusContext(t, ready))
		require.NoError(t, err)
		assert.Equal(t, corev1alpha1.InstancePhaseReady, status.Phase)
		assert.NotEmpty(t, status.ConnectionDetails.Host)
		assert.Equal(t, "AKTEST", status.ConnectionDetails.Username)
	})

	t.Run("resizing is updating", func(t *testing.T) {
		pvc := volumePVC("10Gi", "fast")
		pvc.Status.Conditions = []corev1.PersistentVolumeClaimCondition{{
			Type:    corev1.PersistentVolumeClaimResizing,
			Status:  corev1.ConditionTrue,
			Message: "resizing volume",
		}}
		status, err := New().Status(expansionStatusContext(t, ready, pvc))
		require.NoError(t, err)
		assert.Equal(t, corev1alpha1.InstancePhaseUpdating, status.Phase)
		assert.Contains(t, status.Message, "volume PVC mount0-test-instance-volume-0: Resizing: resizing volume")
		assert.NotEmpty(t, status.ConnectionDetails.Host)
	})

	t.Run("filesystem resize pending stays ready", func(t *testing.T) {
		pvc := filerPVC("1Gi", "fast")
		pvc.Status.Conditions = []corev1.PersistentVolumeClaimCondition{{
			Type:   corev1.PersistentVolumeClaimFileSystemResizePending,
			Status: corev1.ConditionTrue,
		}}
		status, err := New().Status(expansionStatusContext(t, ready, pvc))
		require.NoError(t, err)
		assert.Equal(t, corev1alpha1.InstancePhaseReady, status.Phase)
		assert.NotContains(t, status.Message, "FileSystemResizePending")
	})

	t.Run("request behind target waits", func(t *testing.T) {
		status, err := New().Status(expansionStatusContext(t, ready, volumePVC("1Gi", "fast"), expandableClass("fast", true)))
		require.NoError(t, err)
		assert.Equal(t, corev1alpha1.InstancePhaseUpdating, status.Phase)
		assert.Contains(t, status.Message, "waiting for volume PVC mount0-test-instance-volume-0 to expand from 1Gi to 10Gi")
	})

	t.Run("class that disallows expansion fails", func(t *testing.T) {
		status, err := New().Status(expansionStatusContext(t, ready, volumePVC("1Gi", "fast"), expandableClass("fast", false)))
		require.NoError(t, err)
		assert.Equal(t, corev1alpha1.InstancePhaseFailed, status.Phase)
		assert.Contains(t, status.Message, `storage class "fast" does not allow volume expansion`)
		assert.NotEmpty(t, status.ConnectionDetails.Host)
	})

	t.Run("both disks are reported", func(t *testing.T) {
		sw := readySeaweed()
		sw.Spec.VolumeServerDiskCount = pointer.ToInt32(2)
		status, err := New().Status(expansionStatusContext(t, sw,
			volumePVC("1Gi", "fast"),
			namedPVC("mount1-test-instance-volume-0", "1Gi", "fast"),
			expandableClass("fast", true),
		))
		require.NoError(t, err)
		assert.Equal(t, corev1alpha1.InstancePhaseUpdating, status.Phase)
		assert.Contains(t, status.Message, "mount0-test-instance-volume-0")
		assert.Contains(t, status.Message, "mount1-test-instance-volume-0")
	})

	t.Run("unrelated pvc is ignored", func(t *testing.T) {
		other := namedPVC("data-other-0", "1Gi", "fast")
		status, err := New().Status(expansionStatusContext(t, ready, other))
		require.NoError(t, err)
		assert.Equal(t, corev1alpha1.InstancePhaseReady, status.Phase)
	})
}

func TestPVCToInstance(t *testing.T) {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mount0-my-volume-volume-0",
			Namespace: "default",
			Labels: map[string]string{
				labelManagedBy: managedBySeaweedOperator,
				labelComponent: common.ComponentVolume,
				labelInstance:  "my-volume",
			},
		},
	}
	got := pvcToInstance(context.Background(), pvc)
	require.Len(t, got, 1)
	assert.Equal(t, "my-volume", got[0].Name)
	assert.Equal(t, "default", got[0].Namespace)

	t.Run("owner reference without selector labels is ignored", func(t *testing.T) {
		owned := pvc.DeepCopy()
		owned.Labels = nil
		owned.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
			Name:       "my-volume-volume",
			Controller: pointer.ToBool(true),
		}}
		assert.Empty(t, pvcToInstance(context.Background(), owned))
	})

	t.Run("other component", func(t *testing.T) {
		other := pvc.DeepCopy()
		other.Labels[labelComponent] = common.ComponentS3
		assert.Empty(t, pvcToInstance(context.Background(), other))
	})

	t.Run("missing instance", func(t *testing.T) {
		other := pvc.DeepCopy()
		delete(other.Labels, labelInstance)
		assert.Empty(t, pvcToInstance(context.Background(), other))
	})

	t.Run("unmanaged", func(t *testing.T) {
		other := pvc.DeepCopy()
		other.Labels[labelManagedBy] = "someone-else"
		assert.Empty(t, pvcToInstance(context.Background(), other))
	})
}

func sizedComponents(volume, filer string) map[string]corev1alpha1.ComponentSpec {
	components := validComponents()
	vol := components[common.ComponentVolume]
	vol.Storage.Size = resource.MustParse(volume)
	vol.Storage.StorageClass = pointer.ToString("fast")
	components[common.ComponentVolume] = vol
	f := components[common.ComponentFiler]
	f.Storage.Size = resource.MustParse(filer)
	components[common.ComponentFiler] = f
	return components
}

func seaweedWithStorage(volume, filer string, volumeClass, filerClass *string) *seaweedv1.Seaweed {
	sw := &seaweedv1.Seaweed{
		ObjectMeta: metav1.ObjectMeta{Name: "test-instance", Namespace: "default"},
		Spec: seaweedv1.SeaweedSpec{
			Volume: &seaweedv1.VolumeSpec{
				VolumeServerConfig: seaweedv1.VolumeServerConfig{
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(volume)},
					},
					StorageClassName: volumeClass,
				},
			},
			Filer: &seaweedv1.FilerSpec{
				Persistence: &seaweedv1.PersistenceSpec{
					Enabled: true,
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(filer)},
					},
					StorageClassName: filerClass,
				},
			},
		},
	}
	return sw
}

func readySeaweed() *seaweedv1.Seaweed {
	sw := seaweedWithStorage("10Gi", "1Gi", pointer.ToString("fast"), nil)
	sw.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready"}}
	return sw
}

func expansionStatusContext(t *testing.T, sw *seaweedv1.Seaweed, extras ...client.Object) *controller.Context {
	t.Helper()
	instance := &corev1alpha1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "test-instance", Namespace: "default"},
		Spec:       corev1alpha1.InstanceSpec{Components: validComponents()},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-instance-s3", Namespace: "default"},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "s3-http", Port: 8333}}},
	}
	scheme := runtime.NewScheme()
	require.NoError(t, corev1alpha1.AddToScheme(scheme))
	require.NoError(t, seaweedv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	s3Creds := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: s3ConfigSecretName(instance.Name), Namespace: "default"},
		Data: map[string][]byte{
			s3AccessKeyIDKey:     []byte("AKTEST"),
			s3SecretAccessKeyKey: []byte("secret-test"),
		},
	}
	objs := []client.Object{instance, sw, svc, s3Creds}
	objs = append(objs, extras...)
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return controller.NewContext(context.Background(), cl, instance, common.ProviderName)
}

func volumePVC(size, class string) *corev1.PersistentVolumeClaim {
	return namedPVC("mount0-test-instance-volume-0", size, class)
}

func filerPVC(size, class string) *corev1.PersistentVolumeClaim {
	return namedPVC("test-instance-filer-test-instance-filer-0", size, class)
}

func namedPVC(name, size, class string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: pointer.ToString(class),
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
			},
		},
	}
}

func expandableClass(name string, allow bool) *unstructured.Unstructured {
	sc := storageClass(name)
	if err := unstructured.SetNestedField(sc.Object, allow, "allowVolumeExpansion"); err != nil {
		panic(err)
	}
	return sc
}

func defaultClass(name string, allow bool, created time.Time) *unstructured.Unstructured {
	sc := expandableClass(name, allow)
	sc.SetAnnotations(map[string]string{"storageclass.kubernetes.io/is-default-class": "true"})
	sc.SetCreationTimestamp(metav1.NewTime(created))
	return sc
}
