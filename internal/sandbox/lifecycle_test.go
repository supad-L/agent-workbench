package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureDocker verifies supervisor lifecycle and postconditions, not OS isolation.
type fixtureDocker struct {
	mode    string
	removed bool
	started bool
	source  string
	cancel  context.CancelFunc
	imageID string
}

func (*fixtureDocker) evidence() string { return "synthetic_driver_not_isolation_evidence" }
func (d *fixtureDocker) command(ctx context.Context, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, errors.New("empty docker command")
	}
	switch args[0] {
	case "image":
		if d.mode == "missing_image" {
			return nil, errors.New("fixture missing image")
		}
		if d.imageID != "" {
			return []byte(d.imageID), nil
		}
		return []byte("sha256:" + strings.Repeat("a", 64)), nil
	case "run":
		d.started = true
		var writable string
		for i, arg := range args {
			if arg == "--mount" {
				parts := strings.Split(args[i+1], ",")
				var source, target string
				for _, part := range parts {
					if strings.HasPrefix(part, "source=") {
						source = strings.TrimPrefix(part, "source=")
					}
					if strings.HasPrefix(part, "target=") {
						target = strings.TrimPrefix(part, "target=")
					}
				}
				if target == "/workspace" {
					d.source = source
				}
				if target == "/workspace/frontend/src" {
					writable = source
				}
			}
		}
		if d.mode == "cancelled" {
			d.cancel()
			return nil, context.Canceled
		}
		if d.mode == "run_failure" {
			return nil, errors.New("fixture runner failed")
		}
		if writable == "" || d.source == "" {
			return nil, errors.New("missing expected scoped mount")
		}
		if err := os.WriteFile(filepath.Join(writable, "allowed.txt"), []byte("allowed"), 0644); err != nil {
			return nil, err
		}
		if d.mode == "source_corrupted" {
			if err := os.WriteFile(filepath.Join(d.source, "backend", "protected.txt"), []byte("bad"), 0644); err != nil {
				return nil, err
			}
		}
		return []byte("scope-probe-passed\n"), nil
	case "container":
		return []byte("fixture-container"), nil
	case "rm":
		if d.mode == "cleanup_failure" {
			return nil, errors.New("fixture cleanup failed")
		}
		d.removed = true
		return nil, nil
	default:
		return nil, errors.New("unexpected docker command; pulling or unrelated operations forbidden")
	}
}

func TestBarePodmanImageIDAndSharedScratchDirectory(t *testing.T) {
	scratch, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	driver := &fixtureDocker{imageID: strings.Repeat("a", 64)}
	report, err := probeWithDriverAt(context.Background(), "fixture-image", driver, scratch)
	if err != nil || report.Status != "passed" || report.ImageID != "sha256:"+driver.imageID {
		t.Fatalf("unexpected bare image handling: %+v %v", report, err)
	}
	if !strings.HasPrefix(driver.source, scratch+string(filepath.Separator)) {
		t.Fatal("fixture escaped the selected shared scratch parent")
	}
	entries, err := os.ReadDir(scratch)
	if err != nil || len(entries) != 0 {
		t.Fatal("fixture directory was not cleaned up")
	}
}

func TestSupervisorCleanupAndPostconditionsWithoutDocker(t *testing.T) {
	for _, test := range []struct{ mode, want string }{{"success", "passed"}, {"run_failure", "failed"}, {"source_corrupted", "failed"}, {"cleanup_failure", "failed"}, {"missing_image", "blocked"}, {"cancelled", "failed"}} {
		t.Run(test.mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			driver := &fixtureDocker{mode: test.mode, cancel: cancel}
			report, err := probeWithDriver(ctx, "fixture-image", driver)
			if report.Status != test.want || (err == nil) != (test.want == "passed") {
				t.Fatalf("unexpected lifecycle result: %+v %v", report, err)
			}
			if driver.started && test.mode != "cleanup_failure" && !driver.removed {
				t.Fatal("started probe container was not removed, including after cancellation")
			}
			if report.Evidence == "real_docker" {
				t.Fatal("synthetic driver was mislabeled as actual isolation")
			}
			if driver.source != "" {
				if _, err := os.Stat(filepath.Dir(driver.source)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("private probe fixture was not removed")
				}
			}
		})
	}
}
