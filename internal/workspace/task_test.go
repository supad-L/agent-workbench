package workspace

import (
	"context"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCopyTreeSkipsControlMetadataAndKeepsSource(t *testing.T) {
	source := t.TempDir()
	writeTree(t, source, map[string]string{"src/app.js": "a\n", ".git/HEAD": "ref", "README.md": "r"})
	target := filepath.Join(t.TempDir(), "work")
	if err := CopyTree(context.Background(), source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git must not be copied: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, "src/app.js"))
	if err != nil || string(data) != "a\n" {
		t.Fatalf("copy content %q %v", data, err)
	}
	if err := CopyTree(context.Background(), source, target); err == nil {
		t.Fatal("existing target must be refused")
	}
}

func TestTaskFilesScopeAndSymlinkProtection(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeTree(t, dir, map[string]string{"src/a.txt": "one\ntwo\n", "config.json": "{}"})
	writeTree(t, outside, map[string]string{"secret.txt": "secret"})
	if err := os.Symlink(outside, filepath.Join(dir, "src", "escape")); err != nil {
		t.Fatal(err)
	}
	files := TaskFiles{Dir: dir, Scope: Scope{ReadPaths: []string{"."}, WriteDirs: []string{"src/"}}}
	if err := files.Write("config.json", []byte("x")); err == nil {
		t.Fatal("write outside scope must be refused")
	}
	if err := files.Write("../x", []byte("x")); err == nil {
		t.Fatal("parent traversal must be refused")
	}
	if err := files.Write(".git/config", []byte("x")); err == nil {
		t.Fatal(".git must be refused")
	}
	if err := files.Write("src/escape/pwn.txt", []byte("x")); err == nil {
		t.Fatal("symlink parent must be refused")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwn.txt")); !os.IsNotExist(err) {
		t.Fatal("write escaped through symlink")
	}
	if _, _, err := files.Read("src/escape/secret.txt", 100); err == nil {
		t.Fatal("read through escaping symlink must fail")
	}
	if err := files.Write("src/new/deep/file.go", []byte("package x\n")); err != nil {
		t.Fatal(err)
	}
	if err := files.Replace("src/a.txt", "two", "TWO"); err != nil {
		t.Fatal(err)
	}
	if err := files.Replace("src/a.txt", "missing", "x"); err == nil || !strings.Contains(err.Error(), "找不到") {
		t.Fatalf("missing replace: %v", err)
	}
	content, _, err := files.Read("src/a.txt", 100)
	if err != nil || string(content) != "one\nTWO\n" {
		t.Fatalf("replace result %q %v", content, err)
	}
	list, _, err := files.List("", true, 100)
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, row := range list {
		joined += row.Path + ","
	}
	if !strings.Contains(joined, "src/new/deep/file.go") || strings.Contains(joined, ".git") {
		t.Fatalf("listing %s", joined)
	}
	shallow, _, err := files.List("src", false, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range shallow {
		if strings.Count(row.Path, "/") > 1 {
			t.Fatalf("non-recursive listing descended: %s", row.Path)
		}
	}
}

func TestTreeChangesAndPatchApply(t *testing.T) {
	source := t.TempDir()
	original := map[string]string{"src/a.txt": "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n", "src/del.txt": "gone\n", "keep.md": "k"}
	writeTree(t, source, original)
	root := t.TempDir()
	base, work := filepath.Join(root, "base"), filepath.Join(root, "work")
	for _, dir := range []string{base, work} {
		if err := CopyTree(context.Background(), source, dir); err != nil {
			t.Fatal(err)
		}
	}
	files := TaskFiles{Dir: work, Scope: Scope{ReadPaths: []string{"."}, WriteDirs: []string{"src/"}}}
	if err := files.Write("src/a.txt", []byte("1\n2\nthree\n4\n5\n6\n7\n8\n9\n10\neleven")); err != nil {
		t.Fatal(err)
	}
	if err := files.Write("src/new.txt", []byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(work, "src/del.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(work, "src/emptydir"), 0o755); err != nil {
		t.Fatal(err)
	}
	changes, err := TreeChanges(context.Background(), base, work)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, c := range changes {
		got = append(got, c.Path+":"+c.Kind)
	}
	if strings.Join(got, ",") != "src/a.txt:modified,src/del.txt:deleted,src/new.txt:added" {
		t.Fatalf("changes %v", got)
	}
	diffs, err := UnifiedDiffs(base, work, changes, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if diffs[0].Added != 2 || diffs[0].Removed != 1 {
		t.Fatalf("counts %+v", diffs[0])
	}
	patch := CombinedPatch(diffs)
	if !strings.Contains(patch, "\\ No newline at end of file") || !strings.Contains(patch, "--- /dev/null") {
		t.Fatalf("patch:\n%s", patch)
	}
	applyWithGit(t, source, patch, work)
}

func TestRandomEditsRoundTripThroughGitApply(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 25; round++ {
		lines := []string{}
		for i := 0; i < 40+rng.Intn(60); i++ {
			lines = append(lines, string(rune('a'+rng.Intn(6))))
		}
		next := []string{}
		for _, l := range lines {
			switch rng.Intn(10) {
			case 0:
				continue
			case 1:
				next = append(next, l, "inserted")
			case 2:
				next = append(next, l+"x")
			default:
				next = append(next, l)
			}
		}
		source := t.TempDir()
		writeTree(t, source, map[string]string{"f.txt": strings.Join(lines, "\n") + "\n"})
		root := t.TempDir()
		base, work := filepath.Join(root, "base"), filepath.Join(root, "work")
		_ = CopyTree(context.Background(), source, base)
		_ = CopyTree(context.Background(), source, work)
		writeTree(t, work, map[string]string{"f.txt": strings.Join(next, "\n") + "\n"})
		changes, err := TreeChanges(context.Background(), base, work)
		if err != nil {
			t.Fatal(err)
		}
		diffs, err := UnifiedDiffs(base, work, changes, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		applyWithGit(t, source, CombinedPatch(diffs), work)
	}
}

func TestBinaryAndLargeFilesAreFlagged(t *testing.T) {
	source := t.TempDir()
	writeTree(t, source, map[string]string{"bin.dat": "a\x00b"})
	root := t.TempDir()
	base, work := filepath.Join(root, "base"), filepath.Join(root, "work")
	_ = CopyTree(context.Background(), source, base)
	_ = CopyTree(context.Background(), source, work)
	writeTree(t, work, map[string]string{"bin.dat": "a\x00c"})
	changes, _ := TreeChanges(context.Background(), base, work)
	diffs, err := UnifiedDiffs(base, work, changes, 1<<20)
	if err != nil || len(diffs) != 1 || !diffs[0].Binary || diffs[0].Text != "" {
		t.Fatalf("binary diff %+v %v", diffs, err)
	}
}

func applyWithGit(t *testing.T, source, patch, expected string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable for patch round-trip check")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if err := CopyTree(context.Background(), source, repo); err != nil {
		t.Fatal(err)
	}
	patchFile := filepath.Join(t.TempDir(), "change.patch")
	if err := os.WriteFile(patchFile, []byte(patch), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(git, "-C", repo, "apply", patchFile)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git apply failed: %v\n%s\npatch:\n%s", err, output, patch)
	}
	want, err := Scan(context.Background(), expected)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Scan(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range Diff(want, got) {
		if want[change.Path].Kind == "directory" || got[change.Path].Kind == "directory" {
			continue
		}
		t.Fatalf("applied tree differs at %s (%s)\npatch:\n%s", change.Path, change.Kind, patch)
	}
}
