package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

// The rules that decide what appears on a locked phone.
//
// These are four small pure functions and they are the entire enforcement of a privacy setting the
// user chose. ShowsPreview in particular gates whether the encrypted message body is put into the
// push payload at all: get it wrong in the permissive direction and someone who asked for "sender
// only" has their messages rendered on a lock screen in a meeting.
//
// The empty value carries meaning and is the part most likely to be got wrong. It is not "unset, do
// something sensible" — it means "this account predates the setting", and such an account must fall
// back to the CONSERVATIVE choice rather than the one new accounts get. New accounts are given an
// explicit value at creation precisely so absent only ever means the one thing.

func TestPrivacyDefaultsToSenderOnlyForAccountsThatPredateTheSetting(t *testing.T) {
	var absent NotificationPrivacy // the zero value, as stored on an older account

	if got := absent.Effective(); got != NotificationPrivacySender {
		t.Errorf("an account with no setting resolves to %q, want %q — anyone who has not chosen "+
			"gets message content on their lock screen", got, NotificationPrivacySender)
	}
	if absent.ShowsPreview() {
		t.Error("an account with no setting shows message previews; nobody asked for that")
	}
	if !absent.ShowsSender() {
		t.Error("an account with no setting hides the sender too; the fallback is sender-only, " +
			"not silent")
	}
}

func TestPrivacyEachSettingShowsWhatItSays(t *testing.T) {
	for _, tc := range []struct {
		p            NotificationPrivacy
		showsSender  bool
		showsPreview bool
	}{
		{NotificationPrivacyPreview, true, true},
		{NotificationPrivacySender, true, false},
		{NotificationPrivacyGeneric, false, false},
	} {
		t.Run(string(tc.p), func(t *testing.T) {
			if got := tc.p.ShowsSender(); got != tc.showsSender {
				t.Errorf("ShowsSender = %v, want %v", got, tc.showsSender)
			}
			if got := tc.p.ShowsPreview(); got != tc.showsPreview {
				t.Errorf("ShowsPreview = %v, want %v", got, tc.showsPreview)
			}
			// Effective must not rewrite a setting somebody chose.
			if got := tc.p.Effective(); got != tc.p {
				t.Errorf("Effective changed an explicit setting to %q", got)
			}
		})
	}
}

// A preview necessarily shows who sent it, so there is no setting that previews the message while
// hiding the sender. A future value that got this backwards would be a privacy surprise.
func TestPrivacyAPreviewAlwaysShowsTheSender(t *testing.T) {
	for _, p := range []NotificationPrivacy{
		NotificationPrivacyPreview, NotificationPrivacySender, NotificationPrivacyGeneric, "",
	} {
		if p.ShowsPreview() && !p.ShowsSender() {
			t.Errorf("%q shows the message but claims to hide the sender", p)
		}
	}
}

// Only known values may be written. An unknown one must be refused at the HTTP boundary rather than
// persisted, or a future client could store a setting this server would read as something else.
func TestPrivacyValidAcceptsOnlyKnownValues(t *testing.T) {
	for _, p := range []NotificationPrivacy{
		NotificationPrivacyPreview, NotificationPrivacySender, NotificationPrivacyGeneric,
	} {
		if !p.Valid() {
			t.Errorf("%q is not accepted as input", p)
		}
	}
	for _, p := range []NotificationPrivacy{"", "PREVIEW", "preview ", "everything", "none", "true"} {
		if p.Valid() {
			t.Errorf("%q was accepted as input", p)
		}
	}
	// The empty value specifically: it is a legacy STORAGE state, not something a client may send.
	// A client that means "sender" has to say so.
	if NotificationPrivacy("").Valid() {
		t.Error("the empty value was accepted as input; a client could then write the state that " +
			"means 'predates the setting'")
	}
}

// New accounts are given an explicit value at creation. That is what keeps "absent" meaning exactly
// one thing — if a store forgot, absent would mean both "old account" and "new account" and the
// conservative fallback would be wrong for half of them.
func TestNewAccountsGetAnExplicitSetting(t *testing.T) {
	u := User{Email: "new@pheme.test"}.WithNewUserDefaults()

	if u.NotificationPrivacy == "" {
		t.Fatal("a new account was left with no setting; absent now means two different things")
	}
	if u.NotificationPrivacy != NotificationPrivacyPreview {
		t.Errorf("a new account got %q, want previews — that is what people expect of a messenger",
			u.NotificationPrivacy)
	}
	if !u.NotificationPrivacy.Valid() {
		t.Errorf("a new account was given %q, which a client is not allowed to send", u.NotificationPrivacy)
	}
}

// An existing choice must survive account updates that pass through the defaulting.
func TestDefaultingDoesNotOverwriteAChosenSetting(t *testing.T) {
	for _, chosen := range []NotificationPrivacy{
		NotificationPrivacyGeneric, NotificationPrivacySender, NotificationPrivacyPreview,
	} {
		u := User{Email: "x@pheme.test", NotificationPrivacy: chosen}.WithNewUserDefaults()
		if u.NotificationPrivacy != chosen {
			t.Errorf("a chosen setting of %q became %q", chosen, u.NotificationPrivacy)
		}
	}
}

// The public projection must never carry the email. This is the type handed to other members —
// comment authors, chat participants — and the email is the login credential everywhere else.
func TestPublicUserNeverCarriesTheEmail(t *testing.T) {
	u := User{
		ID: "u1", Email: "private@pheme.test", Username: "publicname",
		DisplayName: "Public Name", AvatarID: "img1",
		PasswordHash: "$argon2id$v=19$m=65536,t=1,p=4$c2FsdA$aGFzaA",
	}

	// Asserted on the serialised form, because that is what actually reaches another user, and a
	// struct field added later without a json tag would still be marshalled.
	encoded, err := json.Marshal(u.Public())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := strings.ToLower(string(encoded))
	if strings.Contains(body, "private@pheme.test") || strings.Contains(body, "email") {
		t.Errorf("the public view of a user carries their email: %s", encoded)
	}
	if strings.Contains(body, "argon2") || strings.Contains(body, "passwordhash") {
		t.Errorf("the public view of a user carries their password hash: %s", encoded)
	}

	pub := u.Public()
	if pub.ID != "u1" || pub.Username != "publicname" || pub.DisplayName != "Public Name" || pub.AvatarID != "img1" {
		t.Errorf("the public view lost something it should keep: %+v", pub)
	}
}
