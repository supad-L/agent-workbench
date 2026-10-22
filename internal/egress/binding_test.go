package egress

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"agent-workbench/contracts"
)

func TestAccountBindingProtectionAndSnapshotIdentity(t *testing.T) {
	proxy := ProxyEndpoint{SchemaVersion: 1, ID: "proxy-1", Label: "Example", Scheme: "http", Host: "localhost", Port: 8080, Enabled: true, Revision: 3}
	account := AccountPolicy{ResourceID: "account-1"}
	binding := NetworkBinding{SchemaVersion: 1, OwnerID: "account-1", Mode: "proxy", ProxyID: "proxy-1", Revision: 5}
	snapshot, err := ResolveBinding(account, binding, &proxy)
	if err != nil {
		t.Fatal(err)
	}
	proxy.Host = "mutated.invalid"
	if snapshot.proxy.Host != "localhost" {
		t.Fatal("snapshot changed through caller-owned metadata")
	}
	if summary := snapshot.Summary(); summary.Mode != "explicit_proxy" || summary.PolicyRevision != 5 || summary.ProxyRevision != 3 {
		t.Fatal("route revisions lost")
	}
	auth := AuthContext{InstanceID: "login-1", AccountID: "account-1", PolicyRevision: 5, ProxyRevision: 3}
	if err := snapshot.ValidateAuth(auth); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*AuthContext){func(a *AuthContext) { a.AccountID = "other-account" }, func(a *AuthContext) { a.PolicyRevision++ }, func(a *AuthContext) { a.ProxyRevision++ }, func(a *AuthContext) { a.InstanceID = "" }} {
		stale := auth
		mutate(&stale)
		if snapshot.ValidateAuth(stale) == nil {
			t.Fatal("stale authentication context accepted")
		}
	}
	account.RequiresDedicatedProxy = true
	protected, err := ResolveBinding(account, binding, &proxy)
	if err != nil {
		t.Fatal(err)
	}
	if protected.Summary().Mode != "enforced_proxy" {
		t.Fatal("account protection was not derived")
	}
	if !errors.Is(protected.ValidateAuth(auth), ErrIsolationUnavailable) {
		t.Fatal("protected auth started without verified isolation")
	}
	if _, err := OpenSession(context.Background(), protected, nil); !errors.Is(err, ErrIsolationUnavailable) {
		t.Fatal("protected account downgraded to application proxy")
	}
}

func TestNetworkMetadataMatchesVersionedWireContracts(t *testing.T) {
	validator, err := contracts.New()
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{
		"proxy_endpoint":  ProxyEndpoint{SchemaVersion: 1, ID: "proxy-1", Label: "Fixture", Scheme: "socks5h", Host: "::1", Port: 1080, Enabled: true, Revision: 1},
		"network_binding": NetworkBinding{SchemaVersion: 1, OwnerID: "account-1", Mode: "proxy", ProxyID: "proxy-1", Revision: 1},
	} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := validator.Validate(name, data); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInvalidAccountRoutesNeverBecomeSystemDefaults(t *testing.T) {
	p := ProxyEndpoint{SchemaVersion: 1, ID: "proxy-1", Label: "Example", Scheme: "http", Host: "localhost", Port: 8080, Enabled: true, Revision: 1}
	for _, test := range []struct {
		account AccountPolicy
		binding NetworkBinding
		proxy   *ProxyEndpoint
	}{
		{AccountPolicy{ResourceID: "account-1"}, NetworkBinding{SchemaVersion: 1, OwnerID: "account-2", Mode: "system", Revision: 1}, nil},
		{AccountPolicy{ResourceID: "account-1"}, NetworkBinding{SchemaVersion: 1, OwnerID: "account-1", Mode: "proxy", ProxyID: p.ID, Revision: 1}, nil},
		{AccountPolicy{ResourceID: "account-1"}, NetworkBinding{SchemaVersion: 1, OwnerID: "account-1", Mode: "system", ProxyID: p.ID, Revision: 1}, nil},
		{AccountPolicy{ResourceID: "account-1", RequiresDedicatedProxy: true}, NetworkBinding{SchemaVersion: 1, OwnerID: "account-1", Mode: "system", Revision: 1}, nil},
		{AccountPolicy{ResourceID: "account-1"}, NetworkBinding{SchemaVersion: 1, OwnerID: "account-1", Mode: "automatic", Revision: 1}, nil},
		{AccountPolicy{ResourceID: "account-1"}, NetworkBinding{SchemaVersion: 1, OwnerID: "account-1", Mode: "system"}, nil},
		{AccountPolicy{ResourceID: "account-1"}, NetworkBinding{SchemaVersion: 1, OwnerID: "account-1", Mode: "proxy", ProxyID: "other-proxy", Revision: 1}, &p},
	} {
		if _, err := ResolveBinding(test.account, test.binding, test.proxy); err == nil {
			t.Fatal("invalid or protected route accepted")
		}
	}
	p.Enabled = false
	if _, err := ResolveBinding(AccountPolicy{ResourceID: "account-1"}, NetworkBinding{SchemaVersion: 1, OwnerID: "account-1", Mode: "proxy", ProxyID: p.ID, Revision: 1}, &p); err == nil {
		t.Fatal("disabled proxy fell back to system")
	}
	if _, err := OpenSession(context.Background(), Snapshot{}, nil); err == nil {
		t.Fatal("unvalidated snapshot accepted")
	}
}
