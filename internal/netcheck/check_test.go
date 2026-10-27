package netcheck

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRouteAndResponseBoundaries(t *testing.T) {
	var direct atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("unexpected credentials")
		}
		_, _ = io.WriteString(w, "203.0.113.7")
	}))
	defer target.Close()
	got := Check(context.Background(), Input{Target: target.URL, Mode: "system"})
	if !got.Reachable || got.IP != "203.0.113.7" {
		t.Fatal(got)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"ip":"2001:db8::1"}`) }))
	got = Check(context.Background(), Input{Target: target.URL, Mode: "proxy", Proxy: proxy.URL})
	if got.IP != "2001:db8::1" || direct.Load() != 1 {
		t.Fatal(got, direct.Load())
	}
	proxy.Close()
	got = Check(context.Background(), Input{Target: target.URL, Mode: "proxy", Proxy: proxy.URL})
	if got.Reachable || direct.Load() != 1 {
		t.Fatal("proxy failure fell back", got, direct.Load())
	}
	for _, body := range []string{"sensitive-body-not-an-ip", strings.Repeat("x", 600), `{"ip":"invalid"}`} {
		t.Run(body[:7], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
			defer server.Close()
			result := Check(context.Background(), Input{Target: server.URL, Mode: "system"})
			if !result.Reachable || result.IP != "" {
				t.Fatal(result)
			}
		})
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	if Check(context.Background(), Input{Target: redirect.URL, Mode: "system"}).Reachable || direct.Load() != 1 {
		t.Fatal("redirect followed")
	}
}

func TestInputAndCancellation(t *testing.T) {
	for _, input := range []Input{
		{Target: "https://user:secret@example.com", Mode: "system"}, {Target: "https://example.com?token=secret", Mode: "system"},
		{Target: "http://example.com", Mode: "system"}, {Target: "https://example.com#secret", Mode: "system"},
		{Target: "https://example.com", Mode: "system", Proxy: "http://127.0.0.1:1"}, {Target: "https://example.com", Mode: "proxy"},
		{Target: "https://example.com", Mode: "enforced_proxy"}, {Target: "https://example.com\\evil", Mode: "system"},
	} {
		if Validate(input) == nil || Check(context.Background(), input).Reachable {
			t.Fatal("invalid route accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Check(ctx, Input{Target: "http://127.0.0.1:1", Mode: "system"}).Reachable {
		t.Fatal("canceled check passed")
	}
	for _, raw := range []string{`{"target":"https://example.com","mode":"system","access_token":"secret"}`, `{}`, `{} {}`, strings.Repeat("x", 9000)} {
		var output bytes.Buffer
		if Serve(ctx, strings.NewReader(raw), &output) != nil || !strings.Contains(output.String(), "invalid_configuration") || strings.Contains(output.String(), "secret") {
			t.Fatal(output.String())
		}
	}
}
