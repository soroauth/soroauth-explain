package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestCheckCleanRefusesDirtyTree: untracked, modified and staged files each
// make the generator refuse; a clean tree does not.
func TestCheckCleanRefusesDirtyTree(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkClean(dir); err == nil || !strings.Contains(err.Error(), "?? a.txt") {
		t.Fatalf("untracked file: err = %v", err)
	}
	git(t, dir, "add", "a.txt")
	if err := checkClean(dir); err == nil {
		t.Fatal("staged file: accepted")
	}
	git(t, dir, "commit", "-q", "-m", "c")
	if err := checkClean(dir); err != nil {
		t.Fatalf("clean tree refused: %v", err)
	}
	if err := os.WriteFile(f, []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkClean(dir); err == nil {
		t.Fatal("modified file: accepted")
	}
}

// TestBuiltEntriesDeterministic: the built inputs are the same on every run,
// which the drift job depends on.
func TestBuiltEntriesDeterministic(t *testing.T) {
	a, err := builtEntries()
	if err != nil {
		t.Fatal(err)
	}
	b, err := builtEntries()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("builtEntries differs between runs")
	}
	if len(a) != 7 {
		t.Fatalf("%d built entries, want 7", len(a))
	}
}

// TestCopiedEntriesFromPinnedVersion: every copied input names the
// soroauth-go version go.mod pins.
func TestCopiedEntriesFromPinnedVersion(t *testing.T) {
	entries, err := copiedEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 21 {
		t.Fatalf("%d copied entries, want 21 (nine vectors at v0.1.0)", len(entries))
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Source, "github.com/soroauth/soroauth-go@v0.1.0 testdata/vectors/") {
			t.Errorf("%s: source %q", e.Name, e.Source)
		}
	}
}
