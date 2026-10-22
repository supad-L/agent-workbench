package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureSecrets struct {
	calls int
	fail  bool
}

func (v *fixtureSecrets) ProxyCredentials(ctx context.Context, ref string) (ProxyCredentials, error) {
	v.calls++
	if ctx.Err() != nil || v.fail || ref != "proxy-secret-1" {
		return ProxyCredentials{}, errors.New("SYNTHETIC_SECRET vault detail")
	}
	return ProxyCredentials{username: "fixture-user", password: "SYNTHETIC_SECRET"}, nil
}

func fixtureSnapshot(t *testing.T, proxyURL string, credentialRef string) Snapshot {
	t.Helper()
	account := AccountPolicy{ResourceID: "account-1"}
	binding := NetworkBinding{SchemaVersion: 1, OwnerID: "account-1", Mode: "system", Revision: 1}
	var proxy *ProxyEndpoint
	if proxyURL != "" {
		parsed, _, err := ParseProxy(ProxyInput{URL: proxyURL})
		if err != nil {
			t.Fatal(err)
		}
		parsed.ID, parsed.Label, parsed.Enabled, parsed.Revision, parsed.CredentialRef = "proxy-1", "Fixture", true, 1, credentialRef
		proxy = &parsed
		binding.Mode, binding.ProxyID = "proxy", parsed.ID
	}
	snapshot, err := ResolveBinding(account, binding, proxy)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestRevocationCancelsResponseBodyAndBlocksReuse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	disconnected := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.WriteString(w, "hello"); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(disconnected)
	}))
	defer target.Close()
	session, err := OpenSession(ctx, fixtureSnapshot(t, "", ""), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := session.HTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	prefix := make([]byte, 5)
	if _, err := io.ReadFull(response.Body, prefix); err != nil {
		t.Fatal(err)
	}
	session.Close()
	if _, err := response.Body.Read(make([]byte, 1)); err == nil {
		t.Fatal("revoked stream remained readable")
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-disconnected:
	case <-ctx.Done():
		t.Fatal("server request was not cancelled")
	}
	if _, err := session.HTTPClient().Do(req); !errors.Is(err, ErrPolicyRevoked) {
		t.Fatalf("revoked client was reused: %v", err)
	}
}

func TestConcurrentAccountRoutesDoNotPolluteEachOther(t *testing.T) {
	t.Setenv("NO_PROXY", "*")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var counts [2]atomic.Int32
	var sessions []*Session
	for i := range 2 {
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { counts[i].Add(1); w.WriteHeader(204) }))
		defer proxy.Close()
		snapshot := fixtureSnapshot(t, proxy.URL, "")
		snapshot.summary.AccountID = fmt.Sprintf("account-%d", i+1)
		session, err := OpenSession(ctx, snapshot, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		sessions = append(sessions, session)
	}
	var wg sync.WaitGroup
	for i := range 2 {
		for range 10 {
			wg.Go(func() {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://does-not-resolve.invalid/probe", nil)
				if err != nil {
					t.Error(err)
					return
				}
				response, err := sessions[i].HTTPClient().Do(req)
				if err != nil {
					t.Error(err)
					return
				}
				if err := response.Body.Close(); err != nil {
					t.Error(err)
				}
			})
		}
	}
	wg.Wait()
	if counts[0].Load() != 10 || counts[1].Load() != 10 {
		t.Fatal("route selection or ambient NO_PROXY changed bound proxies")
	}
	sessions[0].Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://does-not-resolve.invalid/probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := sessions[1].HTTPClient().Do(req)
	if err != nil {
		t.Fatal("unrelated account was stopped", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProxyCredentialsAreResolvedOnlyForTheBoundReference(t *testing.T) {
	ctx := context.Background()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") == "" {
			t.Error("configured proxy authentication missing")
		}
		w.WriteHeader(204)
	}))
	defer proxy.Close()
	resolver := &fixtureSecrets{}
	snapshot := fixtureSnapshot(t, proxy.URL, "proxy-secret-1")
	session, err := OpenSession(ctx, snapshot, resolver)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://provider.invalid/probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := session.HTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 1 {
		t.Fatal("credentials read outside the selected reference")
	}
	resolver.fail = true
	if _, err := OpenSession(ctx, snapshot, resolver); err == nil || err.Error() == "SYNTHETIC_SECRET vault detail" {
		t.Fatal("vault failure accepted or leaked")
	}
	if _, err := OpenSession(ctx, snapshot, nil); err == nil {
		t.Fatal("missing vault silently omitted required proxy authentication")
	}
	snapshot.summary.Mode = "enforced_proxy"
	before := resolver.calls
	if _, err := OpenSession(ctx, snapshot, resolver); !errors.Is(err, ErrIsolationUnavailable) || resolver.calls != before {
		t.Fatal("strict routing resolved credentials before isolation readiness")
	}
}
