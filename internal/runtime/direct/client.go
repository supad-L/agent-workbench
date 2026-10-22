// Package direct implements bounded streaming model turns for custom API connections.
package direct

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"agent-workbench/internal/connection"
	modelruntime "agent-workbench/internal/runtime"
)

var toolName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// Config specifies explicit credentials and transport. No ambient login is read.
type Config struct {
	Endpoint    connection.Endpoint
	Key         string
	AuthKind    string
	Headers     map[string]string
	HTTPClient  *http.Client
	ChatDialect string
}

// Client implements runtime.Model for a single configured protocol.
type Client struct {
	config   Config
	endpoint string
	http     *http.Client
}

// Failure is a safe-to-display error without upstream response bodies or credentials.
type Failure struct {
	Kind       string        `json:"kind"`
	Status     int           `json:"status,omitempty"`
	RetryAfter time.Duration `json:"retry_after,omitempty"`
}

func (f *Failure) Error() string {
	return fmt.Sprintf("model request failed: %s (HTTP %d)", f.Kind, f.Status)
}

// New validates the connection and disables redirects that could forward credentials.
func New(config Config) (*Client, error) {
	endpoint, err := config.Endpoint.Resolve()
	if err != nil {
		return nil, err
	}
	if config.ChatDialect != "" && (config.ChatDialect != "deepseek" || config.Endpoint.Protocol != connection.Chat) {
		return nil, errors.New("unsupported Chat dialect or protocol combination")
	}
	if config.AuthKind == "" {
		config.AuthKind = "bearer"
		if config.Endpoint.Protocol == connection.Messages {
			config.AuthKind = "api_key"
		}
	}
	if config.AuthKind != "bearer" && config.AuthKind != "api_key" && config.AuthKind != "none" {
		return nil, errors.New("unsupported authentication kind")
	}
	if config.AuthKind != "none" && config.Key == "" {
		return nil, errors.New("credential required")
	}
	if config.AuthKind == "api_key" && config.Endpoint.Protocol != connection.Messages {
		return nil, errors.New("api_key header authentication requires Messages protocol")
	}
	for name, value := range config.Headers {
		h := http.CanonicalHeaderKey(name)
		if !validHeader(name, value) || h == "Authorization" || h == "X-Api-Key" || h == "Host" || h == "Content-Length" || h == "Transfer-Encoding" || h == "Content-Type" || h == "Connection" {
			return nil, errors.New("invalid or reserved custom header")
		}
	}
	headers := make(map[string]string, len(config.Headers))
	for name, value := range config.Headers {
		headers[name] = value
	}
	config.Headers = headers
	var hc http.Client
	if config.HTTPClient != nil {
		hc = *config.HTTPClient
	} else {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil // An unconfigured environment proxy is not an account policy.
		hc = http.Client{Transport: transport, Timeout: 2 * time.Minute}
	}
	if hc.Timeout == 0 {
		hc.Timeout = 2 * time.Minute
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{config: config, endpoint: endpoint, http: &hc}, nil
}

func validHeader(name, value string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-'
		if !valid {
			return false
		}
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

// Turn streams exactly one request. Retries belong to the orchestrator, not this method.
func (c *Client) Turn(ctx context.Context, input modelruntime.Request, emit func(modelruntime.Event) error) (result modelruntime.Result, returnErr error) {
	if err := validate(input); err != nil {
		return modelruntime.Result{}, err
	}
	body, err := buildRequest(c.config.Endpoint.Protocol, input)
	if err != nil {
		return modelruntime.Result{}, err
	}
	if c.config.ChatDialect == "deepseek" {
		// DeepSeek currently enables thinking by default and uses max_tokens.
		// This M0 dialect deliberately probes non-thinking tool continuation only.
		delete(body, "max_completion_tokens")
		body["max_tokens"] = input.MaxOutputTokens
		body["thinking"] = map[string]any{"type": "disabled"}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return modelruntime.Result{}, fmt.Errorf("encode model request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return modelruntime.Result{}, errors.New("cannot create model request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.config.Endpoint.Protocol == connection.Messages {
		req.Header.Set("Anthropic-Version", "2023-06-01")
	}
	for key, value := range c.config.Headers {
		req.Header.Set(key, value)
	}
	switch c.config.AuthKind {
	case "api_key":
		req.Header.Set("X-Api-Key", c.config.Key)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+c.config.Key)
	}
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return modelruntime.Result{}, ctx.Err()
		}
		return modelruntime.Result{}, &Failure{Kind: "transport"}
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil && returnErr == nil {
			result = modelruntime.Result{}
			returnErr = &Failure{Kind: "response_close"}
		}
	}()
	if response.StatusCode != http.StatusOK {
		kind := "upstream"
		switch response.StatusCode {
		case 401:
			kind = "authentication"
		case 403:
			kind = "permission"
		case 429:
			kind = "rate_or_quota_limit"
		case 529:
			kind = "overloaded"
		}
		wait := time.Duration(0)
		if seconds, e := strconv.ParseInt(response.Header.Get("Retry-After"), 10, 64); e == nil && seconds > 0 && seconds <= 86400 {
			wait = time.Duration(seconds) * time.Second
		}
		return modelruntime.Result{}, &Failure{Kind: kind, Status: response.StatusCode, RetryAfter: wait}
	}
	result, err = decode(c.config.Endpoint.Protocol, io.LimitReader(response.Body, 64<<20), emit)
	if ctx.Err() != nil {
		return modelruntime.Result{}, ctx.Err()
	}
	if err != nil {
		return modelruntime.Result{}, err
	}
	allowed := make(map[string]bool, len(input.Tools))
	for _, tool := range input.Tools {
		allowed[tool.Name] = true
	}
	seen := map[string]bool{}
	for _, call := range result.Calls {
		if call.ID == "" || !allowed[call.Name] || seen[call.ID] || !jsonObject(call.Arguments) {
			return modelruntime.Result{}, errors.New("provider returned invalid or unauthorized tool call")
		}
		seen[call.ID] = true
	}
	return result, nil
}

func jsonObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return len(raw) != 0 && json.Unmarshal(raw, &object) == nil && object != nil
}

func validate(input modelruntime.Request) error {
	if input.Model == "" || input.MaxOutputTokens <= 0 || len(input.Messages) == 0 {
		return errors.New("model, messages and positive output limit required")
	}
	seen := map[string]bool{}
	for _, tool := range input.Tools {
		if !toolName.MatchString(tool.Name) || seen[tool.Name] || !jsonObject(tool.Parameters) {
			return errors.New("invalid or duplicate tool definition")
		}
		seen[tool.Name] = true
	}
	for _, message := range input.Messages {
		if message.Role != "user" && message.Role != "assistant" && message.Role != "tool" {
			return errors.New("unsupported message role")
		}
		if message.Role == "tool" && message.CallID == "" {
			return errors.New("tool result requires call_id")
		}
		for _, call := range message.Calls {
			if call.ID == "" || !toolName.MatchString(call.Name) || !jsonObject(call.Arguments) {
				return errors.New("invalid history tool call")
			}
		}
		for _, item := range message.ProviderItems {
			if !jsonObject(item) {
				return errors.New("invalid preserved provider item")
			}
		}
	}
	return nil
}
