package federation

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeKeyPackages is a KeyPackageService stand-in: a fixed user->device->package map.
type fakeKeyPackages map[string]map[string][]byte

func (f fakeKeyPackages) DevicesWithKeyPackages(_ context.Context, userID string) ([]string, error) {
	devs := f[userID]
	if len(devs) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(devs))
	for d := range devs {
		out = append(out, d)
	}
	return out, nil
}

func (f fakeKeyPackages) ClaimKeyPackage(_ context.Context, userID, deviceID string) ([]byte, error) {
	return f[userID][deviceID], nil
}

// A hub on host A claims a key package for a user on host B, over the signed S2S
// transport — the material A needs to add B's user to a group. This is the piece
// that makes a remote member reachable.
func TestCrossHostKeyPackageClaim(t *testing.T) {
	aKey := hostKey(t, 1)
	aPub := aKey.Public().(ed25519.PublicKey)

	// B trusts A, and B's user "bob" has published a package for one device.
	packages := fakeKeyPackages{"bob": {"phone": []byte("bob-phone-key-package")}}
	mux := http.NewServeMux()
	NewHandler("b.example", fakeLookup{"a.example": aPub}, nil).WithKeyPackages(packages).Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := NewClient("a.example", "a-key-1", aKey)
	client.PeerURL = func(string) string { return srv.URL }

	claimed, err := client.ClaimRemoteKeyPackages(context.Background(), "b.example", "bob")
	if err != nil {
		t.Fatalf("cross-host claim failed: %v", err)
	}
	if len(claimed) != 1 || claimed[0].DeviceID != "phone" {
		t.Fatalf("claimed = %+v, want bob's phone", claimed)
	}
	if string(claimed[0].KeyPackage) != "bob-phone-key-package" {
		t.Errorf("wrong key package bytes: %q", claimed[0].KeyPackage)
	}
}

// A user with nothing published yields a clean 404, not an error the caller has
// to distinguish from a transport failure.
func TestClaimForAnUnknownUserIsNotFound(t *testing.T) {
	aKey := hostKey(t, 1)
	aPub := aKey.Public().(ed25519.PublicKey)
	mux := http.NewServeMux()
	NewHandler("b.example", fakeLookup{"a.example": aPub}, nil).
		WithKeyPackages(fakeKeyPackages{}).Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := NewClient("a.example", "a-key-1", aKey)
	client.PeerURL = func(string) string { return srv.URL }

	if _, err := client.ClaimRemoteKeyPackages(context.Background(), "b.example", "ghost"); err == nil {
		t.Fatal("claiming for an unknown user succeeded")
	}
}

// A host not on B's nodelist cannot claim, however well-formed the request —
// key packages are handed only to trusted peers.
func TestClaimFromAnUntrustedHostIsRejected(t *testing.T) {
	stranger := hostKey(t, 9)
	mux := http.NewServeMux()
	NewHandler("b.example", fakeLookup{}, nil).
		WithKeyPackages(fakeKeyPackages{"bob": {"phone": []byte("x")}}).Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := NewClient("c.example", "c-key", stranger)
	client.PeerURL = func(string) string { return srv.URL }

	if _, err := client.ClaimRemoteKeyPackages(context.Background(), "b.example", "bob"); err == nil {
		t.Fatal("an untrusted host claimed a key package")
	}
}
