package auth

import (
	"testing"
	"time"
)

func TestPasswordHashing(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil || !ok {
		t.Fatalf("verify valid: ok=%v err=%v", ok, err)
	}
	ok, _ = VerifyPassword("wrong password", hash)
	if ok {
		t.Fatal("verify should fail for wrong password")
	}
}

func TestTokenIssueParse(t *testing.T) {
	m := NewTokenManager("test-secret", time.Minute, time.Hour)
	access, refresh, err := m.Issue("user-123")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	uid, err := m.Parse(access, AccessToken)
	if err != nil || uid != "user-123" {
		t.Fatalf("parse access: uid=%q err=%v", uid, err)
	}

	uid, err = m.Parse(refresh, RefreshToken)
	if err != nil || uid != "user-123" {
		t.Fatalf("parse refresh: uid=%q err=%v", uid, err)
	}

	// Type confusion must be rejected.
	if _, err := m.Parse(access, RefreshToken); err == nil {
		t.Fatal("access token accepted as refresh")
	}

	// Wrong secret must be rejected.
	other := NewTokenManager("different-secret", time.Minute, time.Hour)
	if _, err := other.Parse(access, AccessToken); err == nil {
		t.Fatal("token validated with wrong secret")
	}
}

func TestExpiredToken(t *testing.T) {
	m := NewTokenManager("s", -time.Minute, time.Hour) // already expired access
	access, _, err := m.Issue("u")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := m.Parse(access, AccessToken); err == nil {
		t.Fatal("expired token accepted")
	}
}
