package connection

import "testing"

func TestEndpointResolution(t *testing.T) {
	tests := []struct {
		name     string
		endpoint Endpoint
		want     string
		invalid  bool
	}{
		{"root Messages", Endpoint{Protocol: Messages, Address: "https://example.com", AddressKind: "base_url"}, "https://example.com/v1/messages", false},
		{"version Chat", Endpoint{Protocol: Chat, Address: "https://example.com/v1/", AddressKind: "base_url"}, "https://example.com/v1/chat/completions", false},
		{"prefixed Responses", Endpoint{Protocol: Responses, Address: "https://example.com/api/v1", AddressKind: "base_url"}, "https://example.com/api/v1/responses", false},
		{"already complete", Endpoint{Protocol: Messages, Address: "https://example.com/api/v1/messages", AddressKind: "base_url"}, "https://example.com/api/v1/messages", false},
		{"exact custom", Endpoint{Protocol: Messages, Address: "https://example.com/coding/message?api-version=1", AddressKind: "endpoint"}, "https://example.com/coding/message?api-version=1", false},
		{"credentials in URL", Endpoint{Protocol: Chat, Address: "https://user:secret@example.com", AddressKind: "base_url"}, "", true},
		{"remote HTTP", Endpoint{Protocol: Chat, Address: "http://example.com", AddressKind: "base_url", AllowLocalHTTP: true}, "", true},
		{"implicit local HTTP", Endpoint{Protocol: Chat, Address: "http://127.0.0.1:8080", AddressKind: "base_url"}, "", true},
		{"explicit local HTTP", Endpoint{Protocol: Chat, Address: "http://127.0.0.1:8080", AddressKind: "base_url", AllowLocalHTTP: true}, "http://127.0.0.1:8080/v1/chat/completions", false},
		{"IPv6 loopback", Endpoint{Protocol: Responses, Address: "http://[::1]:8080/v1", AddressKind: "base_url", AllowLocalHTTP: true}, "http://[::1]:8080/v1/responses", false},
		{"ambiguous kind", Endpoint{Protocol: Chat, Address: "https://example.com"}, "", true},
		{"unsupported protocol", Endpoint{Protocol: "other", Address: "https://example.com", AddressKind: "base_url"}, "", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.endpoint.Resolve()
			if (err != nil) != test.invalid {
				t.Fatalf("error=%v, want invalid=%v", err, test.invalid)
			}
			if got != test.want {
				t.Fatalf("got %q want %q", got, test.want)
			}
		})
	}
}
