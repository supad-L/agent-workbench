// Package workspace defines task ownership and immutable source manifests.
package workspace

import (
	"errors"
	"io/fs"
	"path"
	"strings"
	"unicode"
)

// Scope separates command-writable directories from broker-only file patches.
type Scope struct {
	ReadPaths      []string `json:"read_paths"`
	WriteDirs      []string `json:"write_dirs"`
	PatchOnlyPaths []string `json:"patch_only_paths"`
}

// ValidPath accepts canonical relative POSIX paths and protects control metadata.
func ValidPath(name string) bool {
	if !fs.ValidPath(name) || name == "." || path.Clean(name) != name || strings.ContainsAny(name, "\\:\x00") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if strings.EqualFold(part, ".git") || strings.EqualFold(part, ".agent-workbench") {
			return false
		}
	}
	return true
}

func cleanDirectory(name string) (string, bool) {
	if !strings.HasSuffix(name, "/") {
		return "", false
	}
	clean := strings.TrimSuffix(name, "/")
	return clean, ValidPath(clean)
}

// Validate rejects ambiguous, overly broad or internally conflicting permissions.
func (s Scope) Validate() error {
	if len(s.ReadPaths) == 0 {
		return errors.New("explicit read scope required")
	}
	for _, p := range s.ReadPaths {
		if p != "." && !ValidPath(strings.TrimSuffix(p, "/")) {
			return errors.New("invalid read path")
		}
	}
	seen := map[string]bool{}
	for _, p := range s.WriteDirs {
		dir, ok := cleanDirectory(p)
		if !ok || seen[strings.ToLower(dir)] {
			return errors.New("invalid or duplicate write directory")
		}
		seen[strings.ToLower(dir)] = true
		if !s.canRead(dir, true) {
			return errors.New("write directory is not readable")
		}
	}
	for _, p := range s.PatchOnlyPaths {
		if !ValidPath(p) || seen[strings.ToLower(p)] {
			return errors.New("invalid or duplicate patch path")
		}
		seen[strings.ToLower(p)] = true
		if !s.canRead(p, false) {
			return errors.New("patch path is not readable")
		}
		if s.CanWrite(p) {
			return errors.New("patch-only path overlaps command-writable directory")
		}
	}
	for i, p := range s.WriteDirs {
		for _, q := range s.WriteDirs[i+1:] {
			if directoriesOverlap(p, q) {
				return errors.New("nested write directories must be reduced to one owner")
			}
		}
	}
	return nil
}

func (s Scope) canRead(name string, directory bool) bool {
	for _, p := range s.ReadPaths {
		if p == "." {
			return true
		}
		if strings.HasSuffix(p, "/") {
			base := strings.TrimSuffix(p, "/")
			if name == base || strings.HasPrefix(name, base+"/") {
				return true
			}
		} else if !directory && name == p {
			return true
		}
	}
	return false
}

// CanWrite checks the effective directory boundary for an ordinary source write.
func (s Scope) CanWrite(name string) bool {
	if !ValidPath(name) {
		return false
	}
	for _, p := range s.WriteDirs {
		dir, ok := cleanDirectory(p)
		if ok && strings.HasPrefix(name, dir+"/") {
			return true
		}
	}
	return false
}

// CanPatch checks exact broker-only paths. Renames require both paths to be allowed.
func (s Scope) CanPatch(name string) bool {
	if !ValidPath(name) {
		return false
	}
	for _, p := range s.PatchOnlyPaths {
		if p == name {
			return true
		}
	}
	return false
}

// Conflicts detects shared write ownership, including conservative case aliases.
func Conflicts(a, b Scope) bool {
	for _, p := range a.WriteDirs {
		for _, q := range b.WriteDirs {
			if directoriesOverlap(p, q) {
				return true
			}
		}
		for _, q := range b.PatchOnlyPaths {
			if below(q, p) {
				return true
			}
		}
	}
	for _, p := range a.PatchOnlyPaths {
		for _, q := range b.PatchOnlyPaths {
			if strings.EqualFold(p, q) {
				return true
			}
		}
		for _, q := range b.WriteDirs {
			if below(p, q) {
				return true
			}
		}
	}
	return false
}

func below(file, dir string) bool {
	return strings.HasPrefix(strings.ToLower(file), strings.ToLower(dir)) || strings.EqualFold(file, strings.TrimSuffix(dir, "/"))
}
func directoriesOverlap(a, b string) bool {
	return strings.HasPrefix(strings.ToLower(a), strings.ToLower(b)) || strings.HasPrefix(strings.ToLower(b), strings.ToLower(a))
}
