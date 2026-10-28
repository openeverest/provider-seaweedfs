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
	"fmt"
	"strings"

	"github.com/AlekSi/pointer"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/log"

	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"
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

// SyncBackup creates or updates the operator's backup resource, sets a controller
// reference from the Backup CR to enable owner-based watches, and maps operator
// status to OpenEverest states.
func (p *Provider) SyncBackup(c *controller.Context, backup *backupv1alpha1.Backup) (controller.BackupExecutionStatus, error) {
	l := log.FromContext(c.Context())
	l.Info("Syncing backup", "name", backup.Name)

	// TODO: Implement backup sync logic.
	return controller.BackupExecutionStatus{
		State: backupv1alpha1.BackupStatePending,
	}, nil
}

// SyncRestore resolves the source Backup CR, creates or updates the operator's
// restore resource with a controller reference, and maps operator status to
// OpenEverest states.
func (p *Provider) SyncRestore(c *controller.Context, restore *backupv1alpha1.Restore) (controller.RestoreExecutionStatus, error) {
	l := log.FromContext(c.Context())
	l.Info("Syncing restore", "name", restore.Name)

	// TODO: Implement restore sync logic.
	return controller.RestoreExecutionStatus{
		State: backupv1alpha1.RestoreStatePending,
	}, nil
}

// CleanupBackup deletes the operator backup resource. For DeletionPolicy: Retain,
// remove storage-protection finalizers before deletion to preserve backup data.
// Return true only when fully deleted, false to requeue.
func (p *Provider) CleanupBackup(c *controller.Context, backup *backupv1alpha1.Backup) (bool, error) {
	l := log.FromContext(c.Context())
	l.Info("Cleaning up backup", "name", backup.Name)

	// TODO: Implement backup cleanup logic.
	return true, nil
}

// CleanupRestore deletes the operator restore resource. Return true when fully
// deleted, false to requeue.
func (p *Provider) CleanupRestore(c *controller.Context, restore *backupv1alpha1.Restore) (bool, error) {
	l := log.FromContext(c.Context())
	l.Info("Cleaning up restore", "name", restore.Name)

	// TODO: Implement restore cleanup logic.
	return true, nil
}

// BackupWatches implements controller.BackupWatcher. Register watches so operator
// backup status changes trigger reconciliation. Use WatchOwned for resources with
// controller references set by SyncBackup.
func (p *Provider) BackupWatches() []controller.WatchConfig {
	return []controller.WatchConfig{}
}

// RestoreWatches implements controller.RestoreWatcher. Register watches so operator
// restore status changes trigger reconciliation. Use WatchOwned for resources with
// controller references set by SyncRestore.
func (p *Provider) RestoreWatches() []controller.WatchConfig {
	return []controller.WatchConfig{}
}
