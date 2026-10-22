package egress

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
)

// ErrPolicyRevoked identifies a closed or superseded account network session.
var ErrPolicyRevoked = errors.New("account network policy was revoked")

// SecretResolver is a future vault boundary; only the explicit proxy reference is resolved.
type SecretResolver interface {
	ProxyCredentials(context.Context, string) (ProxyCredentials, error)
}

// Session owns one immutable route and its connections. Configuration owners must
// close affected sessions before activating replacement bindings or credentials.
// This is an application HTTP primitive, not process/browser network isolation.
type Session struct {
	ctx       context.Context
	cancel    context.CancelFunc
	client    *http.Client
	transport *http.Transport
	once      sync.Once
}

// OpenSession validates protection before resolving any credentials or sending traffic.
func OpenSession(ctx context.Context, snapshot Snapshot, secrets SecretResolver) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !snapshot.valid {
		return nil, errors.New("validated network snapshot required")
	}
	if snapshot.summary.Mode == "enforced_proxy" {
		return nil, ErrIsolationUnavailable
	}
	policy := Policy{Mode: snapshot.summary.Mode, DNSMode: "system"}
	if policy.Mode == "explicit_proxy" {
		var credentials ProxyCredentials
		if snapshot.proxy.CredentialRef != "" {
			if secrets == nil {
				return nil, errors.New("proxy credential store is unavailable")
			}
			var err error
			credentials, err = secrets.ProxyCredentials(ctx, snapshot.proxy.CredentialRef)
			if err != nil || !credentials.Configured() {
				return nil, errors.New("bound proxy credentials are unavailable")
			}
		}
		policy.DNSMode, policy.ProxyURL = "proxy", snapshot.proxy.proxyURL(credentials)
	}
	client, err := Client(policy)
	if err != nil {
		return nil, err
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		return nil, errors.New("owned HTTP transport required")
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	s := &Session{ctx: sessionCtx, cancel: cancel, client: client, transport: transport}
	client.Transport = &sessionTransport{owner: s}
	return s, nil
}

// HTTPClient supplies the bound transport to an approved model adapter.
func (s *Session) HTTPClient() *http.Client { return s.client }

// Close cancels in-flight requests/response streams and closes idle connections.
// After Close returns, new requests through this session fail without sending.
func (s *Session) Close() {
	s.once.Do(func() {
		s.cancel()
		s.transport.CloseIdleConnections()
	})
}

type sessionTransport struct{ owner *Session }

func (t *sessionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t.owner.ctx.Err() != nil {
		return nil, ErrPolicyRevoked
	}
	ctx, cancel := context.WithCancel(request.Context())
	stop := context.AfterFunc(t.owner.ctx, cancel)
	finish := func() { stop(); cancel() }
	if t.owner.ctx.Err() != nil {
		finish()
		return nil, ErrPolicyRevoked
	}
	response, err := t.owner.transport.RoundTrip(request.Clone(ctx))
	if err != nil {
		finish()
		if t.owner.ctx.Err() != nil {
			return nil, ErrPolicyRevoked
		}
		if request.Context().Err() != nil {
			return nil, request.Context().Err()
		}
		// Transport errors may contain proxy usernames or URLs; keep them private.
		return nil, errors.New("request through the bound network policy failed")
	}
	response.Body = &sessionBody{ReadCloser: response.Body, finish: finish}
	return response, nil
}

type sessionBody struct {
	io.ReadCloser
	finish func()
	once   sync.Once
}

func (b *sessionBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.once.Do(b.finish)
	}
	return n, err
}

func (b *sessionBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.finish)
	return err
}
