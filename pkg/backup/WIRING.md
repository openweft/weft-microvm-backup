# Kloset wiring : sequence of calls

## ⚠️ Upstream blocker on Go 1.26 (2026-06)

`kloset@v1.0.13` pulls in `cockroachdb/pebble` for its cache backend, which
transitively imports `cockroachdb/swiss@v0.0.0-20250624` which uses
unexported Go runtime symbols (`fastrand64`, `hashFn`, `getRuntimeHasher`)
that were removed/renamed somewhere between Go 1.23 and 1.26. The build
silently produces a binary, but `caching.Manager.closed.Load()` nil-panics
at runtime because the swiss-backed map didn't initialise.

Workarounds (pick one before continuing the wiring work) :

1. **Pin Go 1.23 toolchain** for this module — `toolchain go1.23.6` in
   `go.mod`, fall back to `GOTOOLCHAIN=go1.23.6` until kloset's deps catch
   up.
2. **Switch to the SQLite cache backend** — `caching/sqlite` exists in
   kloset, doesn't pull pebble/swiss. Needs a thin `Constructor` wrapper
   similar to `pebble.Constructor`. `mattn/go-sqlite3` IS cgo — would need
   `CGO_ENABLED=1` for the binary, which violates the openweft pure-Go
   policy. So this is only viable if a pure-Go sqlite (modernc.org/sqlite)
   replacement is wired.
3. **Wait for kloset / cockroachdb/swiss to fix Go 1.26 compat.** Quickest
   per-user fix once it lands : `go get github.com/PlakarKorp/kloset@latest`.

The `kloset.go` code below ships the structurally-correct wiring per plakar
v1.0.6's reference pattern. It compiles ; it just nil-panics at runtime on
Go 1.26 until one of the workarounds above lands. The unit tests in
`backup_test.go` only exercise the pre-flight guards (which never reach
kloset) so the CI stays green.

---

## Reference sequence (when the blocker lifts)


Reverse-engineered from
[plakar/subcommands/backup/backup.go](https://github.com/PlakarKorp/plakar/blob/v1.0.6/subcommands/backup/backup.go)
to use as the implementation template for `pkg/backup/kloset.go`.

## Imports

```go
import (
    "github.com/PlakarKorp/kloset/kcontext"
    "github.com/PlakarKorp/kloset/objects"
    "github.com/PlakarKorp/kloset/repository"
    "github.com/PlakarKorp/kloset/snapshot"
    "github.com/PlakarKorp/kloset/snapshot/importer"
    "github.com/PlakarKorp/kloset/storage"

    // Source-fs connector — registers an "fs" importer at init().
    _ "github.com/PlakarKorp/integration-fs/importer"
    // Storage backends — register at init().
    _ "github.com/PlakarKorp/integration-fs/storage" // fs://
    // future : add "github.com/PlakarKorp/integration-s3" / sftp once
    // they exist as separate kloset integrations.
)
```

## Create (one snapshot)

```go
func (b *KlosetBackup) Create(ctx context.Context, spec Spec) (Snapshot, error) {
    kctx := kcontext.NewKContext()  // owns the logger + caches
    defer kctx.Close()

    // 1. Storage : map[string]string config — "location" key carries the URL.
    storeCfg := map[string]string{"location": spec.Target}
    store, err := storage.New(kctx, storeCfg)
    if err != nil { return Snapshot{}, err }

    // 2. Repository : open OR create on first use.
    //    Secret derivation : empty when spec.PassphraseEnv is empty ;
    //    otherwise PBKDF2-derive from os.Getenv(spec.PassphraseEnv).
    secret := deriveSecret(spec.PassphraseEnv)
    repo, err := repository.New(kctx, secret, store, nil /* config bytes */)
    if err != nil { return Snapshot{}, err }
    defer repo.Close()

    // 3. Importer : fs:// importer rooted at SourceRoot, filtered by
    //    spec.Paths / Excludes.
    impOpts := map[string]string{
        "location": "fs://" + b.SourceRoot,
    }
    imp, err := importer.NewImporter(kctx, nil /* importer opts */, impOpts)
    if err != nil { return Snapshot{}, err }
    defer imp.Close(kctx)

    // 4. Snapshot builder. PackfileTempStorage = "" means in-memory ;
    //    pass a dir for large snapshots.
    snap, err := snapshot.Create(repo, repository.DefaultType, "", objects.NilMac)
    if err != nil { return Snapshot{}, err }
    defer snap.Close()

    backupOpts := &snapshot.BackupOptions{
        Excludes: spec.Excludes,  // gitignore globs
        // … snapshot header fields, hooks, etc.
    }
    if err := snap.Backup(imp, backupOpts); err != nil {
        return Snapshot{}, err
    }
    // 5. Resolve descriptor — kloset stores everything on snap.Header.
    return Snapshot{
        ID:         snap.Header.GetIndexID().String(),
        CreatedAt:  snap.Header.Timestamp,
        SizeBytes:  int64(snap.Header.GetSize()),
        TotalBytes: int64(snap.Header.GetTotalSize()),
        Paths:      spec.Paths,
        Labels:     spec.Labels,
    }, nil
}
```

## List (every snapshot in a repository)

```go
func (b *KlosetBackup) List(ctx context.Context, spec Spec) ([]Snapshot, error) {
    // Identical kctx/store/repo opening as Create.
    // …
    var out []Snapshot
    for snapID, err := range repo.GetSnapshots() {
        if err != nil { continue }
        hdr, _, err := snapshot.GetSnapshot(repo, snapID)
        if err != nil { continue }
        out = append(out, Snapshot{
            ID:         snapID.String(),
            CreatedAt:  hdr.Timestamp,
            SizeBytes:  int64(hdr.GetSize()),
            TotalBytes: int64(hdr.GetTotalSize()),
        })
    }
    return out, nil
}
```

## Restore

`snapshot.Load(repo, id)` + walk the snapshot's filesystem tree, materialising
each file under destRoot. Uses `snapshot.NewReader(snap, path)` to stream
contents. Mirror `subcommands/restore/restore.go` in plakar.

## Delete + Prune

`repo.DeleteSnapshot(snapID)` — kloset handles dedup-GC internally on a
later `repo.RebuildState()` pass.

## Encryption

kloset's repository.New takes a `secret []byte`. The standard derivation
is PBKDF2(passphrase, repository_salt) — repository.NewSecret() or
similar helpers. Operators provide the passphrase via env (avoids
process-listing leaks).

## Why this isn't wired in the first commit

Each call site above has 2-3 choices to make (importer options, packfile
temp dir, secret derivation parameters) and pinning the wrong default
locks in a wire-format choice that's painful to migrate later. The
skeleton ships the operator surface (CLI shape, Spec, Snapshot) and
defers the implementation pass so the team can review the kloset defaults
together before stamping them. The stubs return a clear "kloset wiring
pending" error so accidental invocation fails loudly rather than
silently no-op'ing.
