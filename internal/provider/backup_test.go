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
	"fmt"
	"strings"
	"testing"

	"github.com/AlekSi/pointer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/internal/common"
)

// Fixed UID so unit tests get a stable UID-derived SeaweedBackup name.
const testBackupUID = types.UID("abcdef12-3456-7890-abcd-ef1234567890")

func testSeaweedBackupName(backupName string) string {
	name, err := deriveSeaweedBackupName(backupName, string(testBackupUID))
	if err != nil {
		panic(err)
	}
	return name
}

func TestBuildBackupSpecDisabled(t *testing.T) {
	ctx := backupSpecTestContext(t, &corev1alpha1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "sw", Namespace: "ns"},
		Spec: corev1alpha1.InstanceSpec{
			Backup: &corev1alpha1.InstanceBackupSpec{Enabled: false},
		},
	})
	got, err := buildBackupSpec(ctx)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestBuildBackupSpecMapsS3Storage(t *testing.T) {
	forcePath := false
	verifyTLS := true
	bs := &backupv1alpha1.BackupStorage{
		ObjectMeta: metav1.ObjectMeta{Name: "my-s3", Namespace: "ns"},
		Spec: backupv1alpha1.BackupStorageSpec{
			Type: backupv1alpha1.BackupStorageTypeS3,
			S3: &backupv1alpha1.BackupStorageS3Spec{
				Bucket:               "my-bucket",
				Region:               "us-west-2",
				EndpointURL:          "https://s3.example.com",
				VerifyTLS:            &verifyTLS,
				ForcePathStyle:       &forcePath,
				CredentialsSecretRef: commonv1alpha1.SecretRef{Name: "my-s3-creds"},
			},
		},
	}
	instance := &corev1alpha1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "sw", Namespace: "ns"},
		Spec: corev1alpha1.InstanceSpec{
			Backup: &corev1alpha1.InstanceBackupSpec{
				Enabled: true,
				Storages: []corev1alpha1.InstanceBackupStorage{{
					StorageRef: commonv1alpha1.ObjectRef{Name: "my-s3"},
				}},
			},
		},
	}
	ctx := backupSpecTestContext(t, instance, bs)

	got, err := buildBackupSpec(ctx)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Contains(t, got.Storages, "my-s3")
	st := got.Storages["my-s3"]
	assert.Equal(t, seaweedv1.BackupStorageS3, st.Type)
	require.NotNil(t, st.S3)
	assert.Equal(t, "my-bucket", st.S3.Bucket)
	assert.Equal(t, "us-west-2", st.S3.Region)
	assert.Equal(t, "https://s3.example.com", st.S3.Endpoint)
	assert.Equal(t, "/", st.S3.Directory)
	require.NotNil(t, st.S3.ForcePathStyle)
	assert.False(t, *st.S3.ForcePathStyle)
	require.NotNil(t, st.CredentialsSecret)
	assert.Equal(t, "my-s3-creds", *st.CredentialsSecret)
	assert.Empty(t, got.Schedule)
	assert.Empty(t, got.DataMirror)
}

func TestBuildBackupSpecForcePathStyleNilDefaultsFalse(t *testing.T) {
	bs := s3BackupStorage("my-s3", nil)
	instance := enabledBackupInstance("my-s3")
	ctx := backupSpecTestContext(t, instance, bs)

	got, err := buildBackupSpec(ctx)
	require.NoError(t, err)
	require.NotNil(t, got.Storages["my-s3"].S3.ForcePathStyle)
	assert.False(t, *got.Storages["my-s3"].S3.ForcePathStyle,
		"nil OpenEverest forcePathStyle must stay false, not the operator default true")
}

func TestBuildBackupSpecRejectsVerifyTLSFalse(t *testing.T) {
	bs := s3BackupStorage("my-s3", pointer.To(false))
	bs.Spec.S3.VerifyTLS = pointer.To(false)
	ctx := backupSpecTestContext(t, enabledBackupInstance("my-s3"), bs)

	_, err := buildBackupSpec(ctx)
	require.Error(t, err)
	bce := controller.AsBackupConfigError(err)
	require.NotNil(t, bce)
	assert.Equal(t, "VerifyTLSUnsupported", bce.Reason)
	assert.Contains(t, bce.Message, "verifyTLS false")
}

func TestBuildBackupSpecRejectsPITR(t *testing.T) {
	bs := s3BackupStorage("my-s3", pointer.To(true))
	instance := enabledBackupInstance("my-s3")
	instance.Spec.Backup.Storages[0].PITR = &corev1alpha1.InstanceBackupStoragePITR{Enabled: true}
	ctx := backupSpecTestContext(t, instance, bs)

	_, err := buildBackupSpec(ctx)
	require.Error(t, err)
	bce := controller.AsBackupConfigError(err)
	require.NotNil(t, bce)
	assert.Equal(t, controller.PITRConfigInvalidReason, bce.Reason)
}

func TestBuildBackupSpecRejectsInvalidStorageName(t *testing.T) {
	bs := s3BackupStorage("My_S3", pointer.To(true))
	instance := enabledBackupInstance("My_S3")
	ctx := backupSpecTestContext(t, instance, bs)

	_, err := buildBackupSpec(ctx)
	require.Error(t, err)
	bce := controller.AsBackupConfigError(err)
	require.NotNil(t, bce)
	assert.Equal(t, "InvalidStorageName", bce.Reason)
}

func TestBuildBackupSpecRejectsStorageNameTooLong(t *testing.T) {
	name := strings.Repeat("a", seaweedBackupStorageNameMax+1)
	bs := s3BackupStorage(name, pointer.To(true))
	ctx := backupSpecTestContext(t, enabledBackupInstance(name), bs)

	_, err := buildBackupSpec(ctx)
	require.Error(t, err)
	assert.NotNil(t, controller.AsBackupConfigError(err))
}

func TestBuildBackupSpecWaitsForMissingBackupStorage(t *testing.T) {
	ctx := backupSpecTestContext(t, enabledBackupInstance("missing-s3"))

	_, err := buildBackupSpec(ctx)
	require.Error(t, err)
	assert.True(t, controller.IsWaitError(err))
	assert.Contains(t, err.Error(), "missing-s3")
}

func TestValidateSeaweedBackupStorageName(t *testing.T) {
	require.NoError(t, validateSeaweedBackupStorageName("a"))
	require.NoError(t, validateSeaweedBackupStorageName("my-s3"))
	require.NoError(t, validateSeaweedBackupStorageName(strings.Repeat("a", seaweedBackupStorageNameMax)))

	require.Error(t, validateSeaweedBackupStorageName(""))
	require.Error(t, validateSeaweedBackupStorageName("My-S3"))
	require.Error(t, validateSeaweedBackupStorageName("-bad"))
	require.Error(t, validateSeaweedBackupStorageName(strings.Repeat("a", seaweedBackupStorageNameMax+1)))
}

func TestSyncBackupRejectsDeletionPolicyDelete(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	sw := seaweedClusterWithBackupStorage("sw", "my-s3")
	backup := onDemandBackup("adhoc-delete", "sw", "my-s3")
	backup.Spec.DeletionPolicy = backupv1alpha1.BackupDeletionPolicyDelete
	ctx := backupSpecTestContext(t, instance, sw, backup)

	exec, err := (&Provider{}).SyncBackup(ctx, backup)
	require.NoError(t, err)
	assert.Equal(t, backupv1alpha1.BackupStateFailed, exec.State)
	assert.Contains(t, exec.Message, "deletionPolicy Delete")

	exists, err := ctx.Exists(&seaweedv1.SeaweedBackup{}, testSeaweedBackupName("adhoc-delete"))
	require.NoError(t, err)
	assert.False(t, exists, "must not create SeaweedBackup when Delete is set")
}

func TestSyncBackupRejectsEmptyDeletionPolicy(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	sw := seaweedClusterWithBackupStorage("sw", "my-s3")
	backup := onDemandBackup("adhoc-default", "sw", "my-s3")
	backup.Spec.DeletionPolicy = ""
	ctx := backupSpecTestContext(t, instance, sw, backup)

	exec, err := (&Provider{}).SyncBackup(ctx, backup)
	require.NoError(t, err)
	assert.Equal(t, backupv1alpha1.BackupStateFailed, exec.State)
	assert.Contains(t, exec.Message, "deletionPolicy Delete")
}

func TestSyncBackupCreatesSeaweedBackup(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	sw := seaweedClusterWithBackupStorage("sw", "my-s3")
	backup := onDemandBackup("adhoc-1", "sw", "my-s3")
	wantName := testSeaweedBackupName("adhoc-1")
	ctx := backupSpecTestContext(t, instance, sw, backup)

	p := &Provider{}
	exec, err := p.SyncBackup(ctx, backup)
	require.NoError(t, err)
	assert.Equal(t, backupv1alpha1.BackupStatePending, exec.State)
	require.NotNil(t, exec.OperatorBackupRef)
	assert.Equal(t, "SeaweedBackup", exec.OperatorBackupRef.Kind)
	assert.Equal(t, wantName, exec.OperatorBackupRef.Name)
	assert.NotEqual(t, backup.Name, wantName, "SeaweedBackup name must be UID-derived")

	sb := &seaweedv1.SeaweedBackup{}
	require.NoError(t, ctx.Get(sb, wantName))
	assert.Equal(t, "sw", sb.Spec.ClusterName)
	assert.Equal(t, "my-s3", sb.Spec.StorageName)
	assert.Equal(t, "/", sb.Spec.FilerPath)

	exists, err := ctx.Exists(&seaweedv1.SeaweedBackup{}, "adhoc-1")
	require.NoError(t, err)
	assert.False(t, exists, "must not create SeaweedBackup under the Backup name")
}

func TestSyncBackupUsesFilerPathParameter(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	sw := seaweedClusterWithBackupStorage("sw", "my-s3")
	backup := onDemandBackup("adhoc-2", "sw", "my-s3")
	backup.Spec.Parameters = &runtime.RawExtension{Raw: []byte(`{"filerPath":"/buckets"}`)}
	ctx := backupSpecTestContext(t, instance, sw, backup)

	_, err := (&Provider{}).SyncBackup(ctx, backup)
	require.NoError(t, err)

	sb := &seaweedv1.SeaweedBackup{}
	require.NoError(t, ctx.Get(sb, testSeaweedBackupName("adhoc-2")))
	assert.Equal(t, "/buckets", sb.Spec.FilerPath)
}

func TestSyncBackupReusesOperatorBackupRefName(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	sw := seaweedClusterWithBackupStorage("sw", "my-s3")
	backup := onDemandBackup("adhoc-1", "sw", "my-s3")
	ref := seaweedBackupRef("already-bound-name")
	backup.Status.OperatorBackupRef = &ref
	sb := &seaweedv1.SeaweedBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "already-bound-name", Namespace: "ns"},
		Spec: seaweedv1.SeaweedBackupSpec{
			ClusterName: "sw",
			StorageName: "my-s3",
			FilerPath:   "/",
		},
	}
	ctx := backupSpecTestContext(t, instance, sw, backup, sb)

	exec, err := (&Provider{}).SyncBackup(ctx, backup)
	require.NoError(t, err)
	assert.Equal(t, "already-bound-name", exec.OperatorBackupRef.Name)

	exists, err := ctx.Exists(&seaweedv1.SeaweedBackup{}, testSeaweedBackupName("adhoc-1"))
	require.NoError(t, err)
	assert.False(t, exists, "must not create a second SeaweedBackup when ref is set")
}

func TestSyncBackupDoesNotRecreateSucceeded(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	sw := seaweedClusterWithBackupStorage("sw", "my-s3")
	backup := onDemandBackup("adhoc-done", "sw", "my-s3")
	backup.Status.State = backupv1alpha1.BackupStateSucceeded
	ref := seaweedBackupRef(testSeaweedBackupName("adhoc-done"))
	backup.Status.OperatorBackupRef = &ref
	ctx := backupSpecTestContext(t, instance, sw, backup)

	exec, err := (&Provider{}).SyncBackup(ctx, backup)
	require.NoError(t, err)
	assert.Equal(t, backupv1alpha1.BackupStateSucceeded, exec.State)

	exists, err := ctx.Exists(&seaweedv1.SeaweedBackup{}, testSeaweedBackupName("adhoc-done"))
	require.NoError(t, err)
	assert.False(t, exists, "must not recreate SeaweedBackup after Succeeded")
}

func TestDeriveSeaweedBackupName(t *testing.T) {
	got, err := deriveSeaweedBackupName("adhoc-1", "abcdef12-3456-7890")
	require.NoError(t, err)
	assert.Equal(t, "adhoc-1-abcdef12", got)

	long := strings.Repeat("a", seaweedBackupNamePrefixMax+10)
	got, err = deriveSeaweedBackupName(long, "1234567890abcdef")
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("%s-12345678", strings.Repeat("a", seaweedBackupNamePrefixMax)), got)
	assert.LessOrEqual(t, len(got), validation.DNS1123LabelMaxLength)

	_, err = deriveSeaweedBackupName("adhoc-1", "")
	require.Error(t, err)
}

func TestSyncBackupWaitsForStorageOnSeaweed(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	sw := &seaweedv1.Seaweed{
		ObjectMeta: metav1.ObjectMeta{Name: "sw", Namespace: "ns"},
		Spec:       seaweedv1.SeaweedSpec{},
	}
	backup := onDemandBackup("adhoc-1", "sw", "my-s3")
	ctx := backupSpecTestContext(t, instance, sw, backup)

	exec, err := (&Provider{}).SyncBackup(ctx, backup)
	require.NoError(t, err)
	assert.Equal(t, backupv1alpha1.BackupStatePending, exec.State)
	assert.Contains(t, exec.Message, "storages")
}

func TestMapSeaweedBackupStatus(t *testing.T) {
	cases := []struct {
		phase seaweedv1.BackupPhase
		want  backupv1alpha1.BackupState
	}{
		{seaweedv1.BackupPhasePending, backupv1alpha1.BackupStatePending},
		{seaweedv1.BackupPhaseRunning, backupv1alpha1.BackupStateRunning},
		{seaweedv1.BackupPhaseCompleted, backupv1alpha1.BackupStateSucceeded},
		{seaweedv1.BackupPhaseFailed, backupv1alpha1.BackupStateFailed},
		{"", backupv1alpha1.BackupStatePending},
	}
	for _, tc := range cases {
		exec := mapSeaweedBackupStatus(&seaweedv1.SeaweedBackup{
			ObjectMeta: metav1.ObjectMeta{Name: "b"},
			Status:     seaweedv1.SeaweedBackupStatus{Phase: tc.phase},
		})
		assert.Equal(t, tc.want, exec.State, "phase %q", tc.phase)
	}
}

func TestCleanupBackupDeletesSeaweedBackup(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	backup := onDemandBackup("adhoc-1", "sw", "my-s3")
	sbName := testSeaweedBackupName("adhoc-1")
	ref := seaweedBackupRef(sbName)
	backup.Status.OperatorBackupRef = &ref
	sb := &seaweedv1.SeaweedBackup{
		ObjectMeta: metav1.ObjectMeta{Name: sbName, Namespace: "ns"},
		Spec: seaweedv1.SeaweedBackupSpec{
			ClusterName: "sw",
			StorageName: "my-s3",
		},
	}
	ctx := backupSpecTestContext(t, instance, backup, sb)

	done, err := (&Provider{}).CleanupBackup(ctx, backup)
	require.NoError(t, err)
	assert.False(t, done)

	done, err = (&Provider{}).CleanupBackup(ctx, backup)
	require.NoError(t, err)
	assert.True(t, done)
}

func TestCleanupBackupAllowsDeletionPolicyDelete(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	backup := onDemandBackup("adhoc-1", "sw", "my-s3")
	backup.Spec.DeletionPolicy = backupv1alpha1.BackupDeletionPolicyDelete
	sbName := testSeaweedBackupName("adhoc-1")
	ref := seaweedBackupRef(sbName)
	backup.Status.OperatorBackupRef = &ref
	sb := &seaweedv1.SeaweedBackup{
		ObjectMeta: metav1.ObjectMeta{Name: sbName, Namespace: "ns"},
		Spec: seaweedv1.SeaweedBackupSpec{
			ClusterName: "sw",
			StorageName: "my-s3",
		},
	}
	ctx := backupSpecTestContext(t, instance, backup, sb)

	done, err := (&Provider{}).CleanupBackup(ctx, backup)
	require.NoError(t, err)
	assert.False(t, done)

	done, err = (&Provider{}).CleanupBackup(ctx, backup)
	require.NoError(t, err)
	assert.True(t, done)
}

func TestSyncBackupRejectsInvalidFilerPath(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	sw := seaweedClusterWithBackupStorage("sw", "my-s3")
	backup := onDemandBackup("adhoc-bad-path", "sw", "my-s3")
	backup.Spec.Parameters = &runtime.RawExtension{Raw: []byte(`{"filerPath":"/buckets'; rm -rf /"}`)}
	ctx := backupSpecTestContext(t, instance, sw, backup)

	exec, err := (&Provider{}).SyncBackup(ctx, backup)
	require.NoError(t, err)
	assert.Equal(t, backupv1alpha1.BackupStateFailed, exec.State)
	assert.Contains(t, exec.Message, "invalid filerPath")

	exists, err := ctx.Exists(&seaweedv1.SeaweedBackup{}, testSeaweedBackupName("adhoc-bad-path"))
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestSyncBackupFailsMalformedParameters(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	sw := seaweedClusterWithBackupStorage("sw", "my-s3")
	backup := onDemandBackup("adhoc-bad-params", "sw", "my-s3")
	backup.Spec.Parameters = &runtime.RawExtension{Raw: []byte(`{not-json`)}
	ctx := backupSpecTestContext(t, instance, sw, backup)

	exec, err := (&Provider{}).SyncBackup(ctx, backup)
	require.NoError(t, err)
	assert.Equal(t, backupv1alpha1.BackupStateFailed, exec.State)
	assert.Contains(t, exec.Message, "decode backup parameters")
}

func TestSyncBackupFailsWhenStorageNotOnInstance(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	sw := seaweedClusterWithBackupStorage("sw", "other-s3")
	backup := onDemandBackup("adhoc-unlisted", "sw", "other-s3")
	ctx := backupSpecTestContext(t, instance, sw, backup)

	exec, err := (&Provider{}).SyncBackup(ctx, backup)
	require.NoError(t, err)
	assert.Equal(t, backupv1alpha1.BackupStateFailed, exec.State)
	assert.Contains(t, exec.Message, "not registered in Instance.spec.backup.storages")
}

func TestSyncBackupFailsWhenBackupDisabled(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	instance.Spec.Backup.Enabled = false
	sw := seaweedClusterWithBackupStorage("sw", "my-s3")
	backup := onDemandBackup("adhoc-disabled", "sw", "my-s3")
	ctx := backupSpecTestContext(t, instance, sw, backup)

	exec, err := (&Provider{}).SyncBackup(ctx, backup)
	require.NoError(t, err)
	assert.Equal(t, backupv1alpha1.BackupStateFailed, exec.State)
	assert.Contains(t, exec.Message, "backup is not enabled")
}

func TestValidFilerPath(t *testing.T) {
	assert.True(t, validFilerPath("/"))
	assert.True(t, validFilerPath("/buckets"))
	assert.True(t, validFilerPath("/a/b_c.d-e"))
	assert.False(t, validFilerPath(""))
	assert.False(t, validFilerPath("buckets"))
	assert.False(t, validFilerPath("/buckets/"))
	assert.False(t, validFilerPath("/a/../b"))
	assert.False(t, validFilerPath("/buckets'; id"))
	assert.False(t, validFilerPath("/buckets\nrm"))
}

func TestSyncRestoreNotImplemented(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	ctx := backupSpecTestContext(t, instance)

	exec, err := (&Provider{}).SyncRestore(ctx, &backupv1alpha1.Restore{
		ObjectMeta: metav1.ObjectMeta{Name: "restore-1", Namespace: "ns"},
	})
	require.NoError(t, err)
	assert.Equal(t, backupv1alpha1.RestoreStateFailed, exec.State)
	assert.Contains(t, exec.Message, "not implemented")
}

func seaweedClusterWithBackupStorage(cluster, storageName string) *seaweedv1.Seaweed {
	return &seaweedv1.Seaweed{
		ObjectMeta: metav1.ObjectMeta{Name: cluster, Namespace: "ns"},
		Spec: seaweedv1.SeaweedSpec{
			Backup: &seaweedv1.BackupSpec{
				Storages: map[string]seaweedv1.BackupStorageSpec{
					storageName: {Type: seaweedv1.BackupStorageS3},
				},
			},
		},
	}
}

func onDemandBackup(name, instance, storage string) *backupv1alpha1.Backup {
	return &backupv1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "ns",
			UID:       testBackupUID,
		},
		Spec: backupv1alpha1.BackupSpec{
			Origin: backupv1alpha1.BackupOrigin{
				Type:        backupv1alpha1.BackupOriginTypeInstance,
				InstanceRef: &commonv1alpha1.ObjectRef{Name: instance},
			},
			ClassRef:       commonv1alpha1.ObjectRef{Name: "seaweedfs-filer-meta"},
			StorageRef:     commonv1alpha1.ObjectRef{Name: storage},
			DeletionPolicy: backupv1alpha1.BackupDeletionPolicyRetain,
		},
	}
}

func s3BackupStorage(name string, forcePath *bool) *backupv1alpha1.BackupStorage {
	return &backupv1alpha1.BackupStorage{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"},
		Spec: backupv1alpha1.BackupStorageSpec{
			Type: backupv1alpha1.BackupStorageTypeS3,
			S3: &backupv1alpha1.BackupStorageS3Spec{
				Bucket:               "b",
				Region:               "us-east-1",
				EndpointURL:          "https://s3.amazonaws.com",
				ForcePathStyle:       forcePath,
				CredentialsSecretRef: commonv1alpha1.SecretRef{Name: name + "-creds"},
			},
		},
	}
}

func enabledBackupInstance(storageName string) *corev1alpha1.Instance {
	return &corev1alpha1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "sw", Namespace: "ns"},
		Spec: corev1alpha1.InstanceSpec{
			Backup: &corev1alpha1.InstanceBackupSpec{
				Enabled: true,
				Storages: []corev1alpha1.InstanceBackupStorage{{
					StorageRef: commonv1alpha1.ObjectRef{Name: storageName},
				}},
			},
		},
	}
}

func backupSpecTestContext(t *testing.T, instance *corev1alpha1.Instance, objs ...client.Object) *controller.Context {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1alpha1.AddToScheme(scheme))
	require.NoError(t, backupv1alpha1.AddToScheme(scheme))
	require.NoError(t, seaweedv1.AddToScheme(scheme))

	builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance)
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	return controller.NewContext(context.Background(), builder.Build(), instance, common.ProviderName)
}
