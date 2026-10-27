// Package netcheck makes a single explicit diagnostic HTTP request. It never
// authenticates to a provider, follows redirects, or falls back from a proxy.
package netcheck

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agent-workbench/internal/egress"
)

// Input travels only over the helper's stdin, never process arguments or logs.
type Input struct {
	Target string `json:"target"`
	Mode   string `json:"mode"`
	Proxy  string `json:"proxy,omitempty"`
}

// Result excludes URLs, credentials, response bodies and native diagnostics.
type Result struct {
	Reachable bool   `json:"reachable"`
	IP        string `json:"ip,omitempty"`
	Code      string `json:"code"`
}

// Validate checks explicit targets and mutually exclusive network choices.
func Validate(input Input) error {
	if len(input.Target) > 2048 || strings.ContainsAny(input.Target, "\r\n\\") {
		return errors.New("检测目标无效")
	}
	u, err := url.Parse(input.Target)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return errors.New("检测目标需要无认证、查询参数和片段的 HTTP(S) 地址")
	}
	ip := net.ParseIP(u.Hostname())
	local := ip != nil && (ip.IsLoopback() || ip.IsPrivate()) || u.Hostname() == "host.containers.internal" || u.Hostname() == "host.docker.internal" || u.Hostname() == "localhost"
	if u.Scheme != "https" && (u.Scheme != "http" || !local) {
		return errors.New("公网检测目标必须使用 HTTPS；HTTP 仅用于明确的本地检测")
	}
	switch input.Mode {
	case "system":
		if input.Proxy != "" {
			return errors.New("跟随运行时网络不能同时指定代理")
		}
	case "proxy":
		if _, _, err := egress.ParseProxy(egress.ProxyInput{URL: input.Proxy}); err != nil {
			return errors.New("指定代理格式无效或缺失")
		}
	default:
		return errors.New("请选择运行时网络或指定代理")
	}
	return nil
}

// Check sends one bounded GET. Only a plain IP or {"ip":"..."} is disclosed.
func Check(ctx context.Context, input Input) Result {
	if Validate(input) != nil {
		return Result{Code: "invalid_configuration"}
	}
	policy := egress.Policy{Mode: "system", DNSMode: "system"}
	if input.Mode == "proxy" {
		policy = egress.Policy{Mode: "explicit_proxy", DNSMode: "proxy", ProxyURL: input.Proxy}
	}
	client, err := egress.Client(policy)
	if err != nil {
		return Result{Code: "transport_unavailable"}
	}
	defer client.CloseIdleConnections()
	client.Timeout = 6 * time.Second
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, input.Target, nil)
	if err != nil {
		return Result{Code: "invalid_configuration"}
	}
	req.Header.Set("Accept", "text/plain, application/json")
	response, err := client.Do(req)
	if err != nil {
		return Result{Code: "route_failed"}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{Code: "target_rejected"}
	}
	result := Result{Reachable: true, Code: "reachable_ip_unknown"}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 513))
	if err != nil || len(raw) > 512 {
		return result
	}
	value := strings.TrimSpace(string(raw))
	if net.ParseIP(value) == nil {
		var body struct {
			IP string `json:"ip"`
		}
		if json.Unmarshal(raw, &body) == nil {
			value = body.IP
		}
	}
	if ip := net.ParseIP(value); ip != nil {
		result.IP = ip.String()
		result.Code = "reachable"
	}
	return result
}

// Serve bounds stdin, rejects unknown fields and emits only a normalized result.
func Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	decoder := json.NewDecoder(io.LimitReader(in, 8193))
	decoder.DisallowUnknownFields()
	var input Input
	result := Result{Code: "invalid_configuration"}
	if decoder.Decode(&input) == nil {
		var extra any
		if errors.Is(decoder.Decode(&extra), io.EOF) {
			result = Check(ctx, input)
		}
	}
	return json.NewEncoder(out).Encode(result)
}
