package connection

import (
	"bytes"
	"strings"
	"testing"
)

func TestConnectionAliasesDoNotCreateIndependentAccountResources(t *testing.T) {
	pepper := bytes.Repeat([]byte{1}, 32)
	a, err := CredentialResource(pepper, "https://MODEL.example:443/api/one", []byte("synthetic-key"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := CredentialResource(pepper, "https://model.example/api/two", []byte("synthetic-key"))
	if err != nil {
		t.Fatal(err)
	}
	if a != b || strings.Contains(a, "synthetic-key") {
		t.Fatal("alias created an independent or credential-bearing resource")
	}
	c, err := CredentialResource(pepper, "https://model.example", []byte("another-key"))
	if err != nil {
		t.Fatal(err)
	}
	if a == c {
		t.Fatal("different credentials collided")
	}
	p1, err := VerifiedPrincipalResource(pepper, "issuer", "account", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := VerifiedPrincipalResource(pepper, "issuer", "account", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 {
		t.Fatal("verified identity is unstable")
	}
	if _, err := CredentialResource(nil, "https://model.example", []byte("key")); err == nil {
		t.Fatal("unkeyed identity fingerprint accepted")
	}
	if _, err := CredentialResource(pepper, "https://user:secret@model.example", []byte("key")); err == nil {
		t.Fatal("credential-bearing service origin accepted")
	}
}
