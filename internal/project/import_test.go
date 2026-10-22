package project

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportPreservesUncommittedSourceAndRecordsExclusions(t *testing.T) {
	source := t.TempDir()
	data := t.TempDir()
	for name, text := range map[string]string{"main.txt": "uncommitted source", "new.txt": "untracked", ".env": "SECRET_MUST_NOT_COPY", ".git/HEAD": "ref: refs/heads/main", "nested/code.go": "package demo"} {
		p := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(source, "escape")); err != nil {
		t.Fatal(err)
	}
	result, err := (Preparer{Root: data}).Prepare(context.Background(), "Fixture", source)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(filepath.Join(result.Snapshot, "new.txt"))
	if err != nil || string(copied) != "untracked" {
		t.Fatal("untracked source missing")
	}
	if _, err = os.Stat(filepath.Join(result.Snapshot, ".env")); !os.IsNotExist(err) {
		t.Fatal("secret copied")
	}
	if _, err = os.Lstat(filepath.Join(result.Snapshot, "escape")); !os.IsNotExist(err) {
		t.Fatal("symlink followed")
	}
	if !bytes.Contains(result.Manifest, []byte(".env")) || bytes.Contains(result.Manifest, []byte("SECRET_MUST_NOT_COPY")) {
		t.Fatal("exclusion manifest missing or secret content leaked")
	}
	original, err := os.ReadFile(filepath.Join(source, "main.txt"))
	if err != nil || string(original) != "uncommitted source" {
		t.Fatal("original source changed")
	}
}

func tinyPDF() []byte {
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"}
	text := "BT /F1 12 Tf 10 20 Td (Hello PDF) Tj ET"
	objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(text), text))
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, v := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, v)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, o := range offsets[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return b.Bytes()
}
func TestDocumentTextPDFAndUnsupportedStatuses(t *testing.T) {
	dir := t.TempDir()
	managed := t.TempDir()
	for _, test := range []struct {
		name             string
		raw              []byte
		status, contains string
	}{{"note.md", []byte("# Requirements\nhello"), "ready", "Requirements"}, {"extractable.pdf", tinyPDF(), "ready", "Hello PDF"}, {"scan.pdf", []byte("not a readable PDF"), "needs_parser", ""}, {"drawing.png", []byte{1, 2, 3}, "needs_parser", ""}} {
		t.Run(test.name, func(t *testing.T) {
			p := filepath.Join(dir, test.name)
			if err := os.WriteFile(p, test.raw, 0600); err != nil {
				t.Fatal(err)
			}
			v, err := ReadDocument(context.Background(), p, managed)
			if err != nil {
				t.Fatal(err)
			}
			if v.Status != test.status || !strings.Contains(v.Text, test.contains) {
				t.Fatalf("unexpected extraction: %+v", v)
			}
			raw, err := os.ReadFile(v.CopyPath)
			if err != nil || !bytes.Equal(raw, test.raw) {
				t.Fatal("source bytes not preserved")
			}
		})
	}
}
