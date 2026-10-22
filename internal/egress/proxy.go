package egress

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var hostname = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*\.?$`)

// ProxyEndpoint contains exportable metadata only; passwords belong to a vault.
type ProxyEndpoint struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"proxy_id"`
	Label         string `json:"label"`
	Scheme        string `json:"scheme"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	CredentialRef string `json:"credential_ref,omitempty"`
	Enabled       bool   `json:"enabled"`
	Revision      uint64 `json:"revision"`
}

// ProxyInput accepts either a complete URL or split fields, never both.
// It is transient input and must not be persisted as ordinary configuration.
type ProxyInput struct {
	URL      string `json:"-"`
	Scheme   string `json:"-"`
	Host     string `json:"-"`
	Port     int    `json:"-"`
	Username string `json:"-"`
	Password string `json:"-"`
}

func (ProxyInput) String() string   { return "ProxyInput{redacted}" }
func (ProxyInput) GoString() string { return "ProxyInput{redacted}" }

// ProxyCredentials is transient secret material, excluded from JSON and fmt output.
type ProxyCredentials struct{ username, password string }

func (ProxyCredentials) String() string   { return "ProxyCredentials{redacted}" }
func (ProxyCredentials) GoString() string { return "ProxyCredentials{redacted}" }

// MarshalJSON excludes credential material from exports.
func (ProxyCredentials) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }

// Configured reports presence without revealing a username or password.
func (c ProxyCredentials) Configured() bool { return c.username != "" }

// ParseProxy separates a proxy endpoint from authentication without leaking input errors.
// Endpoint IDs, revisions and vault references are assigned by the owning service.
func ParseProxy(input ProxyInput) (ProxyEndpoint, ProxyCredentials, error) {
	invalid := func() (ProxyEndpoint, ProxyCredentials, error) {
		return ProxyEndpoint{}, ProxyCredentials{}, errors.New("invalid proxy input; provide a protocol, host and explicit port using one input format")
	}
	if len(input.URL)+len(input.Scheme)+len(input.Host)+len(input.Username)+len(input.Password) > 8192 {
		return invalid()
	}
	if input.URL != "" {
		if input.Scheme != "" || input.Host != "" || input.Port != 0 || input.Username != "" || input.Password != "" {
			return invalid()
		}
		u, err := url.Parse(input.URL)
		if err != nil || u.Opaque != "" || u.Host == "" || u.RawPath != "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return invalid()
		}
		port, err := strconv.Atoi(u.Port())
		if err != nil {
			return invalid()
		}
		input.Scheme, input.Host, input.Port = u.Scheme, u.Hostname(), port
		if u.User != nil {
			input.Username = u.User.Username()
			input.Password, _ = u.User.Password() // A username-only credential has an empty password.
		}
	}
	endpoint := ProxyEndpoint{SchemaVersion: 1, Scheme: strings.ToLower(input.Scheme), Host: strings.ToLower(input.Host), Port: input.Port}
	if endpoint.Scheme == "socks5" {
		endpoint.Scheme = "socks5h"
	}
	if err := endpoint.validateAddress(); err != nil || input.Username == "" && input.Password != "" || strings.ContainsRune(input.Username, ':') {
		return invalid()
	}
	for _, value := range []string{input.Username, input.Password} {
		if strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return invalid()
		}
	}
	return endpoint, ProxyCredentials{username: input.Username, password: input.Password}, nil
}

func (p ProxyEndpoint) validateAddress() error {
	if p.Scheme != "http" && p.Scheme != "https" && p.Scheme != "socks5h" {
		return errors.New("unsupported proxy protocol")
	}
	if p.Port < 1 || p.Port > 65535 || len(p.Host) > 253 || p.Host == "" || net.ParseIP(p.Host) == nil && !hostname.MatchString(p.Host) {
		return errors.New("invalid proxy host or port")
	}
	return nil
}

// Validate checks metadata after the owning service assigns its identity and revision.
func (p ProxyEndpoint) Validate() error {
	if p.SchemaVersion != 1 || !identifier.MatchString(p.ID) || p.Label == "" || len(p.Label) > 256 || p.Revision == 0 || p.CredentialRef != "" && !identifier.MatchString(p.CredentialRef) {
		return errors.New("invalid proxy metadata")
	}
	return p.validateAddress()
}

func (p ProxyEndpoint) proxyURL(credentials ProxyCredentials) string {
	u := url.URL{Scheme: p.Scheme, Host: net.JoinHostPort(p.Host, strconv.Itoa(p.Port))}
	if credentials.Configured() {
		u.User = url.UserPassword(credentials.username, credentials.password)
	}
	return u.String()
}

// String excludes secret transport configuration from diagnostic formatting.
func (p Policy) String() string {
	data, err := json.Marshal(p)
	if err != nil {
		return "Policy{redacted}"
	}
	return string(data)
}

// GoString applies the same redaction to %#v formatting.
func (p Policy) GoString() string { return p.String() }
