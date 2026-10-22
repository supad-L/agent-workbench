// Package apiconfig shares custom API validation and explicit live tests between CLI and desktop.
package apiconfig

import (
	"agent-workbench/internal/connection"
	"agent-workbench/internal/egress"
	"agent-workbench/internal/runtime/direct"
	"agent-workbench/prototypes/protocol"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"
)

var environmentName = regexp.MustCompile(`^AWB_[A-Z0-9_]{1,100}$`)

// Config contains no secret values; only explicit input references are serialized.
type Config struct {
	Endpoint        connection.Endpoint `json:"endpoint"`
	Model           string              `json:"model"`
	AuthKind        string              `json:"auth_kind"`
	KeyEnv          string              `json:"key_env"`
	ProxyEnv        string              `json:"proxy_env,omitempty"`
	Egress          egress.Policy       `json:"egress"`
	ChatDialect     string              `json:"chat_dialect,omitempty"`
	HeaderEnvs      map[string]string   `json:"header_envs,omitempty"`
	TimeoutSeconds  int                 `json:"timeout_seconds,omitempty"`
	MaxOutputTokens int                 `json:"max_output_tokens,omitempty"`
}

// Summary is safe for displaying the resolved destination before sending requests.
type Summary struct {
	Endpoint        string              `json:"resolved_endpoint"`
	Protocol        connection.Protocol `json:"protocol"`
	Model           string              `json:"model"`
	AuthKind        string              `json:"auth_kind"`
	Network         string              `json:"network"`
	TimeoutSeconds  int                 `json:"timeout_seconds"`
	MaxOutputTokens int                 `json:"max_output_tokens"`
}

// Load strictly decodes one bounded configuration object without reading credentials.
func Load(reader io.Reader) (Config, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, 65537))
	if err != nil || len(raw) > 65536 {
		return Config{}, errors.New("配置超过大小限制")
	}
	var value Config
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil {
		return Config{}, errors.New("配置 JSON 或字段无效")
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return Config{}, errors.New("配置只能包含一个 JSON 对象")
	}
	return value, nil
}

// Validate resolves paths/authentication and checks both network choices without I/O.
func (c Config) Validate() (Summary, error) {
	endpoint, err := c.Endpoint.Resolve()
	if err != nil {
		return Summary{}, err
	}
	if c.Model == "" || len(c.Model) > 256 {
		return Summary{}, errors.New("请填写有效模型名称")
	}
	auth := c.AuthKind
	if auth == "" {
		auth = "bearer"
		if c.Endpoint.Protocol == connection.Messages {
			auth = "api_key"
		}
	}
	if auth != "none" && !environmentName.MatchString(c.KeyEnv) {
		return Summary{}, errors.New("密钥引用需要使用显式 AWB_ 名称")
	}
	switch c.Egress.Mode {
	case "system":
		if c.Egress.DNSMode != "system" || c.ProxyEnv != "" || c.Egress.ProxyURL != "" {
			return Summary{}, errors.New("本机网络不能混入代理配置")
		}
	case "explicit_proxy", "enforced_proxy":
		if c.Egress.DNSMode != "proxy" || !environmentName.MatchString(c.ProxyEnv) {
			return Summary{}, errors.New("指定代理需要代理引用与代理侧解析")
		}
	default:
		return Summary{}, errors.New("请选择本机网络或指定代理")
	}
	if c.TimeoutSeconds < 0 || c.TimeoutSeconds > 180 || c.TimeoutSeconds > 0 && c.TimeoutSeconds < 5 || c.MaxOutputTokens < 0 || c.MaxOutputTokens > 4096 {
		return Summary{}, errors.New("超时应为 5–180 秒，测试输出上限应不超过 4096 tokens")
	}
	if len(c.HeaderEnvs) > 16 {
		return Summary{}, errors.New("额外请求头最多 16 项")
	}
	headers := map[string]string{}
	for name, ref := range c.HeaderEnvs {
		canonical := http.CanonicalHeaderKey(name)
		if !environmentName.MatchString(ref) || canonical == "Cookie" || canonical == "Proxy-Authorization" || canonical == "Set-Cookie" {
			return Summary{}, errors.New("额外请求头引用无效或使用了保留名称")
		}
		if _, exists := headers[canonical]; exists {
			return Summary{}, errors.New("请求头名称重复")
		}
		headers[canonical] = "validation-only"
	}
	if _, err = direct.New(direct.Config{Endpoint: c.Endpoint, Key: "validation-only", AuthKind: auth, Headers: headers, ChatDialect: c.ChatDialect}); err != nil {
		return Summary{}, err
	}
	timeout, tokens := c.TimeoutSeconds, c.MaxOutputTokens
	if timeout == 0 {
		timeout = 45
	}
	if tokens == 0 {
		tokens = 256
	}
	return Summary{Endpoint: endpoint, Protocol: c.Endpoint.Protocol, Model: c.Model, AuthKind: auth, Network: c.Egress.Mode, TimeoutSeconds: timeout, MaxOutputTokens: tokens}, nil
}

// Run performs exactly the explicit, bounded tool probe using only requested secret references.
func (c Config) Run(ctx context.Context, lookup func(string) string) (protocol.Report, error) {
	summary, err := c.Validate()
	if err != nil {
		return protocol.Report{}, err
	}
	policy := c.Egress
	if c.ProxyEnv != "" {
		policy.ProxyURL = lookup(c.ProxyEnv)
	}
	client, err := egress.Client(policy)
	if err != nil {
		return protocol.Report{}, err
	}
	defer client.CloseIdleConnections()
	key := ""
	if summary.AuthKind != "none" {
		key = lookup(c.KeyEnv)
		if key == "" {
			return protocol.Report{}, errors.New("请配置 API Key")
		}
	}
	headers := map[string]string{}
	for name, ref := range c.HeaderEnvs {
		value := lookup(ref)
		if value == "" {
			return protocol.Report{}, errors.New("额外请求头的值尚未配置")
		}
		headers[name] = value
	}
	model, err := direct.New(direct.Config{Endpoint: c.Endpoint, Key: key, AuthKind: summary.AuthKind, HTTPClient: client, Headers: headers, ChatDialect: c.ChatDialect})
	if err != nil {
		return protocol.Report{}, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(summary.TimeoutSeconds)*time.Second)
	defer cancel()
	return protocol.ConfiguredWithLimit(probeCtx, model, c.Model, c.Endpoint.Protocol, summary.MaxOutputTokens)
}
