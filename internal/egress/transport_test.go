package egress

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyFailureDoesNotFallbackToDirect(t *testing.T) {
	var direct atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { direct.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }))
	proxyURL := proxy.URL
	proxy.Close()
	client, err := Client(Policy{Mode: "explicit_proxy", DNSMode: "proxy", ProxyURL: proxyURL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(req)
	if response != nil {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}
	if err == nil || direct.Load() != 0 {
		t.Fatal("unavailable proxy silently fell back to direct")
	}
}

func TestProxyReceivesHostnameWithoutLocalTargetResolution(t *testing.T) {
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "company-model.invalid" {
			t.Errorf("unexpected proxy target: %s", r.URL.Host)
		}
		requests.Add(1)
		w.WriteHeader(204)
	}))
	defer proxy.Close()
	client, err := Client(Policy{Mode: "explicit_proxy", DNSMode: "proxy", ProxyURL: proxy.URL})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://company-model.invalid/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if response.StatusCode != 204 || requests.Load() != 1 {
		t.Fatal("proxy route not used")
	}
}

func TestSystemModeUsesHostRoutingWithoutAmbientProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer target.Close()
	client, err := Client(Policy{Mode: "system", DNSMode: "system"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if response.StatusCode != 204 {
		t.Fatal("host route failed")
	}
}

func TestUnverifiedIsolationFailsClosed(t *testing.T) {
	if _, err := Client(Policy{Mode: "enforced_proxy", DNSMode: "proxy", ProxyURL: "http://127.0.0.1:1234"}); !errors.Is(err, ErrIsolationUnavailable) {
		t.Fatalf("got %v", err)
	}
	for _, policy := range []Policy{{}, {Mode: "system", DNSMode: "proxy"}, {Mode: "explicit_proxy", DNSMode: "system", ProxyURL: "http://127.0.0.1"}, {Mode: "explicit_proxy", DNSMode: "proxy", ProxyURL: "file:///tmp/proxy"}} {
		if _, err := Client(policy); err == nil {
			t.Fatal("invalid network policy accepted")
		}
	}
}
