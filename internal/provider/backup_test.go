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
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/internal/common"
)

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

func TestBuildBackupSpecMapsSchedules(t *testing.T) {
	bs := s3BackupStorage("my-s3", pointer.To(false))
	instance := enabledBackupInstance("my-s3")
	instance.Spec.Backup.Storages[0].Schedules = []corev1alpha1.InstanceBackupSchedule{
		{
			Name:    "nightly",
			Enabled: true,
			Cron:    "0 2 * * *",
			Retention: &corev1alpha1.BackupScheduleRetention{
				Type:  corev1alpha1.BackupScheduleRetentionTypeCount,
				Count: pointer.To(int32(7)),
			},
		},
		{
			Name:    "paused",
			Enabled: false,
			Cron:    "0 4 * * 0",
		},
	}
	ctx := backupSpecTestContext(t, instance, bs)

	got, err := buildBackupSpec(ctx)
	require.NoError(t, err)
	require.Len(t, got.Schedule, 2)

	assert.Equal(t, seaweedv1.BackupScheduleSpec{
		Name:        "nightly",
		Schedule:    "0 2 * * *",
		StorageName: "my-s3",
		Keep:        7,
		FilerPath:   "/",
		Suspend:     false,
	}, got.Schedule[0])
	assert.Equal(t, seaweedv1.BackupScheduleSpec{
		Name:        "paused",
		Schedule:    "0 4 * * 0",
		StorageName: "my-s3",
		Keep:        0,
		FilerPath:   "/",
		Suspend:     true,
	}, got.Schedule[1])
}

func TestBuildBackupSpecScheduleFilerPathParameter(t *testing.T) {
	bs := s3BackupStorage("my-s3", pointer.To(false))
	instance := enabledBackupInstance("my-s3")
	instance.Spec.Backup.Storages[0].Schedules = []corev1alpha1.InstanceBackupSchedule{{
		Name:       "subtree",
		Enabled:    true,
		Cron:       "0 3 * * *",
		Parameters: &runtime.RawExtension{Raw: []byte(`{"filerPath":"/buckets"}`)},
	}}
	ctx := backupSpecTestContext(t, instance, bs)

	got, err := buildBackupSpec(ctx)
	require.NoError(t, err)
	require.Len(t, got.Schedule, 1)
	assert.Equal(t, "/buckets", got.Schedule[0].FilerPath)
}

func TestBuildBackupSpecRejectsTimeRetention(t *testing.T) {
	bs := s3BackupStorage("my-s3", pointer.To(false))
	instance := enabledBackupInstance("my-s3")
	instance.Spec.Backup.Storages[0].Schedules = []corev1alpha1.InstanceBackupSchedule{{
		Name:    "nightly",
		Enabled: true,
		Cron:    "0 2 * * *",
		Retention: &corev1alpha1.BackupScheduleRetention{
			Type:     corev1alpha1.BackupScheduleRetentionTypeTime,
			Duration: "30d",
		},
	}}
	ctx := backupSpecTestContext(t, instance, bs)

	_, err := buildBackupSpec(ctx)
	require.Error(t, err)
	bce := controller.AsBackupConfigError(err)
	require.NotNil(t, bce)
	assert.Equal(t, "UnsupportedRetention", bce.Reason)
	assert.Contains(t, bce.Message, "time retention")
}

func TestBuildBackupSpecRejectsCountRetentionWithoutCount(t *testing.T) {
	bs := s3BackupStorage("my-s3", pointer.To(false))
	instance := enabledBackupInstance("my-s3")
	instance.Spec.Backup.Storages[0].Schedules = []corev1alpha1.InstanceBackupSchedule{{
		Name:    "nightly",
		Enabled: true,
		Cron:    "0 2 * * *",
		Retention: &corev1alpha1.BackupScheduleRetention{
			Type: corev1alpha1.BackupScheduleRetentionTypeCount,
		},
	}}
	ctx := backupSpecTestContext(t, instance, bs)

	_, err := buildBackupSpec(ctx)
	require.Error(t, err)
	bce := controller.AsBackupConfigError(err)
	require.NotNil(t, bce)
	assert.Equal(t, "InvalidRetention", bce.Reason)
}

func TestBuildBackupSpecRejectsInvalidScheduleName(t *testing.T) {
	bs := s3BackupStorage("my-s3", pointer.To(false))
	instance := enabledBackupInstance("my-s3")
	instance.Spec.Backup.Storages[0].Schedules = []corev1alpha1.InstanceBackupSchedule{{
		Name:    "Nightly",
		Enabled: true,
		Cron:    "0 2 * * *",
	}}
	ctx := backupSpecTestContext(t, instance, bs)

	_, err := buildBackupSpec(ctx)
	require.Error(t, err)
	bce := controller.AsBackupConfigError(err)
	require.NotNil(t, bce)
	assert.Equal(t, "InvalidScheduleName", bce.Reason)
}

func TestBuildBackupSpecRejectsScheduleNameTooLong(t *testing.T) {
	bs := s3BackupStorage("my-s3", pointer.To(false))
	instance := enabledBackupInstance("my-s3")
	instance.Spec.Backup.Storages[0].Schedules = []corev1alpha1.InstanceBackupSchedule{{
		Name:    strings.Repeat("a", seaweedBackupStorageNameMax+1),
		Enabled: true,
		Cron:    "0 2 * * *",
	}}
	ctx := backupSpecTestContext(t, instance, bs)

	_, err := buildBackupSpec(ctx)
	require.Error(t, err)
	bce := controller.AsBackupConfigError(err)
	require.NotNil(t, bce)
	assert.Equal(t, "InvalidScheduleName", bce.Reason)
}

func TestBuildBackupSpecRejectsDuplicateScheduleNames(t *testing.T) {
	bsA := s3BackupStorage("s3-a", pointer.To(false))
	bsB := s3BackupStorage("s3-b", pointer.To(false))
	instance := &corev1alpha1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "sw", Namespace: "ns"},
		Spec: corev1alpha1.InstanceSpec{
			Backup: &corev1alpha1.InstanceBackupSpec{
				Enabled: true,
				Storages: []corev1alpha1.InstanceBackupStorage{
					{
						StorageRef: commonv1alpha1.ObjectRef{Name: "s3-a"},
						Schedules: []corev1alpha1.InstanceBackupSchedule{{
							Name: "nightly", Enabled: true, Cron: "0 2 * * *",
						}},
					},
					{
						StorageRef: commonv1alpha1.ObjectRef{Name: "s3-b"},
						Schedules: []corev1alpha1.InstanceBackupSchedule{{
							Name: "nightly", Enabled: true, Cron: "0 3 * * *",
						}},
					},
				},
			},
		},
	}
	ctx := backupSpecTestContext(t, instance, bsA, bsB)

	_, err := buildBackupSpec(ctx)
	require.Error(t, err)
	bce := controller.AsBackupConfigError(err)
	require.NotNil(t, bce)
	assert.Equal(t, "DuplicateSchedule", bce.Reason)
	assert.Contains(t, bce.Message, "nightly")
}

func TestBuildBackupSpecRejectsInvalidCron(t *testing.T) {
	bs := s3BackupStorage("my-s3", pointer.To(false))
	instance := enabledBackupInstance("my-s3")
	instance.Spec.Backup.Storages[0].Schedules = []corev1alpha1.InstanceBackupSchedule{{
		Name:    "nightly",
		Enabled: true,
		Cron:    "not-a-cron",
	}}
	ctx := backupSpecTestContext(t, instance, bs)

	_, err := buildBackupSpec(ctx)
	require.Error(t, err)
	bce := controller.AsBackupConfigError(err)
	require.NotNil(t, bce)
	assert.Equal(t, "InvalidScheduleCron", bce.Reason)
	assert.Contains(t, bce.Message, "not-a-cron")
}

func TestBuildBackupSpecRejectsEmptyCron(t *testing.T) {
	bs := s3BackupStorage("my-s3", pointer.To(false))
	instance := enabledBackupInstance("my-s3")
	instance.Spec.Backup.Storages[0].Schedules = []corev1alpha1.InstanceBackupSchedule{{
		Name:    "nightly",
		Enabled: true,
		Cron:    "",
	}}
	ctx := backupSpecTestContext(t, instance, bs)

	_, err := buildBackupSpec(ctx)
	require.Error(t, err)
	bce := controller.AsBackupConfigError(err)
	require.NotNil(t, bce)
	assert.Equal(t, "InvalidScheduleCron", bce.Reason)
}

func TestBuildBackupSpecRejectsCronTooLong(t *testing.T) {
	bs := s3BackupStorage("my-s3", pointer.To(false))
	instance := enabledBackupInstance("my-s3")
	instance.Spec.Backup.Storages[0].Schedules = []corev1alpha1.InstanceBackupSchedule{{
		Name:    "nightly",
		Enabled: true,
		Cron:    strings.Repeat("x", seaweedBackupCronMax+1),
	}}
	ctx := backupSpecTestContext(t, instance, bs)

	_, err := buildBackupSpec(ctx)
	require.Error(t, err)
	bce := controller.AsBackupConfigError(err)
	require.NotNil(t, bce)
	assert.Equal(t, "InvalidScheduleCron", bce.Reason)
	assert.Contains(t, bce.Message, "at most")
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

	exists, err := ctx.Exists(&seaweedv1.SeaweedBackup{}, "adhoc-delete")
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
	ctx := backupSpecTestContext(t, instance, sw, backup)

	p := &Provider{}
	exec, err := p.SyncBackup(ctx, backup)
	require.NoError(t, err)
	assert.Equal(t, backupv1alpha1.BackupStatePending, exec.State)
	require.NotNil(t, exec.OperatorBackupRef)
	assert.Equal(t, "SeaweedBackup", exec.OperatorBackupRef.Kind)
	assert.Equal(t, "adhoc-1", exec.OperatorBackupRef.Name)

	sb := &seaweedv1.SeaweedBackup{}
	require.NoError(t, ctx.Get(sb, "adhoc-1"))
	assert.Equal(t, "sw", sb.Spec.ClusterName)
	assert.Equal(t, "my-s3", sb.Spec.StorageName)
	assert.Equal(t, "/", sb.Spec.FilerPath)
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
	require.NoError(t, ctx.Get(sb, "adhoc-2"))
	assert.Equal(t, "/buckets", sb.Spec.FilerPath)
}

func TestSyncBackupSkipsScheduled(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	backup := onDemandBackup("nightly-1", "sw", "my-s3")
	backup.Spec.ScheduleName = "nightly"
	ctx := backupSpecTestContext(t, instance, backup)

	exec, err := (&Provider{}).SyncBackup(ctx, backup)
	require.NoError(t, err)
	assert.Equal(t, backupv1alpha1.BackupStatePending, exec.State)
	assert.Contains(t, exec.Message, "scheduled")

	exists, err := ctx.Exists(&seaweedv1.SeaweedBackup{}, "nightly-1")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestSyncBackupObservesScheduledSeaweedBackup(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	backup := onDemandBackup("nightly-1", "sw", "my-s3")
	backup.Spec.ScheduleName = "nightly"
	sb := &seaweedv1.SeaweedBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly-1", Namespace: "ns"},
		Spec: seaweedv1.SeaweedBackupSpec{
			ClusterName: "sw",
			StorageName: "my-s3",
		},
		Status: seaweedv1.SeaweedBackupStatus{Phase: seaweedv1.BackupPhaseCompleted},
	}
	ctx := backupSpecTestContext(t, instance, backup, sb)
	// Populate UID so SetControllerReference / IsControlledBy match the store.
	require.NoError(t, ctx.Get(backup, backup.Name))

	exec, err := (&Provider{}).SyncBackup(ctx, backup)
	require.NoError(t, err)
	assert.Equal(t, backupv1alpha1.BackupStateSucceeded, exec.State)

	require.NoError(t, ctx.Get(sb, "nightly-1"))
	assert.True(t, metav1.IsControlledBy(sb, backup), "scheduled SeaweedBackup must be owned by Backup for WatchOwned")
}

func TestSyncBackupDeletesMirroredBackupWhenSeaweedBackupGone(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	backup := onDemandBackup("nightly-1", "sw", "my-s3")
	backup.Spec.ScheduleName = "nightly"
	ref := seaweedBackupRef("nightly-1")
	backup.Status.OperatorBackupRef = &ref
	backup.Status.State = backupv1alpha1.BackupStateSucceeded
	ctx := backupSpecTestContext(t, instance, backup)

	exec, err := (&Provider{}).SyncBackup(ctx, backup)
	require.NoError(t, err)
	assert.Contains(t, exec.Message, "deleting mirrored Backup")

	exists, err := ctx.Exists(backup, "nightly-1")
	require.NoError(t, err)
	assert.False(t, exists, "mirrored Backup must be deleted when keep pruned SeaweedBackup")
}

func TestMirrorCreatesRetainScheduledBackup(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	sb := &seaweedv1.SeaweedBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sw-nightly-20261029120000",
			Namespace: "ns",
			Labels: map[string]string{
				seaweedv1.LabelBackupSchedule: "nightly",
				seaweedv1.LabelBackupCluster:  "sw",
			},
		},
		Spec: seaweedv1.SeaweedBackupSpec{
			ClusterName: "sw",
			StorageName: "my-s3",
			FilerPath:   "/",
		},
	}
	scheme := runtime.NewScheme()
	require.NoError(t, corev1alpha1.AddToScheme(scheme))
	require.NoError(t, backupv1alpha1.AddToScheme(scheme))
	require.NoError(t, seaweedv1.AddToScheme(scheme))
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance, sb).Build()

	got, err := (&Provider{BaseProvider: controller.BaseProvider{ProviderName: common.ProviderName}}).
		Mirror(context.Background(), cl, sb)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, sb.Name, got.Name)
	assert.Equal(t, "nightly", got.Spec.ScheduleName)
	assert.Equal(t, backupv1alpha1.BackupDeletionPolicyRetain, got.Spec.DeletionPolicy)
	assert.Equal(t, "my-s3", got.Spec.StorageRef.Name)
	assert.Equal(t, "seaweedfs-filer-meta", got.Spec.ClassRef.Name)
	require.NotNil(t, got.Spec.Origin.InstanceRef)
	assert.Equal(t, "sw", got.Spec.Origin.InstanceRef.Name)
}

func TestMirrorSkipsOnDemandSeaweedBackup(t *testing.T) {
	sb := &seaweedv1.SeaweedBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "adhoc-1", Namespace: "ns"},
		Spec: seaweedv1.SeaweedBackupSpec{
			ClusterName: "sw",
			StorageName: "my-s3",
		},
	}
	got, err := (&Provider{}).Mirror(context.Background(), nil, sb)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestMirrorSkipsOtherProvider(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	instance.Spec.ProviderRef.Name = "provider-other"
	sb := &seaweedv1.SeaweedBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sw-nightly-1",
			Namespace: "ns",
			Labels:    map[string]string{seaweedv1.LabelBackupSchedule: "nightly"},
		},
		Spec: seaweedv1.SeaweedBackupSpec{ClusterName: "sw", StorageName: "my-s3"},
	}
	scheme := runtime.NewScheme()
	require.NoError(t, corev1alpha1.AddToScheme(scheme))
	require.NoError(t, seaweedv1.AddToScheme(scheme))
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance, sb).Build()

	got, err := (&Provider{BaseProvider: controller.BaseProvider{ProviderName: common.ProviderName}}).
		Mirror(context.Background(), cl, sb)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSyncBackupDoesNotRecreateSucceeded(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	sw := seaweedClusterWithBackupStorage("sw", "my-s3")
	backup := onDemandBackup("adhoc-done", "sw", "my-s3")
	backup.Status.State = backupv1alpha1.BackupStateSucceeded
	ref := seaweedBackupRef("adhoc-done")
	backup.Status.OperatorBackupRef = &ref
	ctx := backupSpecTestContext(t, instance, sw, backup)

	exec, err := (&Provider{}).SyncBackup(ctx, backup)
	require.NoError(t, err)
	assert.Equal(t, backupv1alpha1.BackupStateSucceeded, exec.State)

	exists, err := ctx.Exists(&seaweedv1.SeaweedBackup{}, "adhoc-done")
	require.NoError(t, err)
	assert.False(t, exists, "must not recreate SeaweedBackup after Succeeded")
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
	sb := &seaweedv1.SeaweedBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "adhoc-1", Namespace: "ns"},
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

func TestCleanupBackupRejectsDeletionPolicyDelete(t *testing.T) {
	instance := enabledBackupInstance("my-s3")
	backup := onDemandBackup("adhoc-1", "sw", "my-s3")
	backup.Spec.DeletionPolicy = backupv1alpha1.BackupDeletionPolicyDelete
	sb := &seaweedv1.SeaweedBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "adhoc-1", Namespace: "ns"},
		Spec: seaweedv1.SeaweedBackupSpec{
			ClusterName: "sw",
			StorageName: "my-s3",
		},
	}
	ctx := backupSpecTestContext(t, instance, backup, sb)

	done, err := (&Provider{}).CleanupBackup(ctx, backup)
	require.Error(t, err)
	assert.False(t, done)
	assert.Contains(t, err.Error(), "deletionPolicy Delete")

	exists, err := ctx.Exists(&seaweedv1.SeaweedBackup{}, "adhoc-1")
	require.NoError(t, err)
	assert.True(t, exists, "must not delete SeaweedBackup when Delete is rejected")
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
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"},
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
			ProviderRef: commonv1alpha1.ObjectRef{Name: common.ProviderName},
			Backup: &corev1alpha1.InstanceBackupSpec{
				Enabled:  true,
				ClassRef: commonv1alpha1.ObjectRef{Name: "seaweedfs-filer-meta"},
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
