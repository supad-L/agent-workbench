package connection

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

// CredentialResource groups duplicate API credentials across connection aliases.
// It does not claim that different keys have independent upstream quotas.
func CredentialResource(pepper []byte, serviceAddress string, credential []byte) (string, error) {
	if len(credential) == 0 {
		return "", errors.New("credential required for identity fingerprint")
	}
	u, err := url.Parse(serviceAddress)
	if err != nil || u.Host == "" || u.User != nil || u.Scheme != "https" && u.Scheme != "http" {
		return "", errors.New("valid service origin required")
	}
	host := strings.ToLower(u.Host)
	if u.Scheme == "https" && u.Port() == "443" {
		host = strings.TrimSuffix(host, ":443")
	}
	if u.Scheme == "http" && u.Port() == "80" {
		host = strings.TrimSuffix(host, ":80")
	}
	return resourceKey(pepper, []string{"credential", u.Scheme + "://" + host, base64.RawStdEncoding.EncodeToString(credential)})
}

// VerifiedPrincipalResource groups an identity already verified by an authentication adapter.
// Callers must not obtain issuer/subject/workspace by trusting model or request claims.
func VerifiedPrincipalResource(pepper []byte, issuer, subject, workspace string) (string, error) {
	if issuer == "" || subject == "" {
		return "", errors.New("verified issuer and subject required")
	}
	return resourceKey(pepper, []string{"principal", issuer, subject, workspace})
}

func resourceKey(pepper []byte, parts []string) (string, error) {
	if len(pepper) < 32 {
		return "", errors.New("resource fingerprint key must contain at least 32 bytes")
	}
	encoded, err := json.Marshal(parts)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, pepper)
	if _, err := mac.Write(encoded); err != nil {
		return "", err
	}
	return "acct-" + hex.EncodeToString(mac.Sum(nil)[:24]), nil
}
