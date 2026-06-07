# weft-microvm-backup

In-VM backup agent for weft microVMs, built on top of [kloset](https://github.com/PlakarKorp/kloset)
(plakar's storage engine). Designed to ship guest data (files, configs,
databases dumps) to remote backupstores with dedup, encryption, and
incremental snapshots — without the operator having to mount the VM disk
externally.

## Where it fits

  weft-block       : block-volume snapshots + image-level backup (raw bytes,
                     S3/SFTP via own BackupTarget abstraction). See
                     weft-block/pkg/backuptarget.
  weft-microvm-backup : file/dir-level guest-data backup, dedup-aware, runs
                        inside the microVM via weft-microvm-agent. THIS REPO.

Two layers, two tools — block backups for full-volume disaster recovery,
file backups for granular restore + cross-volume dedup. Both can co-exist.

## Storage backends (via kloset)

  - oci://     : RECOMMENDED — any distribution-spec OCI registry
                 (ghcr.io, harbor, distribution/registry, zot, ECR, AR,
                 ACR). Same registry the operator already runs for weft
                 driver/kernel artifacts ; one credential surface, one
                 auth flow, cosign-signable. Content-addressed by design.
  - fs://      : host filesystem (dev / tests)
  - s3://      : versitygw (apache-2.0) or CubeFS objectnode (apache-2.0,
                 CNCF graduated) — both fully open source.
                 AWS S3 itself works too. MinIO deliberately excluded
                 — AGPLv3+commercial fails the openweft policy.
  - sftp://    : sftpgo (server-side) or OpenSSH sshd

Same target schemes as weft-block (intentional — operators configure ONE
backupstore endpoint, both block and file backups point at it).

## CLI

  weft-microvm-backup create --target=oci://ghcr.io/myorg/backups:vm-prod-etc --paths=/etc,/var/log
  weft-microvm-backup list   --target=oci://ghcr.io/myorg/backups
  weft-microvm-backup restore <snapshot-id> --target=oci://ghcr.io/myorg/backups:vm-prod-etc --to=/restore-root
  weft-microvm-backup prune  --target=oci://ghcr.io/myorg/backups --keep-last=10

## Integration with weft-microvm-agent

The agent watches a NATS subject (per-VM) for backup requests + invokes
this binary in the guest VM. See cmd/weft-microvm-backup/agent.go for
the IPC contract.

## Why kloset and not plakar's full CLI

kloset is the *library* underneath plakar — it gives us the storage
engine (chunking, dedup, encryption, snapshot index) without dragging in
plakar's interactive shell, web UI, or daemon. Pure-Go, embeds cleanly.

## License

BSD 3-Clause (matches openweft policy).

