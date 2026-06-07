// Package backup wraps github.com/PlakarKorp/kloset for the weft microVM
// use case : back up files/dirs from inside a guest to a remote
// backupstore, with dedup + encryption + incremental snapshots.
//
// Layering :
//
//   Caller (CLI / agent NATS handler)
//      ↓
//   backup.Backup{}  (this package — operator-facing API)
//      ↓
//   kloset/repository.Repository  (chunking, dedup, snapshot index)
//      ↓
//   kloset/storage.Backend         (fs / s3 / sftp / …)
//
// The shapes here intentionally mirror weft-block's snapshot+backup API
// (Spec / List / Delete / Restore) so a unified `weft volume backup …` CLI
// can dispatch by backup KIND : block (weft-block) vs file (this).
package backup

import (
	"context"
	"errors"
	"time"
)

// ErrUnsupported is returned by methods on backends that don't implement
// the requested feature (e.g. prune on an append-only target). Matches
// the upstream weft-drivers sentinel so callers can treat both backup
// flavours through a single error-handling path.
var ErrUnsupported = errors.New("backup: unsupported")

// Spec carries what the operator wants backed up + where. Keep it flat /
// primitive so the same struct serialises over NATS (agent dispatch) and
// over gRPC (control-plane RPC) without per-transport tweaks.
type Spec struct {
	// Target is the backupstore URL : fs://, s3://, sftp://.
	Target string
	// Paths is the list of absolute paths inside the source root to
	// include in the snapshot. Empty list = whole source root.
	Paths []string
	// Excludes is a list of gitignore-style glob patterns evaluated
	// against the relative paths inside the snapshot. Skipped from
	// the snapshot.
	Excludes []string
	// Labels are propagated to the kloset snapshot metadata and
	// surfaced in List / Show. Free-form key/value tags.
	Labels map[string]string
	// PassphraseEnv names the env var holding the encryption passphrase
	// (e.g. "WEFT_BACKUP_PASSPHRASE"). Empty = no encryption (NOT
	// recommended for prod). The env-var indirection avoids the
	// passphrase landing in process listings.
	PassphraseEnv string
}

// Snapshot is the descriptor List returns for one stored backup snapshot.
type Snapshot struct {
	// ID is the kloset snapshot identifier (a content-addressed hex
	// string ; opaque to callers, used for Restore / Delete).
	ID string
	// CreatedAt is when the snapshot was taken (server clock at backup
	// time, stored in kloset metadata).
	CreatedAt time.Time
	// SizeBytes is the snapshot's deduplicated on-target size — i.e.
	// the unique bytes this snapshot added vs prior snapshots in the
	// same repository.
	SizeBytes int64
	// TotalBytes is the snapshot's logical size — what you'd see after
	// a full restore. Larger than SizeBytes when there's effective
	// dedup vs older snapshots.
	TotalBytes int64
	// Paths echoes Spec.Paths so operators can grep the listing.
	Paths []string
	// Labels are echoed from Spec.Labels.
	Labels map[string]string
}

// Backup is the operator-facing entry point. One instance per
// (target + passphrase) tuple. Cheap to construct ; the underlying
// kloset repository is opened lazily on first use and reused across
// calls.
type Backup interface {
	// Create takes a fresh snapshot from the given source paths and
	// stores it in the target. Returns the resulting snapshot
	// descriptor when the upload is durable.
	Create(ctx context.Context, spec Spec) (Snapshot, error)

	// List enumerates every snapshot in the target's repository,
	// newest first. The Spec's Target + PassphraseEnv pick the
	// repository ; Paths / Excludes / Labels are ignored on List.
	List(ctx context.Context, spec Spec) ([]Snapshot, error)

	// Restore reconstructs `snapshotID`'s contents into `destRoot`.
	// destRoot is created if missing ; existing files are overwritten.
	Restore(ctx context.Context, spec Spec, snapshotID string, destRoot string) error

	// Delete drops one snapshot from the repository. The dedup
	// garbage collector reclaims any chunks no longer referenced.
	// Idempotent.
	Delete(ctx context.Context, spec Spec, snapshotID string) error

	// Prune drops every snapshot in the repository EXCEPT the most
	// recent `keepLast`. Returns the number actually deleted. Useful
	// for the operator's "keep N days of backups" retention policy.
	Prune(ctx context.Context, spec Spec, keepLast int) (int, error)
}
