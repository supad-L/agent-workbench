package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// FileDiff is a reviewable change for one path. Text is a git-compatible
// unified diff section (including headers) unless the file is binary or too large.
type FileDiff struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Binary    bool   `json:"binary,omitempty"`
	TooLarge  bool   `json:"too_large,omitempty"`
	Added     int    `json:"added"`
	Removed   int    `json:"removed"`
	Text      string `json:"text,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

const (
	maxDiffFileBytes = 1 << 20
	maxDiffEdit      = 2000
	diffContext      = 3
)

// UnifiedDiffs renders changes between base and work. Output beyond maxTotal
// bytes is omitted per file and flagged as truncated.
func UnifiedDiffs(base, work string, changes []Change, maxTotal int) ([]FileDiff, error) {
	result := []FileDiff{}
	total := 0
	for _, change := range changes {
		before, beforeOK, err := readForDiff(base, change.Path)
		if err != nil {
			return nil, err
		}
		after, afterOK, err := readForDiff(work, change.Path)
		if err != nil {
			return nil, err
		}
		item := FileDiff{Path: change.Path, Kind: change.Kind}
		switch {
		case !beforeOK && !afterOK:
			continue
		case len(before) > maxDiffFileBytes || len(after) > maxDiffFileBytes:
			item.TooLarge = true
		case isBinary(before) || isBinary(after):
			item.Binary = true
		default:
			text, added, removed, ok := renderUnified(change.Path, before, after, beforeOK, afterOK)
			item.Added, item.Removed = added, removed
			if !ok {
				item.TooLarge = true
			} else if total+len(text) > maxTotal {
				item.Truncated = true
			} else {
				item.Text = text
				total += len(text)
			}
		}
		result = append(result, item)
	}
	return result, nil
}

// CombinedPatch concatenates the text sections into one file applicable with git apply.
func CombinedPatch(diffs []FileDiff) string {
	var out strings.Builder
	for _, d := range diffs {
		out.WriteString(d.Text)
	}
	return out.String()
}

func readForDiff(dir, name string) (data []byte, exists bool, returnErr error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, false, err
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	info, err := root.Lstat(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, nil
	}
	if info.Size() > maxDiffFileBytes {
		return make([]byte, maxDiffFileBytes+1), true, nil
	}
	data, err = fs.ReadFile(root.FS(), filepath.ToSlash(name))
	return data, true, err
}

func isBinary(data []byte) bool {
	sample := data
	if len(sample) > 8000 {
		sample = sample[:8000]
	}
	return bytes.IndexByte(sample, 0) >= 0 || !utf8.Valid(sample)
}

type line struct {
	text      string
	noEOLMark bool
}

func splitLines(data []byte) []line {
	if len(data) == 0 {
		return nil
	}
	parts := strings.SplitAfter(string(data), "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	result := make([]line, len(parts))
	for i, p := range parts {
		result[i] = line{text: strings.TrimSuffix(p, "\n"), noEOLMark: !strings.HasSuffix(p, "\n")}
	}
	return result
}

type op struct {
	kind byte // ' ', '-', '+'
	a, b int  // indexes into old/new (valid for the kind)
}

// editScript computes a shortest edit script with Myers' algorithm. Edit
// distance is capped so pathological rewrites fall back to "too large".
func editScript(a, b []line) ([]op, bool) {
	n, m := len(a), len(b)
	maxD := n + m
	if maxD > maxDiffEdit {
		maxD = maxDiffEdit
	}
	offset := maxD + 1
	v := make([]int, 2*maxD+3)
	// trace[d] holds v[-d-1 .. d+1] as it was before round d.
	trace := [][]int{}
	equal := func(x, y int) bool { return a[x].text == b[y].text && a[x].noEOLMark == b[y].noEOLMark }
	found := false
	for d := 0; d <= maxD && !found; d++ {
		lo, hi := offset-d-1, offset+d+2
		if lo < 0 {
			lo = 0
		}
		if hi > len(v) {
			hi = len(v)
		}
		snapshot := make([]int, hi-lo)
		copy(snapshot, v[lo:hi])
		trace = append(trace, snapshot)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && equal(x, y) {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
	}
	if !found {
		return nil, false
	}
	at := func(d, k int) int {
		lo := offset - d - 1
		if lo < 0 {
			lo = 0
		}
		i := offset + k - lo
		if i < 0 || i >= len(trace[d]) {
			return 0
		}
		return trace[d][i]
	}
	ops := []op{}
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		k := x - y
		var prevK int
		if k == -d || (k != d && at(d, k-1) < at(d, k+1)) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := at(d, prevK)
		prevY := prevX - prevK
		if d == 0 {
			prevX, prevY = 0, 0
		}
		for x > prevX && y > prevY {
			x--
			y--
			ops = append(ops, op{' ', x, y})
		}
		if d > 0 {
			if x == prevX {
				y--
				ops = append(ops, op{'+', x, y})
			} else {
				x--
				ops = append(ops, op{'-', x, y})
			}
		}
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops, true
}

func renderUnified(name string, before, after []byte, beforeOK, afterOK bool) (string, int, int, bool) {
	a, b := splitLines(before), splitLines(after)
	ops, ok := editScript(a, b)
	if !ok {
		return "", 0, 0, false
	}
	added, removed := 0, 0
	for _, o := range ops {
		switch o.kind {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "diff --git a/%s b/%s\n", name, name)
	oldName, newName := "a/"+name, "b/"+name
	if !beforeOK {
		out.WriteString("new file mode 100644\n")
		oldName = "/dev/null"
	}
	if !afterOK {
		out.WriteString("deleted file mode 100644\n")
		newName = "/dev/null"
	}
	if added == 0 && removed == 0 {
		return out.String(), 0, 0, true
	}
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", oldName, newName)
	// Group changes into hunks with surrounding context.
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := i - diffContext
		if start < 0 {
			start = 0
		}
		end := i
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run == len(ops) || run-end > 2*diffContext {
				end += min(diffContext, run-end)
				break
			}
			end = run
		}
		oldStart, newStart, oldCount, newCount := -1, -1, 0, 0
		for _, o := range ops[start:end] {
			if oldStart < 0 {
				oldStart, newStart = o.a, o.b
			}
			if o.kind != '+' {
				oldCount++
			}
			if o.kind != '-' {
				newCount++
			}
		}
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", hunkRange(oldStart, oldCount), hunkRange(newStart, newCount))
		for _, o := range ops[start:end] {
			var l line
			if o.kind == '+' {
				l = b[o.b]
			} else {
				l = a[o.a]
			}
			out.WriteByte(o.kind)
			out.WriteString(l.text)
			out.WriteByte('\n')
			if l.noEOLMark {
				out.WriteString("\\ No newline at end of file\n")
			}
		}
		i = end
	}
	return out.String(), added, removed, true
}

func hunkRange(start, count int) string {
	if count == 0 {
		return fmt.Sprintf("%d,0", start)
	}
	if count == 1 {
		return fmt.Sprintf("%d", start+1)
	}
	return fmt.Sprintf("%d,%d", start+1, count)
}
