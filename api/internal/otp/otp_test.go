package otp

import (
	"context"
	"testing"
	"time"
)

func TestGenerateCodeIsSixDigits(t *testing.T) {
	for i := 0; i < 100; i++ {
		code, err := GenerateCode()
		if err != nil {
			t.Fatalf("GenerateCode: %v", err)
		}
		if len(code) != 6 {
			t.Fatalf("code %q is not 6 chars", code)
		}
		for _, c := range code {
			if c < '0' || c > '9' {
				t.Fatalf("code %q has non-digit", code)
			}
		}
	}
}

func TestEqualCode(t *testing.T) {
	h := HashCode("123456")
	if !EqualCode("123456", h) {
		t.Fatal("expected matching code to be equal")
	}
	if EqualCode("000000", h) {
		t.Fatal("expected non-matching code to differ")
	}
}

func TestMemorySignupRoundTripAndExpiry(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	now := time.Unix(1000, 0)
	m.now = func() time.Time { return now }

	s := Signup{Email: "a@b.com", PasswordHash: "h", CodeHash: HashCode("111111")}
	if err := m.PutSignup(ctx, s, time.Minute); err != nil {
		t.Fatalf("PutSignup: %v", err)
	}
	got, err := m.GetSignup(ctx, "a@b.com")
	if err != nil {
		t.Fatalf("GetSignup: %v", err)
	}
	if got.PasswordHash != "h" || got.CodeHash != s.CodeHash {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// Advance past the TTL — the entry should be gone.
	now = now.Add(2 * time.Minute)
	if _, err := m.GetSignup(ctx, "a@b.com"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after expiry, got %v", err)
	}
}

func TestMemoryIncrAttempts(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	_ = m.PutSignup(ctx, Signup{Email: "a@b.com"}, time.Minute)

	for want := 1; want <= MaxAttempts; want++ {
		n, err := m.IncrSignupAttempts(ctx, "a@b.com")
		if err != nil {
			t.Fatalf("IncrSignupAttempts: %v", err)
		}
		if n != want {
			t.Fatalf("attempt count = %d, want %d", n, want)
		}
	}
	if _, err := m.IncrSignupAttempts(ctx, "missing@b.com"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for missing signup, got %v", err)
	}
}

func TestMemoryCooldown(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	now := time.Unix(1000, 0)
	m.now = func() time.Time { return now }

	ok, err := m.CooldownOK(ctx, "a@b.com", 2*time.Minute)
	if err != nil || !ok {
		t.Fatalf("first CooldownOK = %v, %v; want true, nil", ok, err)
	}
	// Immediately again: still cooling down.
	if ok, _ := m.CooldownOK(ctx, "a@b.com", 2*time.Minute); ok {
		t.Fatal("expected cooldown to block the second send")
	}
	// After the window passes, it should allow again.
	now = now.Add(3 * time.Minute)
	if ok, _ := m.CooldownOK(ctx, "a@b.com", 2*time.Minute); !ok {
		t.Fatal("expected cooldown to clear after the window")
	}
}

func TestMemoryResetRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	r := Reset{Email: "a@b.com", UserID: "u1", CodeHash: HashCode("222222")}
	if err := m.PutReset(ctx, r, time.Minute); err != nil {
		t.Fatalf("PutReset: %v", err)
	}
	got, err := m.GetReset(ctx, "a@b.com")
	if err != nil {
		t.Fatalf("GetReset: %v", err)
	}
	if got.UserID != "u1" {
		t.Fatalf("reset round-trip mismatch: %+v", got)
	}
	if err := m.DelReset(ctx, "a@b.com"); err != nil {
		t.Fatalf("DelReset: %v", err)
	}
	if _, err := m.GetReset(ctx, "a@b.com"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
