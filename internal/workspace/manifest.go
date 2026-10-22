package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry records content, type and mode without following symlinks.
type Entry struct {
	Kind   string `json:"kind"`
	Digest string `json:"digest,omitempty"`
	Mode   uint32 `json:"mode"`
	Target string `json:"target,omitempty"`
}

// Manifest is a complete bounded snapshot. No .gitignore rules are consulted.
type Manifest map[string]Entry

// Scan reads a frozen, task-owned tree. Callers must stop all writers first.
func Scan(ctx context.Context, directory string) (result Manifest, returnErr error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open snapshot: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	manifest := Manifest{}
	err = filepath.WalkDir(directory, func(name string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == directory {
			return nil
		}
		rel, err := filepath.Rel(directory, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !ValidPath(rel) {
			return errors.New("snapshot contains forbidden path")
		}
		if len(manifest) >= 50000 {
			return errors.New("snapshot exceeds file limit")
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		entry := Entry{Mode: uint32(info.Mode().Perm())}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			entry.Kind = "symlink"
			target, err := root.Readlink(rel)
			if err != nil {
				return err
			}
			entry.Target = target
		case info.IsDir():
			entry.Kind = "directory"
		case info.Mode().IsRegular():
			if info.Size() > 32<<20 {
				return errors.New("snapshot file exceeds size limit")
			}
			entry.Kind = "file"
			file, err := root.Open(rel)
			if err != nil {
				return err
			}
			hash := sha256.New()
			_, copyErr := io.Copy(hash, io.LimitReader(file, (32<<20)+1))
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			entry.Digest = hex.EncodeToString(hash.Sum(nil))
		default:
			return errors.New("snapshot contains unsupported special file")
		}
		manifest[rel] = entry
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan frozen snapshot: %w", err)
	}
	return manifest, nil
}

// Change includes additions, deletions, mode changes and symlink changes.
type Change struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// Diff returns a stable full-source change list.
func Diff(before, after Manifest) []Change {
	var changes []Change
	for name, old := range before {
		next, ok := after[name]
		if !ok {
			changes = append(changes, Change{name, "deleted"})
		} else if old != next {
			changes = append(changes, Change{name, "modified"})
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			changes = append(changes, Change{name, "added"})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

// ValidateChanges rejects unauthorized changes and symlinks escaping the source tree.
func ValidateChanges(scope Scope, before, after Manifest) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	for _, change := range Diff(before, after) {
		entry, exists := after[change.Path]
		allowed := scope.CanWrite(change.Path) || scope.CanPatch(change.Path)
		if exists && entry.Kind == "directory" {
			for _, dir := range scope.WriteDirs {
				if change.Path == strings.TrimSuffix(dir, "/") {
					allowed = true
				}
			}
		}
		if !allowed {
			return fmt.Errorf("source change outside task scope: %s", change.Path)
		}
		if exists && entry.Kind == "symlink" {
			return fmt.Errorf("new or changed source symlink requires explicit supported policy: %s", change.Path)
		}
	}
	return nil
}
