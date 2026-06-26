package auth

import (
	"errors"
	"testing"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name    string
		pw      string
		wantErr bool
	}{
		{"too short", "Ab1!", true},
		{"single class lowercase", "abcdefghij", true},
		{"single class digits", "12345678", true},
		{"common password", "password1", true},
		{"common mixed-case is lowercased", "Password1", true},
		{"letters plus digits", "abcd1234", false},
		{"letters plus symbol", "abcdefg!", false},
		{"strong", "Tr0ub4dour&3", false},
		{"exactly min length two classes", "abcdefg1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.pw)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for %q", tt.pw)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.pw, err)
			}
			if tt.wantErr && !errors.Is(err, ErrWeakPassword) {
				t.Fatalf("error should wrap ErrWeakPassword, got %v", err)
			}
		})
	}
}
