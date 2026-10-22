package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestScopeBoundaries(t *testing.T) {
	scope := Scope{ReadPaths: []string{"."}, WriteDirs: []string{"frontend/src/"}, PatchOnlyPaths: []string{"package.json"}}
	if err := scope.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path         string
		write, patch bool
	}{
		{"frontend/src/login.ts", true, false}, {"frontend/src2/login.ts", false, false}, {"backend/api.go", false, false}, {"package.json", false, true}, {"../outside", false, false}, {"/absolute", false, false}, {"frontend/src/../../escape", false, false}, {"frontend/src/.git/config", false, false}, {"frontend\\src\\escape", false, false},
	} {
		t.Run(test.path, func(t *testing.T) {
			if scope.CanWrite(test.path) != test.write || scope.CanPatch(test.path) != test.patch {
				t.Fatal("incorrect scope permission")
			}
		})
	}
	for _, other := range []Scope{{WriteDirs: []string{"frontend/"}}, {WriteDirs: []string{"FRONTEND/src/"}}, {PatchOnlyPaths: []string{"package.json"}}, {PatchOnlyPaths: []string{"frontend/src/login.ts"}}} {
		if !Conflicts(scope, other) {
			t.Fatal("overlapping owner not detected")
		}
	}
	if Conflicts(scope, Scope{WriteDirs: []string{"backend/"}}) {
		t.Fatal("unrelated paths conflict")
	}
}

func TestInvalidScopes(t *testing.T) {
	tests := []Scope{
		{}, {ReadPaths: []string{"."}, WriteDirs: []string{"./"}}, {ReadPaths: []string{"."}, WriteDirs: []string{"src"}}, {ReadPaths: []string{"."}, WriteDirs: []string{"src/", "src/child/"}}, {ReadPaths: []string{"frontend/"}, WriteDirs: []string{"backend/"}}, {ReadPaths: []string{"."}, WriteDirs: []string{"src/"}, PatchOnlyPaths: []string{"src/file"}}, {ReadPaths: []string{"."}, PatchOnlyPaths: []string{"../secret"}},
	}
	for _, scope := range tests {
		if scope.Validate() == nil {
			t.Fatalf("invalid scope accepted: %+v", scope)
		}
	}
}

func TestManifestDetectsIgnoredFilesAndModeChanges(t *testing.T) {
	dir := t.TempDir()
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "hidden.txt\n")
	write("base.txt", "original")
	before, err := Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	write("hidden.txt", "untracked and ignored")
	if err := os.Chmod(filepath.Join(dir, "base.txt"), 0755); err != nil {
		t.Fatal(err)
	}
	after, err := Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	changes := Diff(before, after)
	if len(changes) != 2 {
		t.Fatalf("missing changes: %+v", changes)
	}
	scope := Scope{ReadPaths: []string{"."}, PatchOnlyPaths: []string{"base.txt"}}
	if ValidateChanges(scope, before, after) == nil {
		t.Fatal("ignored out-of-scope addition accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, dir); err == nil {
		t.Fatal("cancelled scan succeeded")
	}
}

func TestPatchBaselineAndSymlinkProtection(t *testing.T) {
	dir := t.TempDir()
	scope := Scope{ReadPaths: []string{"."}, PatchOnlyPaths: []string{"package.json"}}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("old"))
	digest := hex.EncodeToString(hash[:])
	if err := Patch(dir, scope, "package.json", digest, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := Patch(dir, scope, "package.json", digest, []byte("stale")); err == nil {
		t.Fatal("stale patch accepted")
	}
	if err := Patch(dir, scope, "outside.json", "absent", []byte("bad")); err == nil {
		t.Fatal("unauthorized patch accepted")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	linked := Scope{ReadPaths: []string{"."}, PatchOnlyPaths: []string{"linked/file"}}
	if err := Patch(dir, linked, "linked/file", "absent", []byte("escape")); err == nil {
		t.Fatal("symlink parent accepted")
	}
	if err := os.Symlink("package.json", filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	alias := Scope{ReadPaths: []string{"."}, PatchOnlyPaths: []string{"alias"}}
	if err := Patch(dir, alias, "alias", digest, []byte("escape")); err == nil {
		t.Fatal("symlink target accepted")
	}
	after, err := Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if ValidateChanges(Scope{ReadPaths: []string{"."}, WriteDirs: []string{"linked/"}}, Manifest{}, after) == nil {
		t.Fatal("new symlinks accepted")
	}
}

func TestCompleteSourceLifecycleAndExactFileCreation(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "src", "old.txt")
	if err := os.WriteFile(old, []byte("source"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(old, filepath.Join(dir, "src", "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "src", "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	after, err := Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{ReadPaths: []string{"."}, WriteDirs: []string{"src/"}, PatchOnlyPaths: []string{"config.json"}}
	if err := ValidateChanges(scope, before, after); err != nil {
		t.Fatal(err)
	}
	if len(Diff(before, after)) != 3 {
		t.Fatal("rename/deletion/new directory were not all recorded")
	}
	if err := Patch(dir, scope, "config.json", "absent", []byte(`{"enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := Patch(dir, scope, "config.json", "absent", []byte(`{}`)); err == nil {
		t.Fatal("create replaced an existing file without its baseline")
	}
	withConfig, err := Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateChanges(scope, after, withConfig); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("renamed.txt", filepath.Join(dir, "src", "alias")); err != nil {
		t.Fatal(err)
	}
	withLink, err := Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if ValidateChanges(scope, withConfig, withLink) == nil {
		t.Fatal("unapproved source link was accepted")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if len(entry.Name()) >= 11 && entry.Name()[:11] == ".awb-patch-" {
			t.Fatal("temporary patch file left behind")
		}
	}
}

func TestManifestDoesNotFollowExternalSymlink(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("should not be read"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	manifest, err := Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest) != 1 || manifest["outside"].Kind != "symlink" || manifest["outside"].Digest != "" {
		t.Fatal("external tree was followed")
	}
}
