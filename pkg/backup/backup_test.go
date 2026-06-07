package backup

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestNew_DefaultsToRoot verifies the in-VM bring-up's default : empty
// source-root → "/" so guest filesystem walks land in the snapshot
// without the operator restating it.
func TestNew_DefaultsToRoot(t *testing.T) {
	b := New("")
	if b.SourceRoot != "/" {
		t.Errorf("New(\"\") source root = %q, want %q", b.SourceRoot, "/")
	}
	if b := New("/var/spool"); b.SourceRoot != "/var/spool" {
		t.Errorf("New(\"/var/spool\") source root = %q, want %q", b.SourceRoot, "/var/spool")
	}
}

// TestCreate_RejectsEmptyTarget asserts the early-fast guard surfaces the
// missing flag rather than hitting kloset with garbage.
func TestCreate_RejectsEmptyTarget(t *testing.T) {
	_, err := New("/").Create(context.Background(), Spec{})
	if err == nil || !strings.Contains(err.Error(), "empty target") {
		t.Errorf("Create with empty target = %v, want 'empty target'", err)
	}
}

// TestCreate_RequiresPassphraseEnv proves the passphrase env-var check
// fires BEFORE we try to open the (potentially expensive) repository.
func TestCreate_RequiresPassphraseEnv(t *testing.T) {
	// Use a sentinel env name guaranteed not to exist in test runner.
	const sentinel = "WEFT_BACKUP_TEST_PASSPHRASE_DOES_NOT_EXIST"
	_ = os.Unsetenv(sentinel)
	_, err := New("/").Create(context.Background(), Spec{
		Target:        "fs:///tmp/whatever",
		PassphraseEnv: sentinel,
	})
	if err == nil || !strings.Contains(err.Error(), "passphrase env") {
		t.Errorf("Create with missing passphrase env = %v, want 'passphrase env … not set'", err)
	}
}

// TestPrune_KeepLastNegativeRejected verifies the input-validation guard.
func TestPrune_KeepLastNegativeRejected(t *testing.T) {
	_, err := New("/").Prune(context.Background(), Spec{Target: "fs:///x"}, -1)
	if err == nil || !strings.Contains(err.Error(), "keepLast must be >= 0") {
		t.Errorf("Prune(-1) = %v, want validation error", err)
	}
}

// TestEnsurePassphraseAvailable_EmptyEnvAcceptsUnencrypted documents the
// "operator opted in to unencrypted" path : empty PassphraseEnv passes.
func TestEnsurePassphraseAvailable_EmptyEnvAcceptsUnencrypted(t *testing.T) {
	if err := ensurePassphraseAvailable(""); err != nil {
		t.Errorf("empty env should accept (operator-opt-in) ; got %v", err)
	}
}

// TestEnsurePassphraseAvailable_SetEnvAccepts confirms the env-var-set
// path passes the pre-check (the actual value is read later by kloset).
func TestEnsurePassphraseAvailable_SetEnvAccepts(t *testing.T) {
	t.Setenv("WEFT_BACKUP_TEST_PASSPHRASE_OK", "non-empty")
	if err := ensurePassphraseAvailable("WEFT_BACKUP_TEST_PASSPHRASE_OK"); err != nil {
		t.Errorf("non-empty env should accept ; got %v", err)
	}
}
