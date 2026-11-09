package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Task copy limits keep a single task bounded on the user's disk.
const (
	maxCopyFiles     = 50000
	maxCopyFileBytes = 32 << 20
	maxCopyBytes     = 512 << 20
)

func skippedControlDirectory(name string) bool {
	return strings.EqualFold(name, ".git") || strings.EqualFold(name, ".agent-workbench")
}

// CopyTree copies a frozen project snapshot into a new private directory.
// Version-control and platform metadata are skipped, special files are refused,
// and symlinks are recreated verbatim (all later access goes through os.Root,
// which refuses to follow links that leave the copy).
func CopyTree(ctx context.Context, source, target string) (returnErr error) {
	if info, err := os.Stat(source); err != nil || !info.IsDir() {
		return errors.New("project snapshot directory unavailable")
	}
	if _, err := os.Lstat(target); err == nil {
		return errors.New("task copy target already exists")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		return err
	}
	files, total := 0, int64(0)
	return filepath.WalkDir(source, func(name string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == source {
			return nil
		}
		rel, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		if item.IsDir() && skippedControlDirectory(item.Name()) {
			return filepath.SkipDir
		}
		if skippedControlDirectory(item.Name()) {
			return nil
		}
		if !ValidPath(filepath.ToSlash(rel)) {
			return fmt.Errorf("snapshot contains unsupported path: %s", filepath.ToSlash(rel))
		}
		files++
		if files > maxCopyFiles {
			return errors.New("project copy exceeds file limit")
		}
		destination := filepath.Join(target, rel)
		info, err := item.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(name)
			if err != nil {
				return err
			}
			return os.Symlink(link, destination)
		case info.IsDir():
			return os.Mkdir(destination, 0o755)
		case info.Mode().IsRegular():
			if info.Size() > maxCopyFileBytes {
				return fmt.Errorf("project file exceeds copy size limit: %s", filepath.ToSlash(rel))
			}
			total += info.Size()
			if total > maxCopyBytes {
				return errors.New("project copy exceeds total size limit")
			}
			return copyFile(name, destination, info.Mode().Perm())
		default:
			return fmt.Errorf("project contains unsupported special file: %s", filepath.ToSlash(rel))
		}
	})
}

func copyFile(source, destination string, mode fs.FileMode) (returnErr error) {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, in.Close()) }()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode|0o600)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, io.LimitReader(in, maxCopyFileBytes+1)); err != nil {
		return errors.Join(err, out.Close())
	}
	return out.Close()
}

// TaskFiles is the broker's only view of a task working copy. Every access
// goes through os.Root, and writes additionally require the task scope.
type TaskFiles struct {
	Dir   string
	Scope Scope
}

// FileInfo is a listing row returned to the model.
type FileInfo struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Size int64  `json:"size,omitempty"`
}

// List returns entries below dir ("" or "." for the root), skipping control
// metadata. Recursive listings stop at limit entries and report truncation.
func (t TaskFiles) List(dir string, recursive bool, limit int) (result []FileInfo, truncated bool, returnErr error) {
	dir = strings.Trim(strings.TrimSpace(dir), "/")
	if dir == "." {
		dir = ""
	}
	if dir != "" && !ValidPath(dir) {
		return nil, false, errors.New("目录路径无效")
	}
	root, err := os.OpenRoot(t.Dir)
	if err != nil {
		return nil, false, err
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	start := "."
	if dir != "" {
		start = dir
		info, err := root.Lstat(dir)
		if err != nil {
			return nil, false, errors.New("目录不存在")
		}
		if !info.IsDir() {
			return nil, false, errors.New("不是目录")
		}
	}
	result = []FileInfo{}
	err = fs.WalkDir(root.FS(), start, func(name string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == start {
			return nil
		}
		if skippedControlDirectory(item.Name()) {
			if item.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if len(result) >= limit {
			truncated = true
			return fs.SkipAll
		}
		row := FileInfo{Path: name, Kind: "file"}
		switch {
		case item.Type()&fs.ModeSymlink != 0:
			row.Kind = "symlink"
		case item.IsDir():
			row.Kind = "directory"
		default:
			if info, err := item.Info(); err == nil {
				row.Size = info.Size()
			}
		}
		result = append(result, row)
		if item.IsDir() && !recursive {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, truncated, nil
}

// Read returns up to maxBytes of one regular file inside the copy.
func (t TaskFiles) Read(name string, maxBytes int) (content []byte, size int64, returnErr error) {
	if !ValidPath(name) {
		return nil, 0, errors.New("文件路径无效")
	}
	root, err := os.OpenRoot(t.Dir)
	if err != nil {
		return nil, 0, err
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	info, err := root.Lstat(name)
	if err != nil {
		return nil, 0, errors.New("文件不存在")
	}
	if !info.Mode().IsRegular() {
		return nil, 0, errors.New("只能读取普通文件")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, 0, errors.New("文件无法读取")
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	content, err = io.ReadAll(io.LimitReader(file, int64(maxBytes)))
	return content, info.Size(), err
}

// Write replaces or creates one regular file inside an authorized write directory.
func (t TaskFiles) Write(name string, content []byte) (returnErr error) {
	if !t.Scope.CanWrite(name) {
		return errors.New("路径不在任务允许修改的目录内")
	}
	if len(content) > 1<<20 {
		return errors.New("单个文件内容超过 1 MiB 上限")
	}
	root, err := os.OpenRoot(t.Dir)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	if err := ensureParents(root, name); err != nil {
		return err
	}
	mode := fs.FileMode(0o644)
	if info, err := root.Lstat(name); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("目标不是普通文件")
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		return errors.Join(err, file.Close())
	}
	return file.Close()
}

// Replace substitutes exactly one occurrence of old with new in a writable file.
func (t TaskFiles) Replace(name, old, replacement string) error {
	if !t.Scope.CanWrite(name) {
		return errors.New("路径不在任务允许修改的目录内")
	}
	if old == "" {
		return errors.New("待替换的原文不能为空")
	}
	content, size, err := t.Read(name, 1<<20+1)
	if err != nil {
		return err
	}
	if size > 1<<20 {
		return errors.New("文件超过 1 MiB，无法替换")
	}
	switch count := strings.Count(string(content), old); count {
	case 0:
		return errors.New("文件中找不到待替换的原文；请先读取文件确认当前内容")
	case 1:
		return t.Write(name, []byte(strings.Replace(string(content), old, replacement, 1)))
	default:
		return fmt.Errorf("待替换的原文出现 %d 次，请提供更长且唯一的片段", count)
	}
}

func ensureParents(root *os.Root, name string) error {
	parts := strings.Split(name, "/")
	for i := 1; i < len(parts); i++ {
		dir := strings.Join(parts[:i], "/")
		info, err := root.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			if err := root.Mkdir(dir, 0o755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("父路径必须是真实目录")
		}
	}
	return nil
}

// TreeChanges compares a task's frozen baseline with its working copy.
func TreeChanges(ctx context.Context, base, work string) ([]Change, error) {
	before, err := Scan(ctx, base)
	if err != nil {
		return nil, err
	}
	after, err := Scan(ctx, work)
	if err != nil {
		return nil, err
	}
	changes := []Change{}
	for _, change := range Diff(before, after) {
		// Directory creation alone is not a reviewable source change.
		if (before[change.Path].Kind == "directory" || after[change.Path].Kind == "directory") && before[change.Path].Kind != "file" && after[change.Path].Kind != "file" {
			continue
		}
		changes = append(changes, change)
	}
	return changes, nil
}
