package apiconfig

import (
	"agent-workbench/internal/connection"
	"agent-workbench/internal/egress"
	"context"
	"strings"
	"testing"
)

func valid() Config {
	return Config{Endpoint: connection.Endpoint{Protocol: connection.Messages, Address: "https://company.example.invalid/gateway/v1", AddressKind: "base_url"}, Model: "company-model", AuthKind: "bearer", KeyEnv: "AWB_COMPANY_KEY", Egress: egress.Policy{Mode: "system", DNSMode: "system"}}
}
func TestConfigurationBoundsAndSecretReferencesBeforeAnyLookup(t *testing.T) {
	for name, mutate := range map[string]func(*Config){"missing model": func(c *Config) { c.Model = "" }, "ambient key": func(c *Config) { c.KeyEnv = "HOME" }, "mixed routing": func(c *Config) { c.ProxyEnv = "AWB_PROXY" }, "missing proxy": func(c *Config) { c.Egress = egress.Policy{Mode: "explicit_proxy", DNSMode: "proxy"} }, "wrong DNS": func(c *Config) { c.Egress.DNSMode = "proxy" }, "unknown mode": func(c *Config) { c.Egress.Mode = "automatic" }, "timeout too long": func(c *Config) { c.TimeoutSeconds = 181 }, "timeout too short": func(c *Config) { c.TimeoutSeconds = 1 }, "token limit": func(c *Config) { c.MaxOutputTokens = 4097 }, "reserved cookie": func(c *Config) { c.HeaderEnvs = map[string]string{"Cookie": "AWB_HEADER"} }, "duplicate headers": func(c *Config) { c.HeaderEnvs = map[string]string{"X-Route": "AWB_A", "x-route": "AWB_B"} }, "ambient header": func(c *Config) { c.HeaderEnvs = map[string]string{"X-Route": "PATH"} }, "bad auth": func(c *Config) { c.AuthKind = "automatic" }} {
		t.Run(name, func(t *testing.T) {
			c := valid()
			mutate(&c)
			lookups := 0
			if _, err := c.Run(context.Background(), func(string) string { lookups++; return "synthetic-only" }); err == nil || lookups != 0 {
				t.Fatal("invalid configuration accessed secret input or was accepted")
			}
		})
	}
	c := valid()
	summary, err := c.Validate()
	if err != nil || summary.Endpoint != "https://company.example.invalid/gateway/v1/messages" {
		t.Fatal("company prefix resolution failed", err)
	}
	if _, err = c.Run(context.Background(), func(string) string { return "" }); err == nil {
		t.Fatal("missing API key accepted")
	}
	c.HeaderEnvs = map[string]string{"X-Route": "AWB_ROUTE"}
	if _, err = c.Run(context.Background(), func(ref string) string {
		if ref == c.KeyEnv {
			return "synthetic-key"
		}
		return ""
	}); err == nil {
		t.Fatal("missing header value accepted")
	}
	c = valid()
	c.Egress = egress.Policy{Mode: "explicit_proxy", DNSMode: "proxy"}
	c.ProxyEnv = "AWB_PROXY"
	if _, err = c.Run(context.Background(), func(string) string { return "" }); err == nil {
		t.Fatal("missing proxy fell back to system")
	}
	for _, raw := range []string{strings.Repeat(" ", 65537), `{} {}`, `{"api_key":"synthetic-secret"}`} {
		if _, err := Load(strings.NewReader(raw)); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}
