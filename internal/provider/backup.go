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
	"fmt"
	"strings"

	"github.com/AlekSi/pointer"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	seaweedfsfilermeta "github.com/openeverest/provider-seaweedfs/definition/backupclasses/seaweedfs-filer-meta"
)

// Compile-time interface checks.
var _ controller.BackupProvider = (*Provider)(nil)
var _ controller.BackupWatcher = (*Provider)(nil)
var _ controller.RestoreWatcher = (*Provider)(nil)

// seaweedBackupStorageNameMax is the max length the seaweedfs-operator webhook
// accepts for spec.backup.storages map keys (and schedule names). Stricter than
// validation.DNS1123LabelMaxLength (63).
const seaweedBackupStorageNameMax = 50

// defaultBackupDirectory is the sink prefix OpenEverest BackupStorage has no
// field for. The proposal and operator examples use "/".
const defaultBackupDirectory = "/"

func buildBackupSpec(c *controller.Context) (*seaweedv1.BackupSpec, error) {
	cfg := c.Instance().Spec.Backup
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	}
	if len(cfg.Storages) == 0 {
		return nil, nil
	}

	storages, err := buildSeaweedStorages(c, cfg.Storages)
	if err != nil {
		return nil, err
	}
	return &seaweedv1.BackupSpec{Storages: storages}, nil
}

func buildSeaweedStorages(
	c *controller.Context,
	entries []corev1alpha1.InstanceBackupStorage,
) (map[string]seaweedv1.BackupStorageSpec, error) {
	out := make(map[string]seaweedv1.BackupStorageSpec, len(entries))
	for _, entry := range entries {
		name := entry.StorageRef.Name
		if err := validateSeaweedBackupStorageName(name); err != nil {
			return nil, &controller.BackupConfigError{
				Reason:  "InvalidStorageName",
				Message: err.Error(),
			}
		}
		if _, exists := out[name]; exists {
			return nil, &controller.BackupConfigError{
				Reason:  "DuplicateStorage",
				Message: fmt.Sprintf("backup storage %q is registered more than once", name),
			}
		}
		if entry.PITR != nil && entry.PITR.Enabled {
			return nil, &controller.BackupConfigError{
				Reason:  controller.PITRConfigInvalidReason,
				Message: fmt.Sprintf("storage %q: PITR is not supported for SeaweedFS filer metadata backups", name),
			}
		}

		bs, err := c.BackupStorage(name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil, controller.WaitFor(fmt.Sprintf("BackupStorage %q not yet present", name))
			}
			return nil, err
		}

		spec, err := mapBackupStorageToSeaweed(bs)
		if err != nil {
			return nil, err
		}
		out[name] = *spec
	}
	return out, nil
}

// mapBackupStorageToSeaweed translates one OpenEverest BackupStorage into the
// operator's BackupStorageSpec. Only type s3 is supported.
func mapBackupStorageToSeaweed(bs *backupv1alpha1.BackupStorage) (*seaweedv1.BackupStorageSpec, error) {
	if bs.Spec.Type != backupv1alpha1.BackupStorageTypeS3 || bs.Spec.S3 == nil {
		return nil, &controller.BackupConfigError{
			Reason:  "UnsupportedStorageType",
			Message: fmt.Sprintf("BackupStorage %q: only type s3 is supported", bs.Name),
		}
	}

	s3 := bs.Spec.S3
	// OpenEverest defaults verifyTLS to true. The operator has no equivalent
	// field; false would silently become verified TLS on the mirror/sink.
	if s3.VerifyTLS != nil && !*s3.VerifyTLS {
		return nil, &controller.BackupConfigError{
			Reason:  "VerifyTLSUnsupported",
			Message: fmt.Sprintf("BackupStorage %q: verifyTLS false is not supported (seaweedfs-operator has no insecure TLS option)", bs.Name),
		}
	}

	credName := s3.CredentialsSecretRef.Name
	if credName == "" {
		return nil, &controller.BackupConfigError{
			Reason:  "MissingCredentialsSecret",
			Message: fmt.Sprintf("BackupStorage %q: credentialsSecretRef.name is required", bs.Name),
		}
	}

	// OpenEverest defaults forcePathStyle to false; the operator defaults the
	// omitted field to true. Always set the pointer so false stays false.
	forcePath := false
	if s3.ForcePathStyle != nil {
		forcePath = *s3.ForcePathStyle
	}

	return &seaweedv1.BackupStorageSpec{
		Type: seaweedv1.BackupStorageS3,
		S3: &seaweedv1.S3BackupStore{
			Bucket:         s3.Bucket,
			Region:         s3.Region,
			Endpoint:       s3.EndpointURL,
			Directory:      defaultBackupDirectory,
			ForcePathStyle: pointer.To(forcePath),
		},
		CredentialsSecret: pointer.To(credName),
	}, nil
}

func validateSeaweedBackupStorageName(name string) error {
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		return fmt.Errorf("backup storage name %q must be a lowercase RFC 1123 label: %s", name, strings.Join(errs, "; "))
	}
	if len(name) > seaweedBackupStorageNameMax {
		return fmt.Errorf("backup storage name %q must be at most %d characters (seaweedfs-operator limit)", name, seaweedBackupStorageNameMax)
	}
	return nil
}

const seaweedBackupKind = "SeaweedBackup"

// deletionPolicyDeleteUnsupported is returned when Backup.spec.deletionPolicy
// is Delete (the OpenEverest default). The operator does not purge
// filer.meta.gz when a SeaweedBackup is deleted, and this provider does not
// remove the filer catalog either, so Delete would silently leave data behind.
// Require Retain until filer-catalog purge is implemented.
const deletionPolicyDeleteUnsupported = "deletionPolicy Delete is not supported: filer catalog purge is not implemented; set deletionPolicy: Retain"

// SyncBackup creates a SeaweedBackup for on-demand Backups and maps operator
// status. Scheduled Backups (spec.scheduleName set) are observed only — the
// seaweedfs-operator scheduler owns those SeaweedBackups.
func (p *Provider) SyncBackup(c *controller.Context, backup *backupv1alpha1.Backup) (controller.BackupExecutionStatus, error) {
	l := log.FromContext(c.Context())
	l.Info("Syncing backup", "name", backup.Name)

	if backup.Spec.ScheduleName != "" {
		return observeSeaweedBackup(c, backup)
	}

	// Do not recreate a pruned SeaweedBackup under the same name; keep would
	// delete it again and we would snapshot forever.
	if backup.Status.State == backupv1alpha1.BackupStateSucceeded {
		return preserveSucceededBackup(backup), nil
	}

	if err := requireRetainDeletionPolicy(backup); err != nil {
		return controller.BackupExecutionStatus{
			State:   backupv1alpha1.BackupStateFailed,
			Message: err.Error(),
		}, nil
	}

	sw := &seaweedv1.Seaweed{}
	if err := c.Get(sw, c.Name()); err != nil {
		if apierrors.IsNotFound(err) {
			return controller.BackupExecutionStatus{
				State:   backupv1alpha1.BackupStatePending,
				Message: "Waiting for Seaweed cluster to exist",
			}, nil
		}
		return controller.BackupExecutionStatus{}, fmt.Errorf("get Seaweed: %w", err)
	}

	storageName := backup.Spec.StorageRef.Name
	if sw.Spec.Backup == nil {
		return controller.BackupExecutionStatus{
			State:   backupv1alpha1.BackupStatePending,
			Message: "Waiting for backup storages to be registered on Seaweed cluster",
		}, nil
	}
	if _, ok := sw.Spec.Backup.Storages[storageName]; !ok {
		return controller.BackupExecutionStatus{
			State:   backupv1alpha1.BackupStatePending,
			Message: fmt.Sprintf("Waiting for storage %q to be registered on Seaweed cluster", storageName),
		}, nil
	}

	params, err := decodeBackupParameters(backup.Spec.Parameters)
	if err != nil {
		return controller.BackupExecutionStatus{}, err
	}
	filerPath := params.FilerPath
	if filerPath == "" {
		filerPath = "/"
	}

	sb := &seaweedv1.SeaweedBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backup.Name,
			Namespace: backup.Namespace,
		},
	}
	if _, err := controllerutil.CreateOrUpdate(c.Context(), c.Client(), sb, func() error {
		// clusterName and storageName are immutable on SeaweedBackup.
		if sb.CreationTimestamp.IsZero() {
			sb.Spec = seaweedv1.SeaweedBackupSpec{
				ClusterName: c.Name(),
				StorageName: storageName,
				FilerPath:   filerPath,
			}
		}
		return controllerutil.SetControllerReference(backup, sb, c.Client().Scheme())
	}); err != nil {
		return controller.BackupExecutionStatus{}, fmt.Errorf("create or update SeaweedBackup: %w", err)
	}

	return mapSeaweedBackupStatus(sb), nil
}

// observeSeaweedBackup maps status for a scheduler-created SeaweedBackup
// without creating or mutating the operator object.
func observeSeaweedBackup(c *controller.Context, backup *backupv1alpha1.Backup) (controller.BackupExecutionStatus, error) {
	sb := &seaweedv1.SeaweedBackup{}
	if err := c.Get(sb, backup.Name); err != nil {
		if apierrors.IsNotFound(err) {
			return controller.BackupExecutionStatus{
				State:   backupv1alpha1.BackupStatePending,
				Message: fmt.Sprintf("Waiting for scheduled SeaweedBackup %q", backup.Name),
			}, nil
		}
		return controller.BackupExecutionStatus{}, fmt.Errorf("get SeaweedBackup: %w", err)
	}
	return mapSeaweedBackupStatus(sb), nil
}

func preserveSucceededBackup(backup *backupv1alpha1.Backup) controller.BackupExecutionStatus {
	exec := controller.BackupExecutionStatus{
		State:       backupv1alpha1.BackupStateSucceeded,
		StartedAt:   backup.Status.StartedAt,
		CompletedAt: backup.Status.CompletedAt,
		Message:     backup.Status.Message,
	}
	if backup.Status.OperatorBackupRef != nil {
		exec.OperatorBackupRef = backup.Status.OperatorBackupRef
	} else {
		ref := seaweedBackupRef(backup.Name)
		exec.OperatorBackupRef = &ref
	}
	return exec
}

func mapSeaweedBackupStatus(sb *seaweedv1.SeaweedBackup) controller.BackupExecutionStatus {
	ref := seaweedBackupRef(sb.Name)
	exec := controller.BackupExecutionStatus{
		OperatorBackupRef: &ref,
		StartedAt:         sb.Status.StartTime,
		CompletedAt:       sb.Status.CompletionTime,
		State:             backupv1alpha1.BackupStatePending,
	}
	switch sb.Status.Phase {
	case seaweedv1.BackupPhaseCompleted:
		exec.State = backupv1alpha1.BackupStateSucceeded
	case seaweedv1.BackupPhaseRunning:
		exec.State = backupv1alpha1.BackupStateRunning
	case seaweedv1.BackupPhaseFailed:
		exec.State = backupv1alpha1.BackupStateFailed
		exec.Message = failedBackupMessage(sb)
	case seaweedv1.BackupPhasePending, "":
		exec.State = backupv1alpha1.BackupStatePending
	default:
		exec.State = backupv1alpha1.BackupStatePending
	}
	return exec
}

func seaweedBackupRef(name string) commonv1alpha1.TypedObjectRef {
	return commonv1alpha1.TypedObjectRef{
		Group: seaweedv1.GroupVersion.Group,
		Kind:  seaweedBackupKind,
		Name:  name,
	}
}

func failedBackupMessage(sb *seaweedv1.SeaweedBackup) string {
	for _, cond := range sb.Status.Conditions {
		if cond.Type == seaweedv1.BackupConditionComplete && cond.Status == metav1.ConditionFalse && cond.Message != "" {
			return cond.Message
		}
	}
	return "SeaweedBackup failed"
}

func decodeBackupParameters(raw *runtime.RawExtension) (seaweedfsfilermeta.SeaweedfsFilerMetaBackupParameters, error) {
	var cfg seaweedfsfilermeta.SeaweedfsFilerMetaBackupParameters
	if raw == nil || len(raw.Raw) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(raw.Raw, &cfg); err != nil {
		return cfg, fmt.Errorf("decode backup parameters: %w", err)
	}
	return cfg, nil
}

// SyncRestore is not implemented yet. Return Failed so Restore CRs do not
// sit in Pending and requeue forever.
func (p *Provider) SyncRestore(c *controller.Context, restore *backupv1alpha1.Restore) (controller.RestoreExecutionStatus, error) {
	l := log.FromContext(c.Context())
	l.Info("Syncing restore", "name", restore.Name)

	return controller.RestoreExecutionStatus{
		State:   backupv1alpha1.RestoreStateFailed,
		Message: "restore is not implemented for seaweedfs-filer-meta",
	}, nil
}

// CleanupBackup deletes the SeaweedBackup CR when deletionPolicy is Retain.
// Delete is rejected: this provider does not purge
// /.seaweedfs-operator/backups/<name>/filer.meta.gz from the filer.
// Return true when fully gone.
func (p *Provider) CleanupBackup(c *controller.Context, backup *backupv1alpha1.Backup) (bool, error) {
	l := log.FromContext(c.Context())
	l.Info("Cleaning up backup", "name", backup.Name)

	if err := requireRetainDeletionPolicy(backup); err != nil {
		return false, err
	}

	sb := &seaweedv1.SeaweedBackup{}
	if err := c.Get(sb, backup.Name); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, fmt.Errorf("get SeaweedBackup: %w", err)
	}
	if sb.DeletionTimestamp.IsZero() {
		if err := c.Delete(sb); err != nil {
			return false, fmt.Errorf("delete SeaweedBackup: %w", err)
		}
	}
	return false, nil
}

// CleanupRestore has nothing to delete until SyncRestore creates a
// SeaweedRestore. Return true so the Restore finalizer can be removed.
func (p *Provider) CleanupRestore(c *controller.Context, restore *backupv1alpha1.Restore) (bool, error) {
	l := log.FromContext(c.Context())
	l.Info("Cleaning up restore", "name", restore.Name)

	return true, nil
}

func requireRetainDeletionPolicy(backup *backupv1alpha1.Backup) error {
	if backup.Spec.DeletionPolicy == backupv1alpha1.BackupDeletionPolicyRetain {
		return nil
	}
	return fmt.Errorf("%s", deletionPolicyDeleteUnsupported)
}

// BackupWatches implements controller.BackupWatcher.
func (p *Provider) BackupWatches() []controller.WatchConfig {
	return []controller.WatchConfig{
		controller.WatchOwned(&seaweedv1.SeaweedBackup{}),
	}
}

// RestoreWatches implements controller.RestoreWatcher.
func (p *Provider) RestoreWatches() []controller.WatchConfig {
	return []controller.WatchConfig{}
}
