package egress

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestProxyInputEquivalenceAndSecretRedaction(t *testing.T) {
	for _, test := range []struct {
		name         string
		input        ProxyInput
		scheme, host string
		port         int
	}{
		{"URL", ProxyInput{URL: "https://alice:p%40ss%3Aword@proxy.example:8443/"}, "https", "proxy.example", 8443},
		{"split", ProxyInput{Scheme: "https", Host: "PROXY.EXAMPLE", Port: 8443, Username: "alice", Password: "p@ss:word"}, "https", "proxy.example", 8443},
		{"IPv6 SOCKS", ProxyInput{URL: "socks5://[::1]:1080"}, "socks5h", "::1", 1080},
	} {
		t.Run(test.name, func(t *testing.T) {
			endpoint, secret, err := ParseProxy(test.input)
			if err != nil || endpoint.Scheme != test.scheme || endpoint.Host != test.host || endpoint.Port != test.port {
				t.Fatalf("invalid normalized endpoint: %+v %v", endpoint, err)
			}
			if test.name != "IPv6 SOCKS" && (!secret.Configured() || secret.username != "alice" || secret.password != "p@ss:word") {
				t.Fatal("encoded credentials did not round-trip")
			}
			for _, value := range []any{secret, test.input, Policy{Mode: "explicit_proxy", DNSMode: "proxy", ProxyURL: endpoint.proxyURL(secret)}} {
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				for _, text := range []string{string(encoded), fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
					if strings.Contains(text, "alice") || strings.Contains(text, "p@ss") || strings.Contains(text, "p%40ss") {
						t.Fatal("proxy authentication leaked into JSON or fmt output")
					}
				}
			}
		})
	}
}

func TestInvalidProxyInputsNeverEchoSecrets(t *testing.T) {
	for _, input := range []ProxyInput{
		{URL: "proxy.example:8080"},
		{URL: "http://alice:SYNTHETIC_SECRET@proxy.example"},
		{URL: "http://proxy.example:8080", Host: "other.example"},
		{URL: "http://proxy.example:8080", Password: "SYNTHETIC_SECRET"},
		{URL: "http://proxy.example:8080/path"},
		{URL: "http://proxy.example:8080?SYNTHETIC_SECRET"},
		{URL: "http://proxy.example:8080#SYNTHETIC_SECRET"},
		{URL: "ftp://proxy.example:8080"},
		{URL: "http://proxy.example:0"},
		{URL: "http://proxy.example:65536"},
		{URL: "http://user:SYNTHETIC_SECRET%0a@proxy.example:8080"},
		{Scheme: "http", Host: "user:SYNTHETIC_SECRET@proxy.example", Port: 8080},
		{Scheme: "http", Host: "proxy.example", Port: 8080, Password: "SYNTHETIC_SECRET"},
		{URL: strings.Repeat("SYNTHETIC_SECRET", 1024)},
	} {
		_, _, err := ParseProxy(input)
		if err == nil || strings.Contains(err.Error(), "SYNTHETIC_SECRET") {
			t.Fatal("invalid input accepted or sensitive error returned")
		}
	}
}

func TestProxyMetadataAndReferenceGuard(t *testing.T) {
	p := ProxyEndpoint{SchemaVersion: 1, ID: "proxy-1", Label: "Example proxy", Scheme: "http", Host: "proxy.example", Port: 8080, Enabled: true, Revision: 1}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*ProxyEndpoint){func(p *ProxyEndpoint) { p.Revision = 0 }, func(p *ProxyEndpoint) { p.ID = "" }, func(p *ProxyEndpoint) { p.Label = "" }, func(p *ProxyEndpoint) { p.CredentialRef = "plain secret" }, func(p *ProxyEndpoint) { p.Scheme = "file" }} {
		invalid := p
		change(&invalid)
		if invalid.Validate() == nil {
			t.Fatal("invalid metadata accepted")
		}
	}
	if CanDeleteProxy(p.ID, []NetworkBinding{{SchemaVersion: 1, OwnerID: "account-1", Mode: "proxy", ProxyID: p.ID, Revision: 1}}) == nil {
		t.Fatal("referenced proxy deletion accepted")
	}
	if err := CanDeleteProxy(p.ID, nil); err != nil {
		t.Fatal(err)
	}
	if CanDeleteProxy("bad identity", nil) == nil {
		t.Fatal("invalid proxy deletion identity accepted")
	}
}
