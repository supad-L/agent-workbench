package preflight

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent-workbench/internal/sandbox"
)

func TestRealContainerNetworkPreflight(t *testing.T) {
	kind := os.Getenv("AWB_TEST_SANDBOX_RUNTIME")
	if kind == "" {
		t.Skip("explicit runtime, local image, shared scratch and built Linux helpers required")
	}
	image, scratch := os.Getenv("AWB_TEST_SANDBOX_IMAGE"), os.Getenv("AWB_TEST_SANDBOX_SCRATCH")
	if image == "" || scratch == "" {
		t.Fatal("explicit local image and shared scratch required")
	}
	root, err := os.MkdirTemp(scratch, "awb-preflight-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	}()
	helpers, err := filepath.Abs("../../bin")
	if err != nil {
		t.Fatal(err)
	}
	service, err := newService(context.Background(), filepath.Join(root, "journal"), helpers, exec.LookPath, runCommand, sandbox.Cleanup)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	host := "host.containers.internal"
	if kind == "docker" {
		host = "host.docker.internal"
	}
	if value := os.Getenv("AWB_TEST_PREFLIGHT_HOST"); value != "" {
		host = value
	}
	start := func(handler http.Handler) (*httptest.Server, string) {
		t.Helper()
		listener, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewUnstartedServer(handler)
		server.Listener = listener
		server.Start()
		t.Cleanup(server.Close)
		return server, "http://" + net.JoinHostPort(host, strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	}
	var direct, proxied atomic.Int32
	_, target := start(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { direct.Add(1); _, _ = io.WriteString(w, "203.0.113.7") }))
	proxy, proxyURL := start(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") == "" {
			t.Error("proxy auth missing")
		}
		proxied.Add(1)
		_, _ = io.WriteString(w, `{"ip":"203.0.113.8"}`)
	}))
	r := Request{Provider: "codex", Runtime: sandbox.RuntimeKind(kind), Image: image, Binary: "/awb-missing-codex", Scratch: root, Mode: "system", Target: target}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	report, err := service.Inspect(ctx, r)
	if err != nil || report.IP != "203.0.113.7" || report.ExecutionReady || report.EnvironmentReady {
		t.Fatal("system diagnostic or missing CLI misreported", report, err)
	}
	r.Mode = "proxy"
	r.Proxy = strings.Replace(proxyURL, "http://", "http://fixture:synthetic-secret@", 1)
	report, err = service.Inspect(ctx, r)
	if err != nil || report.IP != "203.0.113.8" || direct.Load() != 1 || proxied.Load() != 1 {
		t.Fatal("proxy route not isolated from direct request", report, direct.Load(), proxied.Load(), err)
	}
	proxy.Close()
	report, err = service.Inspect(ctx, r)
	if err != nil || report.IP != "" || direct.Load() != 1 || report.EnvironmentReady {
		t.Fatal("proxy outage fell back", report, err)
	}
	entries, err := os.ReadDir(service.root)
	if err != nil || len(entries) != 0 {
		t.Fatal("cleanup journal retained", entries, err)
	}
	t.Log("real container helper: system route, authenticated proxy, outage without direct fallback, missing CLI blocked, cleanup confirmed; loopback targets only")
}
