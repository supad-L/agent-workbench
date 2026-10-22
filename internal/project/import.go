// Package project prepares managed copies without changing the user's source tree.
package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"agent-workbench/internal/store"
	"agent-workbench/internal/workspace"
)

// Entry records every copied or explicitly excluded import input.
type Entry struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Hash     string `json:"hash,omitempty"`
	Mode     uint32 `json:"mode"`
	Excluded string `json:"excluded,omitempty"`
}

// Preparer owns a private project-data root, separate from imported sources.
type Preparer struct{ Root string }

// Prepare creates a new managed repo or a verified copy including uncommitted files.
func (p Preparer) Prepare(ctx context.Context, name, source string) (result store.Project, returnErr error) {
	if name == "" || len(name) > 160 {
		return result, errors.New("请填写项目名称")
	}
	root, err := filepath.Abs(p.Root)
	if err != nil {
		return result, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return result, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return result, err
	}
	id := store.ID()
	target := filepath.Join(root, id, "source")
	if err = os.MkdirAll(target, 0700); err != nil {
		return result, err
	}
	keep := false
	defer func() {
		if !keep {
			returnErr = errors.Join(returnErr, os.RemoveAll(filepath.Dir(target)))
		}
	}()
	var entries []Entry
	if source == "" {
		if err = exec.CommandContext(ctx, "git", "init", "-q", "--initial-branch=main", target).Run(); err != nil {
			return result, errors.New("无法在管理区初始化新项目仓库")
		}
		entries = []Entry{}
	} else {
		source, err = filepath.Abs(source)
		if err != nil {
			return result, err
		}
		source, err = filepath.EvalSymlinks(source)
		if err != nil {
			return result, errors.New("无法读取源项目目录")
		}
		if source == root || strings.HasPrefix(source, root+string(filepath.Separator)) {
			return result, errors.New("请选择管理区以外的源项目")
		}
		entries, err = p.collect(ctx, source, target, root)
		if err != nil {
			return result, err
		}
		after, err := p.collect(ctx, source, "", root)
		if err != nil {
			return result, err
		}
		a, err := json.Marshal(entries)
		if err != nil {
			return result, err
		}
		b, err := json.Marshal(after)
		if err != nil {
			return result, err
		}
		if string(a) != string(b) {
			return result, errors.New("导入期间源项目发生变化，请保存工作后重试")
		}
	}
	manifest, err := json.Marshal(entries)
	if err != nil {
		return result, err
	}
	keep = true
	return store.Project{ID: id, Name: name, Source: source, Snapshot: target, Manifest: manifest}, nil
}

func excluded(name string, isDir bool) string {
	lower := strings.ToLower(name)
	if isDir {
		switch lower {
		case ".git", "node_modules", ".venv", "venv", "__pycache__", ".cache", "dist", "build", "output", ".next", "credentials", ".ssh", ".aws":
			return "生成目录、仓库元数据或凭证目录"
		}
	}
	if strings.HasPrefix(lower, ".env") && lower != ".env.example" || lower == ".npmrc" || lower == ".netrc" || lower == "auth.json" || lower == "credentials.json" || lower == "id_rsa" || lower == "id_ed25519" || lower == ".ds_store" {
		return "敏感或本机专用配置"
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx"} {
		if strings.HasSuffix(lower, suffix) {
			return "密钥文件"
		}
	}
	return ""
}

func (p Preparer) collect(ctx context.Context, source, target, dataRoot string) (result []Entry, returnErr error) {
	root, err := os.OpenRoot(source)
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	result = []Entry{}
	var total int64
	err = filepath.WalkDir(source, func(path string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("无法扫描部分源文件")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == source {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := item.Info()
		if err != nil {
			return err
		}
		entry := Entry{Path: rel, Mode: uint32(info.Mode().Perm()), Kind: "file"}
		if info.IsDir() {
			entry.Kind = "directory"
		}
		entry.Excluded = excluded(item.Name(), info.IsDir())
		if path == dataRoot {
			entry.Excluded = "工作台管理区"
		}
		if !workspace.ValidPath(rel) && entry.Excluded == "" {
			entry.Excluded = "不支持的路径"
		}
		if info.Mode()&os.ModeSymlink != 0 {
			entry.Kind, entry.Excluded = "symlink", "符号链接不跟随"
		}
		if !info.IsDir() && !info.Mode().IsRegular() && entry.Excluded == "" {
			entry.Excluded = "不支持的特殊文件"
		}
		if len(result) >= 50000 {
			return errors.New("源项目文件数量超过导入限制")
		}
		if entry.Excluded != "" {
			result = append(result, entry)
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			result = append(result, entry)
			if target != "" {
				return os.MkdirAll(filepath.Join(target, filepath.FromSlash(rel)), info.Mode().Perm())
			}
			return nil
		}
		if info.Size() > 32<<20 {
			return errors.New("源项目存在超过 32 MiB 的单个文件")
		}
		file, err := root.Open(rel)
		if err != nil {
			return errors.New("源文件在读取期间发生变化或不可访问")
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, (32<<20)+1))
		closeErr := file.Close()
		if err = errors.Join(readErr, closeErr); err != nil {
			return err
		}
		total += int64(len(raw))
		if len(raw) > 32<<20 || total > 256<<20 {
			return errors.New("源项目内容超过导入大小限制")
		}
		hash := sha256.Sum256(raw)
		entry.Hash = "sha256:" + hex.EncodeToString(hash[:])
		result = append(result, entry)
		if target != "" {
			return os.WriteFile(filepath.Join(target, filepath.FromSlash(rel)), raw, info.Mode().Perm())
		}
		return nil
	})
	return result, err
}
