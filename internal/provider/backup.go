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
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/AlekSi/pointer"
	"github.com/robfig/cron/v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	seaweedfsfilermeta "github.com/openeverest/provider-seaweedfs/definition/backupclasses/seaweedfs-filer-meta"
)

// filerPathPattern rejects characters that can break out of the operator's
// single-quoted weed-shell command (quotes, whitespace, metacharacters).
var filerPathPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// Compile-time interface checks.
var _ controller.BackupProvider = (*Provider)(nil)
var _ controller.BackupWatcher = (*Provider)(nil)
var _ controller.RestoreWatcher = (*Provider)(nil)
var _ controller.BackupMirror = (*Provider)(nil)

// seaweedBackupStorageNameMax is the max length the seaweedfs-operator webhook
// accepts for spec.backup.storages map keys (and schedule names). Stricter than
// validation.DNS1123LabelMaxLength (63).
const seaweedBackupStorageNameMax = 50

// seaweedBackupCronMax is BackupScheduleSpec.Schedule's kubebuilder MaxLength
// on the seaweedfs-operator CRD.
const seaweedBackupCronMax = 120

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
	schedules, err := buildSeaweedSchedules(cfg.Storages)
	if err != nil {
		return nil, err
	}

	return &seaweedv1.BackupSpec{
		Storages:   storages,
		Schedule:   schedules,
		DataMirror: buildSeaweedDataMirrors(cfg.Storages),
	}, nil
}

// buildSeaweedDataMirrors returns one BackupMirrorSpec per registered storage.
// The operator listMapKey is storageName (at most one mirror per storage).
// One continuous weed filer.backup Deployment per registered storage.
// filerPath is always "/" so /.seaweedfs-operator/backups (and the rest
// of the tree) is included. Off only when backup itself is off.
func buildSeaweedDataMirrors(entries []corev1alpha1.InstanceBackupStorage) []seaweedv1.BackupMirrorSpec {
	out := make([]seaweedv1.BackupMirrorSpec, 0, len(entries))
	for _, entry := range entries {
		out = append(out, seaweedv1.BackupMirrorSpec{
			StorageName: entry.StorageRef.Name,
			FilerPath:   "/",
		})
	}
	return out
}

func buildSeaweedSchedules(entries []corev1alpha1.InstanceBackupStorage) ([]seaweedv1.BackupScheduleSpec, error) {
	var out []seaweedv1.BackupScheduleSpec
	// schedule name -> storage that registered it
	seen := make(map[string]string)
	for _, entry := range entries {
		storageName := entry.StorageRef.Name
		for _, sched := range entry.Schedules {
			if err := validateSeaweedBackupLabelName("backup schedule", sched.Name); err != nil {
				return nil, &controller.BackupConfigError{
					Reason:  "InvalidScheduleName",
					Message: err.Error(),
				}
			}
			if prev, ok := seen[sched.Name]; ok {
				return nil, &controller.BackupConfigError{
					Reason: "DuplicateSchedule",
					Message: fmt.Sprintf(
						"backup schedule %q is registered more than once (storages %q and %q)",
						sched.Name, prev, storageName,
					),
				}
			}
			seen[sched.Name] = storageName

			if err := validateScheduleCron(sched.Name, sched.Cron); err != nil {
				return nil, err
			}

			keep, err := mapScheduleKeep(sched)
			if err != nil {
				return nil, err
			}

			params, err := decodeBackupParameters(sched.Parameters)
			if err != nil {
				return nil, &controller.BackupConfigError{
					Reason:  "InvalidScheduleParameters",
					Message: err.Error(),
				}
			}
			filerPath := params.FilerPath
			if filerPath == "" {
				filerPath = "/"
			}
			if !validFilerPath(filerPath) {
				return nil, &controller.BackupConfigError{
					Reason: "InvalidScheduleParameters",
					Message: fmt.Sprintf(
						"schedule %q: invalid filerPath %q: must be an absolute, clean path of [A-Za-z0-9._/-]",
						sched.Name, filerPath,
					),
				}
			}

			out = append(out, seaweedv1.BackupScheduleSpec{
				Name:        sched.Name,
				Schedule:    sched.Cron,
				StorageName: storageName,
				Keep:        keep,
				FilerPath:   filerPath,
				Suspend:     !sched.Enabled,
			})
		}
	}
	return out, nil
}

func validateScheduleCron(scheduleName, expr string) error {
	if expr == "" {
		return &controller.BackupConfigError{
			Reason:  "InvalidScheduleCron",
			Message: fmt.Sprintf("schedule %q: cron is required", scheduleName),
		}
	}
	if len(expr) > seaweedBackupCronMax {
		return &controller.BackupConfigError{
			Reason: "InvalidScheduleCron",
			Message: fmt.Sprintf(
				"schedule %q: cron must be at most %d characters (seaweedfs-operator limit)",
				scheduleName, seaweedBackupCronMax,
			),
		}
	}
	if _, err := cron.ParseStandard(expr); err != nil {
		return &controller.BackupConfigError{
			Reason: "InvalidScheduleCron",
			Message: fmt.Sprintf(
				"schedule %q: invalid cron %q: %v",
				scheduleName, expr, err,
			),
		}
	}
	return nil
}

func mapScheduleKeep(sched corev1alpha1.InstanceBackupSchedule) (int32, error) {
	if sched.Retention == nil {
		return 0, nil
	}
	switch sched.Retention.Type {
	case corev1alpha1.BackupScheduleRetentionTypeCount:
		if sched.Retention.Count == nil {
			return 0, &controller.BackupConfigError{
				Reason:  "InvalidRetention",
				Message: fmt.Sprintf("schedule %q: retention type count requires count", sched.Name),
			}
		}
		return *sched.Retention.Count, nil
	case corev1alpha1.BackupScheduleRetentionTypeTime:
		return 0, &controller.BackupConfigError{
			Reason: "UnsupportedRetention",
			Message: fmt.Sprintf(
				"schedule %q: time retention is not supported (seaweedfs-operator only supports keep count); use retention type count",
				sched.Name,
			),
		}
	default:
		return 0, &controller.BackupConfigError{
			Reason:  "InvalidRetention",
			Message: fmt.Sprintf("schedule %q: unknown retention type %q", sched.Name, sched.Retention.Type),
		}
	}
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
	return validateSeaweedBackupLabelName("backup storage", name)
}

// validateSeaweedBackupLabelName enforces the seaweedfs-operator webhook rule for
// spec.backup.storages keys and schedule names: lowercase RFC 1123, ≤50 chars.
func validateSeaweedBackupLabelName(what, name string) error {
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		return fmt.Errorf("%s name %q must be a lowercase RFC 1123 label: %s", what, name, strings.Join(errs, "; "))
	}
	if len(name) > seaweedBackupStorageNameMax {
		return fmt.Errorf("%s name %q must be at most %d characters (seaweedfs-operator limit)", what, name, seaweedBackupStorageNameMax)
	}
	return nil
}

const seaweedBackupKind = "SeaweedBackup"

// seaweedBackupNamePrefixMax leaves room for "-" + 8-char UID suffix within
// validation.DNS1123LabelMaxLength (63). Catalog path is
// /.seaweedfs-operator/backups/<SeaweedBackup name>/filer.meta.gz — a
// UID-derived name keeps a Retain recreate of the same Backup name from
// overwriting the previous catalog.
const seaweedBackupNamePrefixMax = 54
const seaweedBackupUIDSuffixLen = 8

// deletionPolicyDeleteUnsupported is returned when Backup.spec.deletionPolicy
// is Delete (the OpenEverest default). The operator does not purge
// filer.meta.gz when a SeaweedBackup is deleted, and this provider does not
// remove the filer catalog either, so Delete would silently leave data behind.
// Require Retain until filer-catalog purge is implemented.
const deletionPolicyDeleteUnsupported = "deletionPolicy Delete is not supported: filer catalog purge is not implemented; set deletionPolicy: Retain"

// SyncBackup creates a SeaweedBackup for on-demand Backups and maps operator
// status. Scheduled Backups (spec.scheduleName set) are observed only — the
// seaweedfs-operator scheduler owns those SeaweedBackups. OpenEverest inventory
// mirrors operator keep: when a previously observed scheduled SeaweedBackup is
// gone, SyncBackup deletes the mirrored Backup.
func (p *Provider) SyncBackup(c *controller.Context, backup *backupv1alpha1.Backup) (controller.BackupExecutionStatus, error) {
	l := log.FromContext(c.Context())
	l.Info("Syncing backup", "name", backup.Name)

	// Scheduled first so keep-pruned SeaweedBackups delete the mirrored Backup
	// even after Succeeded. On-demand Succeeded still short-circuits below so
	// we never recreate a pruned SeaweedBackup under the same name.
	if backup.Spec.ScheduleName != "" {
		return observeSeaweedBackup(c, backup)
	}

	if backup.Status.State == backupv1alpha1.BackupStateSucceeded {
		return preserveSucceededBackup(backup), nil
	}

	if err := requireRetainDeletionPolicy(backup); err != nil {
		return controller.BackupExecutionStatus{
			State:   backupv1alpha1.BackupStateFailed,
			Message: err.Error(),
		}, nil
	}

	storageName := backup.Spec.StorageRef.Name
	if msg := permanentStorageMisconfig(c, storageName); msg != "" {
		return controller.BackupExecutionStatus{
			State:   backupv1alpha1.BackupStateFailed,
			Message: msg,
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
		return controller.BackupExecutionStatus{
			State:   backupv1alpha1.BackupStateFailed,
			Message: err.Error(),
		}, nil
	}
	filerPath := params.FilerPath
	if filerPath == "" {
		filerPath = "/"
	}
	if !validFilerPath(filerPath) {
		return controller.BackupExecutionStatus{
			State:   backupv1alpha1.BackupStateFailed,
			Message: fmt.Sprintf("invalid filerPath %q: must be an absolute, clean path of [A-Za-z0-9._/-]", filerPath),
		}, nil
	}

	sbName, err := seaweedBackupName(backup)
	if err != nil {
		return controller.BackupExecutionStatus{
			State:   backupv1alpha1.BackupStateFailed,
			Message: err.Error(),
		}, nil
	}
	sb := &seaweedv1.SeaweedBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sbName,
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

// seaweedBackupName returns the SeaweedBackup object name for this Backup.
// Prefer status.operatorBackupRef.name once set so Sync/Cleanup stay stable;
// otherwise derive "<truncated-name>-<uid[:8]>".
func seaweedBackupName(backup *backupv1alpha1.Backup) (string, error) {
	if backup.Status.OperatorBackupRef != nil && backup.Status.OperatorBackupRef.Name != "" {
		return backup.Status.OperatorBackupRef.Name, nil
	}
	return deriveSeaweedBackupName(backup.Name, string(backup.UID))
}

func deriveSeaweedBackupName(name, uid string) (string, error) {
	if uid == "" {
		return "", fmt.Errorf("backup UID is required to derive SeaweedBackup name")
	}
	prefix := name
	if len(prefix) > seaweedBackupNamePrefixMax {
		prefix = prefix[:seaweedBackupNamePrefixMax]
	}
	suffix := uid
	if len(suffix) > seaweedBackupUIDSuffixLen {
		suffix = suffix[:seaweedBackupUIDSuffixLen]
	}
	return fmt.Sprintf("%s-%s", prefix, suffix), nil
}

func permanentStorageMisconfig(c *controller.Context, storageName string) string {
	cfg := c.Instance().Spec.Backup
	if cfg == nil || !cfg.Enabled {
		return fmt.Sprintf("backup is not enabled on Instance %q", c.Name())
	}
	for _, entry := range cfg.Storages {
		if entry.StorageRef.Name == storageName {
			return ""
		}
	}
	return fmt.Sprintf("storage %q is not registered in Instance.spec.backup.storages", storageName)
}

// validFilerPath accepts absolute, clean paths limited to safe path characters
// so values cannot break out of the operator's single-quoted weed shell.
func validFilerPath(p string) bool {
	return strings.HasPrefix(p, "/") && path.Clean(p) == p && filerPathPattern.MatchString(p)
}

// observeSeaweedBackup maps status for a scheduler-created SeaweedBackup
// without creating or mutating the operator object.
func observeSeaweedBackup(c *controller.Context, backup *backupv1alpha1.Backup) (controller.BackupExecutionStatus, error) {
	sb := &seaweedv1.SeaweedBackup{}
	if err := c.Get(sb, backup.Name); err != nil {
		if apierrors.IsNotFound(err) {
			if shouldDeleteOrphanedScheduledBackup(backup) {
				if err := c.Delete(backup); err != nil && !apierrors.IsNotFound(err) {
					return controller.BackupExecutionStatus{}, fmt.Errorf("delete pruned scheduled Backup: %w", err)
				}
				return controller.BackupExecutionStatus{
					State:   backupv1alpha1.BackupStatePending,
					Message: fmt.Sprintf("SeaweedBackup %q gone; deleting mirrored Backup", backup.Name),
				}, nil
			}
			return controller.BackupExecutionStatus{
				State:   backupv1alpha1.BackupStatePending,
				Message: fmt.Sprintf("Waiting for scheduled SeaweedBackup %q", backup.Name),
			}, nil
		}
		return controller.BackupExecutionStatus{}, fmt.Errorf("get SeaweedBackup: %w", err)
	}

	if err := ensureSeaweedBackupOwnedBy(c, backup, sb); err != nil {
		return controller.BackupExecutionStatus{}, err
	}
	return mapSeaweedBackupStatus(sb), nil
}

func shouldDeleteOrphanedScheduledBackup(backup *backupv1alpha1.Backup) bool {
	if backup.Status.OperatorBackupRef != nil {
		return true
	}
	switch backup.Status.State {
	case backupv1alpha1.BackupStateSucceeded, backupv1alpha1.BackupStateFailed, backupv1alpha1.BackupStateRunning:
		return true
	default:
		return false
	}
}

// ensureSeaweedBackupOwnedBy sets Backup as controller of SeaweedBackup when
// missing, so WatchOwned routes operator events to the mirrored Backup.
func ensureSeaweedBackupOwnedBy(c *controller.Context, backup *backupv1alpha1.Backup, sb *seaweedv1.SeaweedBackup) error {
	if metav1.IsControlledBy(sb, backup) {
		return nil
	}
	if err := controllerutil.SetControllerReference(backup, sb, c.Client().Scheme()); err != nil {
		return fmt.Errorf("set controller reference on SeaweedBackup: %w", err)
	}
	if err := c.Client().Update(c.Context(), sb); err != nil {
		return fmt.Errorf("adopt SeaweedBackup: %w", err)
	}
	return nil
}

// OperatorBackupType implements controller.BackupMirror.
func (p *Provider) OperatorBackupType() client.Object {
	return &seaweedv1.SeaweedBackup{}
}

// Mirror implements controller.BackupMirror. Scheduler-created SeaweedBackups
// carry LabelBackupSchedule; on-demand ones (created by SyncBackup) do not and
// are skipped. Mirrored Backups use deletionPolicy Retain — Delete would claim
// to purge filer.meta.gz, which this provider does not do.
func (p *Provider) Mirror(ctx context.Context, c client.Client, obj client.Object) (*backupv1alpha1.Backup, error) {
	sb, ok := obj.(*seaweedv1.SeaweedBackup)
	if !ok {
		return nil, nil
	}
	scheduleName := sb.Labels[seaweedv1.LabelBackupSchedule]
	if scheduleName == "" {
		// On-demand backups originate from a Backup CR- no mirroring needed.
		return nil, nil
	}

	instance := &corev1alpha1.Instance{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: sb.Namespace, Name: sb.Spec.ClusterName}, instance); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get parent Instance %q: %w", sb.Spec.ClusterName, err)
	}
	if instance.Spec.ProviderRef.Name != p.Name() {
		return nil, nil
	}
	if instance.Spec.Backup == nil || instance.Spec.Backup.ClassRef.Name == "" {
		return nil, nil
	}

	return &backupv1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sb.Name,
			Namespace: sb.Namespace,
		},
		Spec: backupv1alpha1.BackupSpec{
			Origin: backupv1alpha1.BackupOrigin{
				Type:        backupv1alpha1.BackupOriginTypeInstance,
				InstanceRef: &commonv1alpha1.ObjectRef{Name: sb.Spec.ClusterName},
			},
			ClassRef:       commonv1alpha1.ObjectRef{Name: instance.Spec.Backup.ClassRef.Name},
			StorageRef:     commonv1alpha1.ObjectRef{Name: sb.Spec.StorageName},
			ScheduleName:   scheduleName,
			DeletionPolicy: backupv1alpha1.BackupDeletionPolicyRetain,
		},
	}, nil
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
		return exec
	}
	if name, err := seaweedBackupName(backup); err == nil {
		ref := seaweedBackupRef(name)
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

// CleanupBackup deletes the SeaweedBackup CR. Filer-catalog purge is not
// implemented, so deletionPolicy Delete only removes the operator CR (catalog
// left behind). Look up the CR via status.operatorBackupRef.name (UID-derived).
// Return true when fully gone.
func (p *Provider) CleanupBackup(c *controller.Context, backup *backupv1alpha1.Backup) (bool, error) {
	l := log.FromContext(c.Context())
	l.Info("Cleaning up backup", "name", backup.Name)

	if !c.ShouldRetainBackupData(backup) {
		// Filer-catalog purge is not implemented; the catalog is left behind.
		l.Info("deletionPolicy Delete: removing SeaweedBackup only; filer catalog is not purged", "name", backup.Name)
	}

	sbName, err := seaweedBackupName(backup)
	if err != nil {
		// No ref and no UID — nothing we can look up; treat as gone.
		return true, nil
	}

	sb := &seaweedv1.SeaweedBackup{}
	if err := c.Get(sb, sbName); err != nil {
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
