// Package connection validates explicit model-service configuration.
package connection

import (
	"errors"
	"net"
	"net/url"
	"path"
	"strings"
)

// Protocol selects the request and streaming wire format.
type Protocol string

const (
	// Messages is the Anthropic Messages wire protocol.
	Messages Protocol = "messages"
	// Chat is the OpenAI Chat Completions wire protocol.
	Chat Protocol = "chat_completions"
	// Responses is the OpenAI Responses wire protocol.
	Responses Protocol = "responses"
)

// Endpoint specifies a base address or an exact endpoint.
type Endpoint struct {
	Protocol       Protocol `json:"protocol"`
	Address        string   `json:"address"`
	AddressKind    string   `json:"address_kind"`
	AllowLocalHTTP bool     `json:"allow_local_http"`
}

// Resolve validates the address and appends an endpoint path exactly once.
func (e Endpoint) Resolve() (string, error) {
	var suffix string
	switch e.Protocol {
	case Messages:
		suffix = "/messages"
	case Chat:
		suffix = "/chat/completions"
	case Responses:
		suffix = "/responses"
	default:
		return "", errors.New("unsupported protocol")
	}
	u, err := url.Parse(e.Address)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("invalid model endpoint")
	}
	if u.Scheme != "https" {
		host := u.Hostname()
		ip := net.ParseIP(host)
		local := strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
		if u.Scheme != "http" || !e.AllowLocalHTTP || !local {
			return "", errors.New("HTTPS required; HTTP is restricted to explicitly enabled loopback services")
		}
	}
	if strings.Contains(u.Path, "\\") || strings.Contains(u.Path, "\x00") {
		return "", errors.New("invalid endpoint path")
	}
	switch e.AddressKind {
	case "endpoint":
		if u.Path == "" || u.Path == "/" {
			return "", errors.New("complete endpoint requires a path")
		}
	case "base_url":
		p := strings.TrimRight(u.Path, "/")
		if strings.HasSuffix(p, suffix) {
			u.Path = p
		} else if strings.HasSuffix(p, "/v1") {
			u.Path = p + suffix
		} else {
			u.Path = path.Join("/", p, "v1") + suffix
		}
		u.RawPath = ""
	default:
		return "", errors.New("address_kind must be base_url or endpoint")
	}
	return u.String(), nil
}
