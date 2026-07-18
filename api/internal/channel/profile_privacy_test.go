package channel

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
)

func decodeUser(t *testing.T, body []byte) domain.User {
	t.Helper()
	var u domain.User
	if err := json.Unmarshal(body, &u); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	return u
}

// The regression this setting is most likely to suffer, and the reason the request field is a
// pointer while every field beside it is a plain string.
//
// The other profile fields are cleared by omission — that is this endpoint's established contract.
// If the privacy setting followed that rule, "absent" would be indistinguishable from "sender",
// because sender IS the empty value. Every profile save from a client that predates the setting —
// an old mobile build, a stale browser tab — would then quietly switch a user's lock screen back on
// while they were editing their bio. They would never be told, and the only symptom would be their
// name appearing on a lock screen they had asked to keep bare.
func TestUpdateProfile_OmittedPrivacyIsUnchanged(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "priv-omit@pheme.test")

	rec := f.do(http.MethodPatch, "/v1/me", token, map[string]any{
		"notificationPrivacy": "generic",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("set privacy: got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := decodeUser(t, rec.Body.Bytes()).NotificationPrivacy; got != domain.NotificationPrivacyGeneric {
		t.Fatalf("privacy = %q, want generic", got)
	}

	// A profile save that says nothing about notifications — exactly what an older client sends.
	rec = f.do(http.MethodPatch, "/v1/me", token, map[string]any{
		"displayName": "Ada",
		"bio":         "mathematician",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update profile: got %d (%s)", rec.Code, rec.Body.String())
	}
	u := decodeUser(t, rec.Body.Bytes())
	if u.NotificationPrivacy != domain.NotificationPrivacyGeneric {
		t.Errorf("privacy = %q after an unrelated profile save, want it untouched at generic — "+
			"an older client must not be able to reset this by not knowing about it",
			u.NotificationPrivacy)
	}
	if u.DisplayName != "Ada" {
		t.Errorf("displayName = %q, want Ada", u.DisplayName)
	}
}

// Turning it back off has to work, which for this field means writing the zero value explicitly —
// the case a naive "omitempty everywhere" implementation silently drops.
func TestUpdateProfile_PrivacyCanBeSetBackToSender(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "priv-reset@pheme.test")

	f.do(http.MethodPatch, "/v1/me", token, map[string]any{"notificationPrivacy": "generic"})
	rec := f.do(http.MethodPatch, "/v1/me", token, map[string]any{"notificationPrivacy": "sender"})
	if rec.Code != http.StatusOK {
		t.Fatalf("reset privacy: got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := decodeUser(t, rec.Body.Bytes()).NotificationPrivacy; got != domain.NotificationPrivacySender {
		t.Errorf("privacy = %q, want the sender default", got)
	}
}

// An unrecognised value must be refused rather than stored. Stored, it would be read back as the
// zero value — the most revealing option — so a typo would quietly turn a lock screen on.
func TestUpdateProfile_RejectsUnknownPrivacy(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "priv-bad@pheme.test")

	rec := f.do(http.MethodPatch, "/v1/me", token, map[string]any{
		"notificationPrivacy": "shout-it-from-the-rooftops",
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400 for an unknown privacy setting (%s)", rec.Code, rec.Body.String())
	}
}
