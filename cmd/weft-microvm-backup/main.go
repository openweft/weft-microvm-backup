// Command weft-microvm-backup is the in-VM CLI for file/dir-level backup of
// guest data. Runs alongside (or invoked by) weft-microvm-agent ; talks to
// a remote backupstore configured per-target URL.
//
// Subcommands :
//
//	create  : take a fresh snapshot
//	list    : enumerate snapshots in a repository
//	restore : reconstruct a snapshot under a destination root
//	delete  : drop one snapshot
//	prune   : keep N newest snapshots, drop the rest
//
// Common flags :
//
//	--target  (required) : backupstore URL (fs:// / s3:// / sftp://)
//	--passphrase-env     : env var holding the encryption passphrase
//	                       (empty = no encryption ; not recommended)
//	--source-root        : in-VM filesystem root the source walker is
//	                       based at (default "/")
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/openweft/weft-microvm-backup/pkg/backup"
)

func main() {
	root := newRootCmd()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "weft-microvm-backup:", err)
		os.Exit(1)
	}
}

type commonFlags struct {
	target        string
	passphraseEnv string
	sourceRoot    string
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "weft-microvm-backup",
		Short: "In-VM file/dir backup agent for weft microVMs (kloset-backed)",
		Long: "weft-microvm-backup ships guest data (files / dirs / config / DB " +
			"dumps) to a remote backupstore with dedup, encryption, and " +
			"incremental snapshots. Backups are kloset-format (decompressible / " +
			"restorable with the plakar CLI offline).",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newCreateCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newRestoreCmd())
	cmd.AddCommand(newDeleteCmd())
	cmd.AddCommand(newPruneCmd())
	return cmd
}

func addCommonFlags(cmd *cobra.Command, f *commonFlags) {
	cmd.Flags().StringVar(&f.target, "target", "",
		`Backupstore URL. Recommended : "oci://registry/repo:tag" (any distribution-spec OCI registry — ghcr.io, harbor, distribution/registry, zot, ECR, AR, ACR ; same auth surface as weft driver pulls). Also supported : "fs://path" (local dev), "s3://bucket@region/prefix" (versitygw / CubeFS objectnode / AWS S3), "sftp://user@host:port/path" (sftpgo / OpenSSH).`)
	cmd.Flags().StringVar(&f.passphraseEnv, "passphrase-env", "WEFT_BACKUP_PASSPHRASE",
		`Env var holding the encryption passphrase. Empty disables encryption (NOT recommended for prod).`)
	cmd.Flags().StringVar(&f.sourceRoot, "source-root", "/",
		`In-VM filesystem root the source walker is anchored at. Defaults to / (whole guest).`)
	_ = cmd.MarkFlagRequired("target")
}

func backupFor(f *commonFlags) backup.Backup {
	return backup.New(f.sourceRoot)
}

func newCreateCmd() *cobra.Command {
	var (
		c      commonFlags
		paths  []string
		excl   []string
		labels []string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Take a fresh snapshot of the guest's files",
		RunE: func(cmd *cobra.Command, _ []string) error {
			spec := backup.Spec{
				Target:        c.target,
				Paths:         paths,
				Excludes:      excl,
				Labels:        parseLabels(labels),
				PassphraseEnv: c.passphraseEnv,
			}
			snap, err := backupFor(&c).Create(cmd.Context(), spec)
			if err != nil {
				return err
			}
			fmt.Printf("created snapshot id=%s size=%d total=%d\n", snap.ID, snap.SizeBytes, snap.TotalBytes)
			return nil
		},
	}
	addCommonFlags(cmd, &c)
	cmd.Flags().StringSliceVar(&paths, "paths", nil, `Comma-separated absolute paths INSIDE source-root to include. Empty = whole source-root.`)
	cmd.Flags().StringSliceVar(&excl, "exclude", nil, `Comma-separated gitignore-style globs to exclude.`)
	cmd.Flags().StringSliceVar(&labels, "label", nil, `Repeatable key=value labels stamped on the snapshot metadata.`)
	return cmd
}

func newListCmd() *cobra.Command {
	var c commonFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Enumerate snapshots in the target repository",
		RunE: func(cmd *cobra.Command, _ []string) error {
			snaps, err := backupFor(&c).List(cmd.Context(), backup.Spec{
				Target:        c.target,
				PassphraseEnv: c.passphraseEnv,
			})
			if err != nil {
				return err
			}
			if len(snaps) == 0 {
				fmt.Fprintln(os.Stderr, "(no snapshots)")
				return nil
			}
			for _, s := range snaps {
				fmt.Printf("%s\t%s\tsize=%d\ttotal=%d\n",
					s.ID, s.CreatedAt.Format("2006-01-02T15:04:05Z"), s.SizeBytes, s.TotalBytes)
			}
			return nil
		},
	}
	addCommonFlags(cmd, &c)
	return cmd
}

func newRestoreCmd() *cobra.Command {
	var (
		c       commonFlags
		destDir string
	)
	cmd := &cobra.Command{
		Use:   "restore <snapshot-id>",
		Short: "Reconstruct a snapshot under a destination root",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return backupFor(&c).Restore(cmd.Context(), backup.Spec{
				Target:        c.target,
				PassphraseEnv: c.passphraseEnv,
			}, args[0], destDir)
		},
	}
	addCommonFlags(cmd, &c)
	cmd.Flags().StringVar(&destDir, "to", "",
		`Destination filesystem root. The snapshot's tree is materialised under this path. Created if missing.`)
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func newDeleteCmd() *cobra.Command {
	var c commonFlags
	cmd := &cobra.Command{
		Use:   "delete <snapshot-id>",
		Short: "Drop one snapshot from the repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return backupFor(&c).Delete(cmd.Context(), backup.Spec{
				Target:        c.target,
				PassphraseEnv: c.passphraseEnv,
			}, args[0])
		},
	}
	addCommonFlags(cmd, &c)
	return cmd
}

func newPruneCmd() *cobra.Command {
	var (
		c        commonFlags
		keepLast int
	)
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Keep the N newest snapshots, drop the rest",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dropped, err := backupFor(&c).Prune(cmd.Context(), backup.Spec{
				Target:        c.target,
				PassphraseEnv: c.passphraseEnv,
			}, keepLast)
			if err != nil {
				return err
			}
			fmt.Printf("pruned %d snapshot(s) ; kept %d newest\n", dropped, keepLast)
			return nil
		},
	}
	addCommonFlags(cmd, &c)
	cmd.Flags().IntVar(&keepLast, "keep-last", 10, `How many newest snapshots to keep. 0 prunes everything.`)
	return cmd
}

// parseLabels turns a slice of "key=value" args into a map.
func parseLabels(in []string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for _, kv := range in {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		out[k] = v
	}
	return out
}
