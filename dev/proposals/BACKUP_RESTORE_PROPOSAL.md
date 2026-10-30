Backup and Restore seaweed docs [here](https://github.com/seaweedfs/seaweedfs-operator/blob/master/BACKUP_SUPPORT.md)

# Proposal

The SeaweedFS operator already has `SeaweedBackup` and `SeaweedRestore`. A restore into the cluster that is already running is a `SeaweedRestore` pointed at that same cluster. This uses the current OpenEverest v2 types (`backup.openeverest.io`): `BackupClass`, `Backup`, `Restore`, and `BackupStorage`. `BackupStorage` on that API is S3 only.

A backup here is a filer metadata snapshot (`fs.meta.save` / `fs.meta.load`). It is not a copy of the file bytes, and it is not point-in-time recovery. For an S3 storage the snapshot Job does not upload the catalog. It writes `/.seaweedfs-operator/backups/<name>/filer.meta.gz` on the filer. Restore reads that filer path back with `weed filer.cat`. It does not read the bucket.

## What maps to what

| OpenEverest | SeaweedFS operator |
|---|---|
| `BackupClass` | One class, `seaweedfs-filer-meta`, `executionMode: ProviderManaged`, `supportsPITR: false`, `maxPITREnabledStorages: 0`. The provider creates the SeaweedFS objects. OpenEverest does not run the backup Job itself. |
| `BackupStorage` | One entry in `Seaweed.spec.backup.storages`, keyed by the `BackupStorage` name. Type `s3`. |
| `Instance.spec.backup.storages[].schedules` | `Seaweed.spec.backup.schedule` |
| `Backup` (on demand) | `SeaweedBackup` created by the provider |
| Scheduled `SeaweedBackup` | Mirrored back as a `Backup` with `spec.scheduleName` set. The provider does not create these. |
| `Restore` into an existing `Instance` | `SeaweedRestore` with `backupSource` (not `backupName`) |

`Backup.spec.storageRef` must already be one of the storages registered on the Instance. The operator only accepts a storage listed on the `Seaweed`.

For S3, `storageRef` does not choose where the catalog is written. Every object-store snapshot is staged at the same filer path, `/.seaweedfs-operator/backups/<SeaweedBackup name>/filer.meta.gz`. Every data mirror copies that file. `storageName` is still set on the `SeaweedBackup` and on `backupSource`, because the restore Job uses the storage type to decide to read the filer instead of a PVC.

## Backup

The provider writes this onto the `Seaweed` when backup is enabled (storages
and schedules). `dataMirror` is still opt-in and not written by Sync yet — see
below.

```yaml
spec:
  backup:
    storages:
      my-s3:                       # BackupStorage.metadata.name
        type: s3
        s3:
          bucket: my-bucket        # BackupStorage.spec.s3.bucket
          region: us-west-2        # BackupStorage.spec.s3.region
          endpoint: https://s3.example.com
          directory: /             # fixed; BackupStorage has no directory field
          forcePathStyle: false    # always set; false must not become the operator default true
        credentialsSecret: my-s3-creds
    schedule:
      - name: nightly              # schedule name
        schedule: "0 2 * * *"      # schedule.cron
        storageName: my-s3
        keep: 7                    # retention type count, count 7
        filerPath: /               # schedule.parameters.filerPath, default /
        suspend: false             # enabled: false becomes suspend: true
    # Not written by Sync yet (later slice):
    # dataMirror: [...]
```

Disabled schedules stay on the list with `suspend: true` so toggling
`enabled` does not drop the definition. That diverges from the OpenEverest
API comment that a disabled schedule is "removed from the engine": Seaweed's
scheduler has an explicit `Suspend` field, and removing/re-adding a schedule
re-anchors first-fire timing. `keep: 0` and `suspend: false` are omitted on
the wire (`omitempty`); they are the operator defaults.

Field mapping for S3:

| `BackupStorage.spec.s3` | `Seaweed` storage |
|---|---|
| `bucket` | `s3.bucket` |
| `region` | `s3.region` |
| `endpointURL` | `s3.endpoint` |
| `forcePathStyle` | `s3.forcePathStyle` |
| `credentialsSecretRef.name` | `credentialsSecret` |

The Secret keys are the same on both sides: `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`.

OpenEverest defaults `forcePathStyle` to false. The operator defaults it to true when the field is omitted. The provider always sets `forcePathStyle` on the `Seaweed` storage (nil/`false` → `false`), so false stays false. An in-cluster MinIO or SeaweedFS endpoint needs `forcePathStyle: true` on the `BackupStorage`.

`verifyTLS: false` is rejected with a `BackupConfigured` condition (`VerifyTLSUnsupported`). The operator's S3 storage has no such field, so dropping it would make the mirror fail. The default `verifyTLS: true` is left unset on the `Seaweed`.

Storage map keys must pass the operator webhook: lowercase RFC 1123 label, at most 50 characters. Invalid names become `BackupConfigured`=`InvalidStorageName`. PITR on a storage is rejected (`PITRConfigInvalid`). A missing `BackupStorage` requeues Sync until it appears.

Schedule names use the same RFC 1123 / 50-character rule (`InvalidScheduleName`). Names must be unique across all storages on the Instance (`DuplicateSchedule`); the operator marks the list with `listMapKey=name` for SSA merge but does not webhook-enforce uniqueness, so the provider rejects duplicates up front. Cron is validated up front with the same `cron.ParseStandard` the operator scheduler uses, and must be at most 120 characters (operator CRD `MaxLength`) — failures are `InvalidScheduleCron`. Without that, Sync would succeed and the schedule would only fail later inside the operator tick. Schedule `filerPath` (from `parameters`) is validated with the same absolute/clean/`[A-Za-z0-9._/-]` rule as on-demand backups (`InvalidScheduleParameters`); the operator interpolates it into a single-quoted `fs.meta.save` shell.

An on-demand `Backup` becomes a `SeaweedBackup`:

```yaml
apiVersion: seaweed.seaweedfs.com/v1
kind: SeaweedBackup
metadata:
  name: adhoc-1
spec:
  clusterName: seaweed-sample
  storageName: my-s3
  filerPath: /
```

That runs `fs.meta.save` once and stages the catalog on the filer. `Completed` means that Job finished. Nothing checks that a mirror copied the file to the bucket, or that a mirror is running.

`SyncBackup` creates a `SeaweedBackup` for an on-demand `Backup` only. It does not create one for a `Backup` with `spec.scheduleName` set (those are observed only). For on-demand it also does not create one again when the `Backup` is already `Succeeded` — otherwise a pruned `SeaweedBackup` would be recreated under the old name, a new snapshot would run, and `keep` would prune it again. Scheduled inventory follows operator `keep` instead (see below).

`deletionPolicy: Delete` is rejected (`SyncBackup` → `Failed`, `CleanupBackup` errors). The provider does not purge `filer.meta.gz` from the filer, so Delete would silently leave data behind. Use `deletionPolicy: Retain`. `CleanupBackup` then deletes only the `SeaweedBackup` CR and leaves the filer catalog (same as operator `keep`).

Scheduled backups stay on the SeaweedFS scheduler. The provider's `BackupMirror`
watches `SeaweedBackup` and mirrors objects that carry the operator label
`seaweed.seaweedfs.com/backup-schedule` (absent on on-demand backups created by
`SyncBackup`). Each mirrored OpenEverest `Backup` reuses the `SeaweedBackup`
name, sets `spec.scheduleName` from that label, and forces
`deletionPolicy: Retain`.

`SyncBackup` for `spec.scheduleName` backups is observe-only: it does not create
the `SeaweedBackup`. On first observation it adopts the `SeaweedBackup` with a
controller owner reference from the `Backup`, so `BackupWatches`
`WatchOwned(SeaweedBackup)` enqueues on status changes and when `keep` deletes
the object. OpenEverest inventory mirrors operator `keep`: once the `Backup`
has been observed (`status.operatorBackupRef` or a Running/Succeeded/Failed
state), a missing `SeaweedBackup` means prune (or an external delete) removed
it — the provider deletes the mirrored `Backup`, including Succeeded ones.
(Filer `filer.meta.gz` is still left behind; same as operator keep.)

Status maps as `Pending` → `Pending`, `Running` → `Running`, `Completed` → `Succeeded`, `Failed` → `Failed`. `Succeeded` means the catalog is on the filer. It does not mean the bucket has it. `status.operatorBackupRef` points at the `SeaweedBackup`.

`filerPath` is the only backup parameter that changes the snapshot. It defaults to `/`.

Retention maps only for a count. `retention: {type: count, count: 7}` becomes `keep: 7`. Omitting retention becomes `keep: 0`, which keeps every `SeaweedBackup`. Time retention (`30d`, `4w`, `2m`) is rejected with `BackupConfigured`=`UnsupportedRetention`. It is not ignored.

`keep` deletes old `SeaweedBackup` objects and their Jobs. It does not delete `filer.meta.gz`.

`deletionPolicy: Delete` is the OpenEverest default but is rejected until filer-catalog purge is implemented. On-demand and mirrored scheduled backups must use `Retain`. Mirrored scheduled backups are created with `Retain`.

Storage and schedule names must be lowercase RFC 1123 labels of at most 50 characters. That is what the operator accepts as `spec.backup.storages` keys and schedule names.

### Data mirror

Off unless the user opts in. One mirror per registered storage is a continuous `weed filer.backup -initialSnapshot` Deployment. It copies every file under the mirrored path, not just the catalog, and it re-walks that tree on every pod start. Restore does not read it. The mirror is also the only thing that carries the catalog off the cluster. With it left off, the catalog stays on the filer.

When it is on, the provider adds:

```yaml
dataMirror:
  - storageName: my-s3
    filerPath: /
```

`filerPath` is `/` so the hidden backup directory is included. How the user opts in is decided at implementation.

## Restore into the existing cluster

```yaml
apiVersion: seaweed.seaweedfs.com/v1
kind: SeaweedRestore
metadata:
  name: restore-1
spec:
  clusterName: seaweed-sample
  backupSource:
    storageName: my-s3
    metaPath: /.seaweedfs-operator/backups/adhoc-1/filer.meta.gz
  filerPath: /
```

`backupName` is not used. It needs the `SeaweedBackup` to still exist and be `Completed`, and `keep` deletes that object. `backupSource` only needs the catalog file, which `keep` does not delete.

`Restore.spec.instanceRef` is the cluster that already exists. The provider does not create a new `Seaweed`. `dataSource.type` must be `Backup`. `PointInTime` is rejected because this class does not support PITR.

`fs.meta.load` uses `CreateEntry`. On a path that already exists, that deletes chunks that are on the current entry and not on the restored one. On a live filer:

- A file changed since the backup loses its current chunks, and the restored entry points at chunks that are already gone.
- A file deleted since the backup comes back, but its chunks are gone.
- A file created since the backup is left in place. The load does not roll the cluster back.

Restore is only safe when the filer's metadata is empty or lost and the volume data is still on this cluster. The provider gates it, either with an explicit metadata-only disaster recovery parameter or by refusing a filer that is not empty. Which gate is decided at implementation.

The operator's restore Job does not stop the S3 gateway. Whether this provider stops client traffic during `fs.meta.load` is decided at implementation. Writes during the load race with `CreateEntry`.

Only one restore per Instance runs at a time.

`Instance.spec.dataSource` (a new Instance from a backup) is rejected. The file bytes are on the source cluster's volumes, so a new cluster cannot be seeded from this backup.

## What does not map

- **Data mirror is not PITR.** The mirror is a continuous copy of file bytes, and restore does not use it. `supportsPITR` stays false, and `Instance.spec.backup.storages[].pitr` is rejected.
- **`Backup.origin.type: External` is not mapped.** A path inside the bucket is not something `SeaweedRestore` can load. `supportsImport` stays false.
- **`BackupStorage.spec.s3.verifyTLS: false` is rejected.** There is nowhere to put it.
- **Time retention is rejected.** The operator only has `keep`.
- **Google Cloud Storage, Azure, Backblaze B2, and a PVC** exist on the operator and do not exist on `BackupStorage`, whose type is `s3` only.
