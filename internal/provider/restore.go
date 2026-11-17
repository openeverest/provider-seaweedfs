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
	"path"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	seaweedfsfilermeta "github.com/openeverest/provider-seaweedfs/definition/backupclasses/seaweedfs-filer-meta"
)

// Compile-time interface check.
var _ controller.RestoreWatcher = (*Provider)(nil)

const seaweedRestoreKind = "SeaweedRestore"

func reservedFilerMetaPath(seaweedBackupName string) string {
	return path.Join("/.seaweedfs-operator/backups", seaweedBackupName, "filer.meta.gz")
}

// SyncRestore creates a SeaweedRestore with backupSource (not backupName) so
// restore survives keep pruning the SeaweedBackup.
func (p *Provider) SyncRestore(c *controller.Context, restore *backupv1alpha1.Restore) (controller.RestoreExecutionStatus, error) {
	l := log.FromContext(c.Context())
	l.Info("Syncing restore", "name", restore.Name)

	if restore.Status.State == backupv1alpha1.RestoreStateSucceeded {
		return preserveSucceededRestore(restore), nil
	}
	if restore.Status.State == backupv1alpha1.RestoreStateFailed && restore.Status.OperatorRestoreRef == nil {
		return controller.RestoreExecutionStatus{
			State:   backupv1alpha1.RestoreStateFailed,
			Message: restore.Status.Message,
		}, nil
	}

	if restore.Spec.DataSource.Type != backupv1alpha1.DataSourceTypeBackup {
		return controller.RestoreExecutionStatus{
			State:   backupv1alpha1.RestoreStateFailed,
			Message: "dataSource.type must be Backup; PointInTime is not supported for seaweedfs-filer-meta",
		}, nil
	}
	if restore.Spec.DataSource.Backup == nil || restore.Spec.DataSource.Backup.BackupRef.Name == "" {
		return controller.RestoreExecutionStatus{
			State:   backupv1alpha1.RestoreStateFailed,
			Message: "dataSource.backup.backupRef is required",
		}, nil
	}

	params, err := decodeRestoreParameters(restore.Spec.Parameters)
	if err != nil {
		return controller.RestoreExecutionStatus{
			State:   backupv1alpha1.RestoreStateFailed,
			Message: err.Error(),
		}, nil
	}

	// If we already own a SeaweedRestore, keep mapping its status even when
	// another Restore is also non-terminal (creation is what we serialize).
	// Do not re-probe the filer here so a mid-flight write cannot strand an
	// in-progress operator Job.
	srName := seaweedRestoreName(restore)
	existing := &seaweedv1.SeaweedRestore{}
	if err := c.Get(existing, srName); err == nil {
		return mapSeaweedRestoreStatus(existing), nil
	} else if !apierrors.IsNotFound(err) {
		return controller.RestoreExecutionStatus{}, fmt.Errorf("get SeaweedRestore: %w", err)
	}

	if blocking, err := otherActiveRestore(c, restore); err != nil {
		return controller.RestoreExecutionStatus{}, err
	} else if blocking != "" {
		return controller.RestoreExecutionStatus{
			State:   backupv1alpha1.RestoreStatePending,
			Message: fmt.Sprintf("Waiting for restore %q to finish (only one restore per Instance at a time)", blocking),
		}, nil
	}

	filerPath := params.FilerPath
	if filerPath == "" {
		filerPath = "/"
	}
	if !validFilerPath(filerPath) {
		return controller.RestoreExecutionStatus{
			State:   backupv1alpha1.RestoreStateFailed,
			Message: fmt.Sprintf("invalid filerPath %q: must be an absolute, clean path of [A-Za-z0-9._/-]", filerPath),
		}, nil
	}

	backup := &backupv1alpha1.Backup{}
	if err := c.Get(backup, restore.Spec.DataSource.Backup.BackupRef.Name); err != nil {
		if apierrors.IsNotFound(err) {
			return controller.RestoreExecutionStatus{
				State:   backupv1alpha1.RestoreStateFailed,
				Message: fmt.Sprintf("source Backup %q not found", restore.Spec.DataSource.Backup.BackupRef.Name),
			}, nil
		}
		return controller.RestoreExecutionStatus{}, fmt.Errorf("get source Backup: %w", err)
	}
	if msg := validateRestoreBackupOrigin(backup, c.Name()); msg != "" {
		return controller.RestoreExecutionStatus{
			State:   backupv1alpha1.RestoreStateFailed,
			Message: msg,
		}, nil
	}
	if backup.Status.State != backupv1alpha1.BackupStateSucceeded {
		return controller.RestoreExecutionStatus{
			State:   backupv1alpha1.RestoreStatePending,
			Message: fmt.Sprintf("Waiting for source Backup %q to succeed (state %q)", backup.Name, backup.Status.State),
		}, nil
	}

	storageName := backup.Spec.StorageRef.Name
	if storageName == "" {
		return controller.RestoreExecutionStatus{
			State:   backupv1alpha1.RestoreStateFailed,
			Message: "source Backup has empty storageRef",
		}, nil
	}

	sw := &seaweedv1.Seaweed{}
	if err := c.Get(sw, c.Name()); err != nil {
		if apierrors.IsNotFound(err) {
			return controller.RestoreExecutionStatus{
				State:   backupv1alpha1.RestoreStatePending,
				Message: "Waiting for Seaweed cluster to exist",
			}, nil
		}
		return controller.RestoreExecutionStatus{}, fmt.Errorf("get Seaweed: %w", err)
	}
	if sw.Spec.Backup == nil {
		return controller.RestoreExecutionStatus{
			State:   backupv1alpha1.RestoreStatePending,
			Message: "Waiting for backup storages to be registered on Seaweed cluster",
		}, nil
	}
	if _, ok := sw.Spec.Backup.Storages[storageName]; !ok {
		return controller.RestoreExecutionStatus{
			State:   backupv1alpha1.RestoreStatePending,
			Message: fmt.Sprintf("Waiting for storage %q to be registered on Seaweed cluster", storageName),
		}, nil
	}

	if err := ensureFilerPathEmptyForRestore(
		c.Context(),
		filerHTTPBaseURLFn(c.Name(), c.Namespace()),
		filerPath,
	); err != nil {
		if isFilerProbeTransient(err) {
			return controller.RestoreExecutionStatus{
				State:   backupv1alpha1.RestoreStatePending,
				Message: err.Error(),
			}, nil
		}
		return controller.RestoreExecutionStatus{
			State:   backupv1alpha1.RestoreStateFailed,
			Message: err.Error(),
		}, nil
	}

	sbName, err := seaweedBackupNameForRestore(backup)
	if err != nil {
		return controller.RestoreExecutionStatus{
			State:   backupv1alpha1.RestoreStateFailed,
			Message: err.Error(),
		}, nil
	}

	sr := &seaweedv1.SeaweedRestore{
		ObjectMeta: metav1.ObjectMeta{
			Name:      srName,
			Namespace: restore.Namespace,
		},
	}
	if _, err := controllerutil.CreateOrUpdate(c.Context(), c.Client(), sr, func() error {
		// clusterName / backupSource are effectively immutable for the Job.
		if sr.CreationTimestamp.IsZero() {
			sr.Spec = seaweedv1.SeaweedRestoreSpec{
				ClusterName: c.Name(),
				BackupSource: &seaweedv1.BackupSource{
					StorageName: storageName,
					MetaPath:    reservedFilerMetaPath(sbName),
				},
				FilerPath: filerPath,
			}
		}
		return controllerutil.SetControllerReference(restore, sr, c.Client().Scheme())
	}); err != nil {
		return controller.RestoreExecutionStatus{}, fmt.Errorf("create or update SeaweedRestore: %w", err)
	}

	return mapSeaweedRestoreStatus(sr), nil
}

// validateRestoreBackupOrigin requires the source Backup to have been produced
// by this Instance. External / cross-Instance restores are unsupported: the
// catalog path and volume chunks live on the producing cluster's filer.
func validateRestoreBackupOrigin(backup *backupv1alpha1.Backup, instanceName string) string {
	if backup.Spec.Origin.Type != backupv1alpha1.BackupOriginTypeInstance {
		return fmt.Sprintf(
			"source Backup %q origin type %q is not supported; only Instance-produced backups can be restored",
			backup.Name, backup.Spec.Origin.Type,
		)
	}
	if backup.Spec.Origin.InstanceRef == nil || backup.Spec.Origin.InstanceRef.Name == "" {
		return fmt.Sprintf("source Backup %q has empty origin.instanceRef", backup.Name)
	}
	if backup.Spec.Origin.InstanceRef.Name != instanceName {
		return fmt.Sprintf(
			"source Backup %q was produced by Instance %q; restore target is %q",
			backup.Name, backup.Spec.Origin.InstanceRef.Name, instanceName,
		)
	}
	return ""
}

// seaweedBackupNameForRestore resolves the SeaweedBackup object name used in
// the filer catalog path. Prefer status.operatorBackupRef; scheduled mirrored
// Backups reuse the SeaweedBackup name; on-demand derive from UID.
func seaweedBackupNameForRestore(backup *backupv1alpha1.Backup) (string, error) {
	if backup.Status.OperatorBackupRef != nil && backup.Status.OperatorBackupRef.Name != "" {
		return backup.Status.OperatorBackupRef.Name, nil
	}
	if backup.Spec.ScheduleName != "" {
		return backup.Name, nil
	}
	return seaweedBackupName(backup)
}

func seaweedRestoreName(restore *backupv1alpha1.Restore) string {
	if restore.Status.OperatorRestoreRef != nil && restore.Status.OperatorRestoreRef.Name != "" {
		return restore.Status.OperatorRestoreRef.Name
	}
	return restore.Name
}

// otherActiveRestore returns the name of another non-terminal Restore for this
// Instance that should run first. Among peers that have not finished, the
// earliest CreationTimestamp (then name) is allowed to create a SeaweedRestore;
// others stay Pending. That avoids a deadlock when two new Restores both have
// an empty status. Peers that can never run (being deleted, or source Backup
// missing so the runtime never reconciles them) are skipped.
func otherActiveRestore(c *controller.Context, current *backupv1alpha1.Restore) (string, error) {
	list := &backupv1alpha1.RestoreList{}
	if err := c.Client().List(c.Context(), list, client.InNamespace(current.Namespace)); err != nil {
		return "", fmt.Errorf("list restores: %w", err)
	}

	var winner *backupv1alpha1.Restore
	for i := range list.Items {
		r := &list.Items[i]
		if r.Spec.InstanceRef.Name != c.Name() {
			continue
		}
		ok, err := restoreCanBlockPeers(c, r)
		if err != nil {
			return "", err
		}
		if !ok {
			continue
		}
		if winner == nil || restoreRunsFirst(r, winner) {
			winner = r
		}
	}
	if winner == nil || (winner.Name == current.Name && winner.UID == current.UID) {
		return "", nil
	}
	return winner.Name, nil
}

// restoreCanBlockPeers reports whether r should serialize creation of other
// SeaweedRestores for this Instance.
func restoreCanBlockPeers(c *controller.Context, r *backupv1alpha1.Restore) (bool, error) {
	if !r.DeletionTimestamp.IsZero() {
		return false, nil
	}
	if !isActiveRestoreState(r.Status.State) {
		return false, nil
	}
	if r.Spec.DataSource.Backup == nil || r.Spec.DataSource.Backup.BackupRef.Name == "" {
		return true, nil
	}
	backup := &backupv1alpha1.Backup{}
	if err := c.Get(backup, r.Spec.DataSource.Backup.BackupRef.Name); err != nil {
		if apierrors.IsNotFound(err) {
			// Runtime never reconciles a Restore whose backupRef is missing, so
			// it would block peers forever if counted as active.
			return false, nil
		}
		return false, fmt.Errorf("get peer restore %q source Backup: %w", r.Name, err)
	}
	return true, nil
}

func isActiveRestoreState(state backupv1alpha1.RestoreState) bool {
	switch state {
	case backupv1alpha1.RestoreStateSucceeded, backupv1alpha1.RestoreStateFailed:
		return false
	default:
		// Pending, Running, Error, and empty (not yet observed).
		return true
	}
}

func restoreRunsFirst(a, b *backupv1alpha1.Restore) bool {
	if !a.CreationTimestamp.Equal(&b.CreationTimestamp) {
		return a.CreationTimestamp.Before(&b.CreationTimestamp)
	}
	return a.Name < b.Name
}

func preserveSucceededRestore(restore *backupv1alpha1.Restore) controller.RestoreExecutionStatus {
	exec := controller.RestoreExecutionStatus{
		State:       backupv1alpha1.RestoreStateSucceeded,
		StartedAt:   restore.Status.StartedAt,
		CompletedAt: restore.Status.CompletedAt,
		Message:     restore.Status.Message,
	}
	if restore.Status.OperatorRestoreRef != nil {
		exec.OperatorRestoreRef = restore.Status.OperatorRestoreRef
		return exec
	}
	ref := seaweedRestoreRef(seaweedRestoreName(restore))
	exec.OperatorRestoreRef = &ref
	return exec
}

func mapSeaweedRestoreStatus(sr *seaweedv1.SeaweedRestore) controller.RestoreExecutionStatus {
	ref := seaweedRestoreRef(sr.Name)
	exec := controller.RestoreExecutionStatus{
		OperatorRestoreRef: &ref,
		StartedAt:          sr.Status.StartTime,
		CompletedAt:        sr.Status.CompletionTime,
		State:              backupv1alpha1.RestoreStatePending,
	}
	switch sr.Status.Phase {
	case seaweedv1.RestorePhaseCompleted:
		exec.State = backupv1alpha1.RestoreStateSucceeded
		exec.Message = "Restore completed"
	case seaweedv1.RestorePhaseRunning:
		exec.State = backupv1alpha1.RestoreStateRunning
		exec.Message = "Restore job is running"
	case seaweedv1.RestorePhaseFailed:
		exec.State = backupv1alpha1.RestoreStateFailed
		exec.Message = failedRestoreMessage(sr)
	case seaweedv1.RestorePhasePending, "":
		exec.State = backupv1alpha1.RestoreStatePending
	default:
		exec.State = backupv1alpha1.RestoreStatePending
	}
	return exec
}

func seaweedRestoreRef(name string) commonv1alpha1.TypedObjectRef {
	return commonv1alpha1.TypedObjectRef{
		Group: seaweedv1.GroupVersion.Group,
		Kind:  seaweedRestoreKind,
		Name:  name,
	}
}

func failedRestoreMessage(sr *seaweedv1.SeaweedRestore) string {
	for _, cond := range sr.Status.Conditions {
		if cond.Type == seaweedv1.RestoreConditionComplete && cond.Status == metav1.ConditionFalse && cond.Message != "" {
			return cond.Message
		}
	}
	return "SeaweedRestore failed"
}

func decodeRestoreParameters(raw *runtime.RawExtension) (seaweedfsfilermeta.SeaweedfsFilerMetaRestoreParameters, error) {
	var cfg seaweedfsfilermeta.SeaweedfsFilerMetaRestoreParameters
	if raw == nil || len(raw.Raw) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(raw.Raw, &cfg); err != nil {
		return cfg, fmt.Errorf("decode restore parameters: %w", err)
	}
	return cfg, nil
}

// CleanupRestore deletes the SeaweedRestore CR. Return true when fully gone.
func (p *Provider) CleanupRestore(c *controller.Context, restore *backupv1alpha1.Restore) (bool, error) {
	l := log.FromContext(c.Context())
	l.Info("Cleaning up restore", "name", restore.Name)

	srName := seaweedRestoreName(restore)
	sr := &seaweedv1.SeaweedRestore{}
	if err := c.Get(sr, srName); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, fmt.Errorf("get SeaweedRestore: %w", err)
	}
	if sr.DeletionTimestamp.IsZero() {
		if err := c.Delete(sr); err != nil {
			return false, fmt.Errorf("delete SeaweedRestore: %w", err)
		}
	}
	return false, nil
}

// RestoreWatches implements controller.RestoreWatcher.
func (p *Provider) RestoreWatches() []controller.WatchConfig {
	return []controller.WatchConfig{
		controller.WatchOwned(&seaweedv1.SeaweedRestore{}),
	}
}
