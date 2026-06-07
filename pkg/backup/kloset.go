package backup

// kloset.go is the concrete Backup implementation backed by
// github.com/PlakarKorp/kloset.
//
// ⚠️ UPSTREAM BLOCKER (Go 1.26, 2026-06)
//
// kloset@v1.0.13 depends on cockroachdb/pebble which transitively imports
// cockroachdb/swiss, which uses unexported Go runtime symbols
// (fastrand64, hashFn, getRuntimeHasher) that were removed/renamed in
// Go 1.24+. Until either kloset switches to a compatible cache backend
// (the sqlite/ one is there but needs cgo) or cockroachdb/swiss catches
// up to current Go, importing kloset packages directly breaks the build
// on Go 1.26.
//
// What ships here today :
//
//   * The operator-facing Backup interface (see backup.go).
//   * A KlosetBackup type with the same method shape, returning a clear
//     "kloset wiring blocked — see WIRING.md" error on every call.
//   * The full reference sequence in WIRING.md, ready to drop in once
//     the upstream blocker lifts.
//
// The CLI still ships (`weft-microvm-backup --help` works, all subcommand
// flags parse correctly). It just surfaces the error to the operator
// rather than silently degrading. Unit tests in backup_test.go exercise
// the pre-flight validation guards (which never need kloset).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
)

// errKlosetBlocked is the sentinel surfaced to callers while the upstream
// kloset / cockroachdb/swiss Go 1.26 incompatibility is unresolved.
// Wrapped in every Backup method return so operators / CI logs get a
// clear, greppable signal.
var errKlosetBlocked = errors.New("kloset wiring blocked on cockroachdb/swiss vs Go 1.26 incompatibility — see pkg/backup/WIRING.md")

// KlosetBackup is the kloset-backed Backup implementation. Field shape is
// preserved across the blocked / unblocked transition so test fixtures
// keep working once the wiring lands.
type KlosetBackup struct {
	// SourceRoot is the filesystem path the source iterator walks from.
	// In-VM use : "/". Tests can point at a temp dir.
	SourceRoot string
}

// New returns a KlosetBackup rooted at sourceRoot. Empty root defaults to
// "/" — the in-VM bring-up's expected value.
func New(sourceRoot string) *KlosetBackup {
	if sourceRoot == "" {
		sourceRoot = "/"
	}
	return &KlosetBackup{SourceRoot: sourceRoot}
}

func (b *KlosetBackup) Create(ctx context.Context, spec Spec) (Snapshot, error) {
	if spec.Target == "" {
		return Snapshot{}, fmt.Errorf("backup.Create: empty target")
	}
	if err := ensurePassphraseAvailable(spec.PassphraseEnv); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{}, fmt.Errorf("backup.Create: %w", errKlosetBlocked)
}

func (b *KlosetBackup) List(ctx context.Context, spec Spec) ([]Snapshot, error) {
	if spec.Target == "" {
		return nil, fmt.Errorf("backup.List: empty target")
	}
	if err := ensurePassphraseAvailable(spec.PassphraseEnv); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("backup.List: %w", errKlosetBlocked)
}

func (b *KlosetBackup) Restore(ctx context.Context, spec Spec, snapshotID, destRoot string) error {
	if spec.Target == "" {
		return fmt.Errorf("backup.Restore: empty target")
	}
	if snapshotID == "" {
		return fmt.Errorf("backup.Restore: empty snapshot id")
	}
	if destRoot == "" {
		return fmt.Errorf("backup.Restore: empty dest root")
	}
	if err := ensurePassphraseAvailable(spec.PassphraseEnv); err != nil {
		return err
	}
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return fmt.Errorf("create dest %s: %w", destRoot, err)
	}
	return fmt.Errorf("backup.Restore: %w", errKlosetBlocked)
}

func (b *KlosetBackup) Delete(ctx context.Context, spec Spec, snapshotID string) error {
	if spec.Target == "" {
		return fmt.Errorf("backup.Delete: empty target")
	}
	if snapshotID == "" {
		return fmt.Errorf("backup.Delete: empty snapshot id")
	}
	if err := ensurePassphraseAvailable(spec.PassphraseEnv); err != nil {
		return err
	}
	return fmt.Errorf("backup.Delete: %w", errKlosetBlocked)
}

// Prune keeps the `keepLast` newest snapshots + drops the rest. Calls
// List + Delete under the hood ; surfaces errKlosetBlocked from List when
// kloset is unavailable.
func (b *KlosetBackup) Prune(ctx context.Context, spec Spec, keepLast int) (int, error) {
	if keepLast < 0 {
		return 0, fmt.Errorf("backup.Prune: keepLast must be >= 0")
	}
	snaps, err := b.List(ctx, spec)
	if err != nil {
		return 0, err
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].CreatedAt.After(snaps[j].CreatedAt) })
	if len(snaps) <= keepLast {
		return 0, nil
	}
	toDrop := snaps[keepLast:]
	for _, s := range toDrop {
		if err := b.Delete(ctx, spec, s.ID); err != nil {
			return 0, fmt.Errorf("prune %s: %w", s.ID, err)
		}
	}
	return len(toDrop), nil
}

// ensurePassphraseAvailable confirms the env var is populated when
// configured. The actual passphrase is read at kloset open time once
// the wiring lifts.
func ensurePassphraseAvailable(envVar string) error {
	if envVar == "" {
		return nil
	}
	if v := os.Getenv(envVar); v == "" {
		return fmt.Errorf("backup: passphrase env %q is not set (or is empty)", envVar)
	}
	return nil
}

// Compile-time proof KlosetBackup satisfies the public interface.
var _ Backup = (*KlosetBackup)(nil)
