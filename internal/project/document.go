package project

import (
	"agent-workbench/internal/store"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/ledongthuc/pdf"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ReadDocument preserves source bytes and distinguishes extracted text from unreadable input.
func ReadDocument(ctx context.Context, source, managedRoot string) (result store.Document, returnErr error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() {
		return result, errors.New("请选择普通资料文件，不能使用符号链接")
	}
	if info.Size() > 8<<20 {
		return result, errors.New("资料文件超过 8 MiB 限制")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		return result, errors.New("无法读取资料文件")
	}
	if len(raw) > 8<<20 {
		return result, errors.New("资料文件过大")
	}
	hash := sha256.Sum256(raw)
	result = store.Document{Name: filepath.Base(source), Hash: "sha256:" + hex.EncodeToString(hash[:]), Status: "needs_parser"}
	switch strings.ToLower(filepath.Ext(source)) {
	case ".md", ".txt", ".markdown":
		if utf8.Valid(raw) && !bytes.ContainsRune(raw, 0) {
			result.Text, result.Status = string(raw), "ready"
		}
	case ".pdf":
		text, err := pdfText(ctx, raw)
		if err == nil && strings.TrimSpace(text) != "" {
			result.Text, result.Status = text, "ready"
		}
	}
	if len(result.Text) > 2<<20 {
		return store.Document{}, errors.New("提取的资料文本超过限制")
	}
	if err = ctx.Err(); err != nil {
		return store.Document{}, err
	}
	if err = os.MkdirAll(managedRoot, 0700); err != nil {
		return store.Document{}, err
	}
	copyPath := filepath.Join(managedRoot, hex.EncodeToString(hash[:])+".source")
	if _, err = os.Stat(copyPath); errors.Is(err, os.ErrNotExist) {
		if err = os.WriteFile(copyPath, raw, 0600); err != nil {
			return store.Document{}, err
		}
	} else if err != nil {
		return store.Document{}, err
	}
	result.Source, result.CopyPath = source, copyPath
	return result, nil
}

func pdfText(ctx context.Context, raw []byte) (text string, returnErr error) {
	defer func() {
		if recover() != nil {
			text = ""
			returnErr = errors.New("PDF 文本解析失败")
		}
	}()
	reader, err := pdf.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return "", err
	}
	if reader.NumPage() > 128 {
		return "", errors.New("PDF 页数超过限制")
	}
	var result strings.Builder
	for i := 1; i <= reader.NumPage(); i++ {
		if err = ctx.Err(); err != nil {
			return "", err
		}
		page, err := reader.Page(i).GetPlainText(nil)
		if err != nil {
			return "", err
		}
		result.WriteString(page)
		result.WriteString("\n")
		if result.Len() > 2<<20 {
			return "", errors.New("PDF 提取文本过大")
		}
	}
	return result.String(), nil
}
