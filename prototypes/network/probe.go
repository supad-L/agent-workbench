// Package network verifies host HTTP routing with loopback fixtures only.
package network

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	"agent-workbench/internal/egress"
)

// Report distinguishes application HTTP experiments from network-layer isolation.
type Report struct {
	Evidence                    string `json:"evidence"`
	SystemRoute                 bool   `json:"system_route"`
	ExplicitProxy               bool   `json:"explicit_proxy"`
	ProxyFailureBlocked         bool   `json:"proxy_failure_blocked"`
	RevokedSessionBlocked       bool   `json:"revoked_session_blocked"`
	IndependentAccountContinues bool   `json:"independent_account_continues"`
	StrictIsolation             string `json:"strict_isolation"`
}

func snapshot(account string, proxyURL string, protected bool) (egress.Snapshot, error) {
	binding := egress.NetworkBinding{SchemaVersion: 1, OwnerID: account, Mode: "system", Revision: 1}
	var proxy *egress.ProxyEndpoint
	if proxyURL != "" {
		parsed, _, err := egress.ParseProxy(egress.ProxyInput{URL: proxyURL})
		if err != nil {
			return egress.Snapshot{}, err
		}
		parsed.ID, parsed.Label, parsed.Enabled, parsed.Revision = "fixture-proxy", "Loopback fixture", true, 1
		proxy = &parsed
		binding.Mode, binding.ProxyID = "proxy", parsed.ID
	}
	return egress.ResolveBinding(egress.AccountPolicy{ResourceID: account, RequiresDedicatedProxy: protected}, binding, proxy)
}

func request(ctx context.Context, client *http.Client, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return errors.New("unexpected loopback fixture status")
	}
	return errors.Join(readErr, closeErr)
}

// Run creates no public/model requests, reads no credentials, and changes no proxy settings.
func Run(ctx context.Context) (Report, error) {
	report := Report{Evidence: "loopback_http_not_network_isolation", StrictIsolation: "unverified_blocked"}
	var directHits, proxyHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { directHits.Add(1); w.WriteHeader(http.StatusNoContent) }))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { proxyHits.Add(1); w.WriteHeader(http.StatusNoContent) }))
	defer proxy.Close()
	systemSnapshot, err := snapshot("system-account", "", false)
	if err != nil {
		return report, err
	}
	systemSession, err := egress.OpenSession(ctx, systemSnapshot, nil)
	if err != nil {
		return report, err
	}
	defer systemSession.Close()
	if err := request(ctx, systemSession.HTTPClient(), target.URL); err != nil {
		return report, err
	}
	report.SystemRoute = directHits.Load() == 1
	proxySnapshot, err := snapshot("proxy-account", proxy.URL, false)
	if err != nil {
		return report, err
	}
	proxySession, err := egress.OpenSession(ctx, proxySnapshot, nil)
	if err != nil {
		return report, err
	}
	defer proxySession.Close()
	if err := request(ctx, proxySession.HTTPClient(), target.URL); err != nil {
		return report, err
	}
	report.ExplicitProxy = proxyHits.Load() == 1 && directHits.Load() == 1
	protectedSnapshot, err := snapshot("protected-account", proxy.URL, true)
	if err != nil {
		return report, err
	}
	if session, err := egress.OpenSession(ctx, protectedSnapshot, nil); !errors.Is(err, egress.ErrIsolationUnavailable) {
		if session != nil {
			session.Close()
		}
		return report, errors.New("unverified strict route was accepted")
	}
	proxy.Close()
	report.ProxyFailureBlocked = request(ctx, proxySession.HTTPClient(), target.URL) != nil && directHits.Load() == 1
	proxySession.Close()
	report.RevokedSessionBlocked = errors.Is(request(ctx, proxySession.HTTPClient(), target.URL), egress.ErrPolicyRevoked)
	if err := request(ctx, systemSession.HTTPClient(), target.URL); err != nil {
		return report, err
	}
	report.IndependentAccountContinues = directHits.Load() == 2
	if !report.SystemRoute || !report.ExplicitProxy || !report.ProxyFailureBlocked || !report.RevokedSessionBlocked || !report.IndependentAccountContinues {
		return report, errors.New("loopback account routing probe failed")
	}
	return report, nil
}
