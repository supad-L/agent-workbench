// Package egress separates host routing, explicit proxies and verified isolation.
package egress

import (
	"errors"
	"net/http"
	"net/url"
	"time"
)

// ErrIsolationUnavailable prevents an application proxy from impersonating a kill switch.
var ErrIsolationUnavailable = errors.New("enforced proxy requires a verified network-isolated execution environment")

// Policy belongs to one connection; it never changes global Clash or OS settings.
type Policy struct {
	Mode     string `json:"mode"`
	DNSMode  string `json:"dns_mode"`
	ProxyURL string `json:"-"`
}

// Client creates an explicit HTTP transport without ambient proxy fallback.
// System mode follows OS routing (including TUN), but does not read macOS UI proxy settings.
func Client(policy Policy) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	switch policy.Mode {
	case "system":
		if policy.ProxyURL != "" || policy.DNSMode != "system" {
			return nil, errors.New("system routing requires system DNS and no explicit proxy")
		}
	case "explicit_proxy":
		if policy.DNSMode != "proxy" {
			return nil, errors.New("explicit proxy requires proxy-side target resolution")
		}
		endpoint, credentials, err := ParseProxy(ProxyInput{URL: policy.ProxyURL})
		if err != nil {
			return nil, err
		}
		proxyURL, err := url.Parse(endpoint.proxyURL(credentials))
		if err != nil {
			return nil, errors.New("invalid normalized proxy endpoint")
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	case "enforced_proxy":
		return nil, ErrIsolationUnavailable
	default:
		return nil, errors.New("explicit network mode required")
	}
	return &http.Client{Transport: transport, Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
