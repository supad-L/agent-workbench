package egress

import "errors"

// AccountPolicy is supplied by a trusted authentication/account adapter, never a UI flag.
type AccountPolicy struct {
	ResourceID             string
	RequiresDedicatedProxy bool
}

// NetworkBinding represents the user's two mutually exclusive routing choices.
// OwnerID is a canonical account resource, shared by connection and role aliases.
type NetworkBinding struct {
	SchemaVersion int    `json:"schema_version"`
	OwnerID       string `json:"account_resource_id"`
	Mode          string `json:"ui_mode"`
	ProxyID       string `json:"proxy_id,omitempty"`
	Revision      uint64 `json:"policy_revision"`
}

// SnapshotSummary identifies an immutable route without credentials.
type SnapshotSummary struct {
	AccountID      string `json:"account_resource_id"`
	Mode           string `json:"mode"`
	PolicyRevision uint64 `json:"policy_revision"`
	ProxyID        string `json:"proxy_id,omitempty"`
	ProxyRevision  uint64 `json:"proxy_revision,omitempty"`
}

// Snapshot is created only after account ownership and proxy metadata checks.
type Snapshot struct {
	summary SnapshotSummary
	proxy   ProxyEndpoint
	valid   bool
}

// Summary returns metadata by value; callers cannot change an established route.
func (s Snapshot) Summary() SnapshotSummary { return s.summary }

// ResolveBinding derives strict protection from account policy, not protocol/model names.
func ResolveBinding(account AccountPolicy, binding NetworkBinding, proxy *ProxyEndpoint) (Snapshot, error) {
	if binding.SchemaVersion != 1 || !identifier.MatchString(account.ResourceID) || binding.OwnerID != account.ResourceID || binding.Revision == 0 {
		return Snapshot{}, errors.New("network binding does not match the canonical account resource")
	}
	s := Snapshot{valid: true, summary: SnapshotSummary{AccountID: account.ResourceID, PolicyRevision: binding.Revision}}
	switch binding.Mode {
	case "system":
		if account.RequiresDedicatedProxy || binding.ProxyID != "" || proxy != nil {
			return Snapshot{}, errors.New("account requires a dedicated proxy or binding contains conflicting routing choices")
		}
		s.summary.Mode = "system"
	case "proxy":
		if proxy == nil || !proxy.Enabled || binding.ProxyID != proxy.ID {
			return Snapshot{}, errors.New("bound proxy is missing, disabled or mismatched; direct fallback is forbidden")
		}
		if err := proxy.Validate(); err != nil {
			return Snapshot{}, err
		}
		s.proxy = *proxy
		s.summary.Mode, s.summary.ProxyID, s.summary.ProxyRevision = "explicit_proxy", proxy.ID, proxy.Revision
		if account.RequiresDedicatedProxy {
			s.summary.Mode = "enforced_proxy"
		}
	default:
		return Snapshot{}, errors.New("select system or proxy routing")
	}
	return s, nil
}

// CanDeleteProxy refuses deletion even when a referring binding is currently unusable.
func CanDeleteProxy(id string, bindings []NetworkBinding) error {
	if !identifier.MatchString(id) {
		return errors.New("invalid proxy identity")
	}
	for _, binding := range bindings {
		if binding.ProxyID == id {
			return errors.New("proxy is still referenced; rebind its accounts before deletion")
		}
	}
	return nil
}

// AuthContext pins an authentication instance to an account and route revision.
// It describes continuity, not a user authentication or authorization token.
type AuthContext struct {
	InstanceID     string `json:"instance_id"`
	AccountID      string `json:"account_resource_id"`
	PolicyRevision uint64 `json:"policy_revision"`
	ProxyRevision  uint64 `json:"proxy_revision"`
}

// ValidateAuth rejects late callbacks/refresh attempts from another policy generation.
func (s Snapshot) ValidateAuth(auth AuthContext) error {
	if !s.valid || !identifier.MatchString(auth.InstanceID) || auth.AccountID != s.summary.AccountID || auth.PolicyRevision != s.summary.PolicyRevision || auth.ProxyRevision != s.summary.ProxyRevision {
		return errors.New("authentication context belongs to a stale or different account network policy")
	}
	if s.summary.Mode == "enforced_proxy" {
		return ErrIsolationUnavailable
	}
	return nil
}
