package channel

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// multipartAvatar builds a multipart/form-data body with a single "avatar" file
// part. It returns the body and content type.
func multipartAvatar(t *testing.T, png []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("avatar", "avatar.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(png); err != nil {
		t.Fatalf("write avatar: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

func TestGetMeReturnsOwnUser(t *testing.T) {
	f := newAppFixture(t)
	token, u := f.tokenFor(t, "me@b.com")
	rec := f.do(http.MethodGet, "/v1/me", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var got domain.User
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.ID != u.ID || got.Email != "me@b.com" {
		t.Fatalf("unexpected user: %+v", got)
	}
}

func TestUpdateProfileSetsUsernameAndFields(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "p@b.com")
	rec := f.do(http.MethodPatch, "/v1/me", token, map[string]any{
		"username": "Alice_99", "displayName": "Alice", "bio": "hi", "phone": "+1", "website": "https://a.dev",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var got domain.User
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Username != "Alice_99" || got.DisplayName != "Alice" || got.Website != "https://a.dev" {
		t.Fatalf("unexpected profile: %+v", got)
	}
	// Username may be cleared with an empty string. Decode into a fresh struct:
	// the cleared username is omitted from the response (omitempty), so reusing
	// a populated struct would keep the stale value.
	rec = f.do(http.MethodPatch, "/v1/me", token, map[string]any{"username": ""})
	var cleared domain.User
	_ = json.Unmarshal(rec.Body.Bytes(), &cleared)
	if cleared.Username != "" {
		t.Fatalf("expected cleared username, got %q", cleared.Username)
	}
}

func TestUsernameUniquenessCaseInsensitive(t *testing.T) {
	f := newAppFixture(t)
	a, _ := f.tokenFor(t, "a@b.com")
	b, _ := f.tokenFor(t, "b@b.com")
	if rec := f.do(http.MethodPatch, "/v1/me", a, map[string]any{"username": "taken"}); rec.Code != http.StatusOK {
		t.Fatalf("first set status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	rec := f.do(http.MethodPatch, "/v1/me", b, map[string]any{"username": "TAKEN"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate username status = %d, want 409; body=%s", rec.Code, rec.Body)
	}
}

func TestUsernameValidationRejected(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "v@b.com")
	for _, bad := range []string{"ab", "1abc", "no spaces", "way_too_long_username_exceeding_limit"} {
		rec := f.do(http.MethodPatch, "/v1/me", token, map[string]any{"username": bad})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("username %q status = %d, want 400", bad, rec.Code)
		}
	}
}

func TestProfileRejectsNonHTTPWebsite(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "w@b.com")
	rec := f.do(http.MethodPatch, "/v1/me", token, map[string]any{"displayName": "Ada", "website": "javascript:alert(1)"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("javascript website status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
	// A normal https URL is accepted.
	rec = f.do(http.MethodPatch, "/v1/me", token, map[string]any{"displayName": "Ada", "website": "https://example.com"})
	if rec.Code != http.StatusOK {
		t.Fatalf("https website status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
}

func TestAvatarUploadServeAndReplace(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "av@b.com")

	body, ct := multipartAvatar(t, makePNG(t, 800, 600))
	rec := f.doRaw(http.MethodPost, "/v1/me/avatar", token, ct, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var u domain.User
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	if u.AvatarID == "" {
		t.Fatalf("expected avatarId set")
	}
	first := u.AvatarID

	// The avatar is served by the public image endpoint.
	rec = f.do(http.MethodGet, "/v1/images/"+first, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("serve avatar status = %d, want 200", rec.Code)
	}

	// Replacing the avatar drops the previous blob.
	body, ct = multipartAvatar(t, makePNG(t, 400, 400))
	rec = f.doRaw(http.MethodPost, "/v1/me/avatar", token, ct, body)
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	if u.AvatarID == "" || u.AvatarID == first {
		t.Fatalf("expected a new avatarId, got %q (was %q)", u.AvatarID, first)
	}
	if rec := f.do(http.MethodGet, "/v1/images/"+first, "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("old avatar blob status = %d, want 404 (should be deleted)", rec.Code)
	}

	// Deleting clears the avatar and its blob. Decode into a fresh struct, since
	// the cleared avatarId is omitted from the response (omitempty).
	rec = f.do(http.MethodDelete, "/v1/me/avatar", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", rec.Code)
	}
	var afterDelete domain.User
	_ = json.Unmarshal(rec.Body.Bytes(), &afterDelete)
	if afterDelete.AvatarID != "" {
		t.Fatalf("expected cleared avatar, got %q", afterDelete.AvatarID)
	}
}

// An account has to be callable something. With both fields blank the clients have nothing to
// render but six characters of a database id — "User 3a7119" — and that is what everyone else in a
// conversation sees, indefinitely.
//
// The state was easy to reach without meaning to: the profile screen initialises its display-name
// field from an account that never had one, so saving a bio and nothing else wrote the name back as
// an empty string.
func TestProfileRejectsBlankNameAndUsername(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "nameless@example.com")

	rec := f.do(http.MethodPatch, "/v1/me", token, map[string]any{"displayName": "  ", "username": ""})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank name and username status = %d, want 400; body=%s", rec.Code, rec.Body)
	}

	// A username alone is a perfectly good identity — the display name is not the only way to be
	// nameable, and demanding both would be inventing a requirement.
	rec = f.do(http.MethodPatch, "/v1/me", token, map[string]any{"displayName": "", "username": "ada"})
	if rec.Code != http.StatusOK {
		t.Fatalf("username-only status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
}

// The bug that produced "User 3a7119" on a real account.
//
// The mobile settings screen saves the notification-privacy choice on its own, so PATCH /v1/me
// arrives carrying that one field. Every other profile field used to be cleared by omission, so
// turning message previews on silently erased the user's display name — and their bio, phone and
// website — and everyone they chatted with started seeing six characters of a database id.
func TestProfilePartialUpdateKeepsEverythingElse(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "partial@b.com")

	rec := f.do(http.MethodPatch, "/v1/me", token, map[string]any{
		"displayName": "Ada Lovelace",
		"bio":         "counting",
		"website":     "https://example.com",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("initial save status = %d; body=%s", rec.Code, rec.Body)
	}

	// Exactly what the settings screen sends: one field, nothing else.
	rec = f.do(http.MethodPatch, "/v1/me", token, map[string]any{"notificationPrivacy": "preview"})
	if rec.Code != http.StatusOK {
		t.Fatalf("privacy-only save status = %d; body=%s", rec.Code, rec.Body)
	}

	var u domain.User
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if u.DisplayName != "Ada Lovelace" {
		t.Errorf("displayName = %q after a privacy-only save, want it untouched", u.DisplayName)
	}
	if u.Bio != "counting" {
		t.Errorf("bio = %q after a privacy-only save, want it untouched", u.Bio)
	}
	if u.Website != "https://example.com" {
		t.Errorf("website = %q after a privacy-only save, want it untouched", u.Website)
	}
	if u.NotificationPrivacy != domain.NotificationPrivacyPreview {
		t.Errorf("notificationPrivacy = %q, want the change to have applied", u.NotificationPrivacy)
	}
}

// Clearing is still possible — it just has to be asked for.
func TestProfileCanStillClearAField(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "clearing@b.com")

	f.do(http.MethodPatch, "/v1/me", token, map[string]any{"displayName": "Ada", "bio": "counting"})
	rec := f.do(http.MethodPatch, "/v1/me", token, map[string]any{"bio": ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("clear bio status = %d; body=%s", rec.Code, rec.Body)
	}
	var u domain.User
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	if u.Bio != "" {
		t.Errorf("bio = %q, want it cleared when explicitly set to empty", u.Bio)
	}
	if u.DisplayName != "Ada" {
		t.Errorf("displayName = %q, want it untouched", u.DisplayName)
	}
}
