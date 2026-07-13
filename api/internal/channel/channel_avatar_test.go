package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/store"
)

func TestChannelAvatarUploadServeReplaceAndClear(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "chav@b.com")
	ch := createChannel(t, f, token, "Alerts")

	body, ct := multipartAvatar(t, makePNG(t, 800, 600))
	rec := f.doRaw(http.MethodPost, "/v1/channels/"+ch.ID+"/avatar", token, ct, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var got domain.Channel
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.AvatarID == "" {
		t.Fatalf("expected avatarId set on the channel")
	}
	first := got.AvatarID

	// The channel avatar is served by the same public image endpoint as any other.
	if rec = f.do(http.MethodGet, "/v1/images/"+first, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("serve avatar status = %d, want 200", rec.Code)
	}

	// Replacing it drops the blob it replaced.
	body, ct = multipartAvatar(t, makePNG(t, 400, 400))
	rec = f.doRaw(http.MethodPost, "/v1/channels/"+ch.ID+"/avatar", token, ct, body)
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.AvatarID == "" || got.AvatarID == first {
		t.Fatalf("expected a new avatarId, got %q (was %q)", got.AvatarID, first)
	}
	if rec = f.do(http.MethodGet, "/v1/images/"+first, "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("replaced avatar blob still served: status = %d, want 404", rec.Code)
	}
	second := got.AvatarID

	// Clearing it removes the field and the blob. Decoded into a fresh value:
	// avatarId is omitempty, so a cleared avatar is an *absent* key, which would
	// leave a stale id behind in a reused struct.
	if rec = f.do(http.MethodDelete, "/v1/channels/"+ch.ID+"/avatar", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var cleared domain.Channel
	_ = json.Unmarshal(rec.Body.Bytes(), &cleared)
	if cleared.AvatarID != "" {
		t.Errorf("avatarId = %q after delete, want empty", cleared.AvatarID)
	}
	if rec = f.do(http.MethodGet, "/v1/images/"+second, "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("deleted avatar blob still served: status = %d, want 404", rec.Code)
	}
}

// A channel's avatar is part of its settings, so only its owner may change it.
func TestChannelAvatarRejectsNonOwner(t *testing.T) {
	f := newAppFixture(t)
	ownerToken, _ := f.tokenFor(t, "owner-av@b.com")
	ch := createChannel(t, f, ownerToken, "Alerts")

	otherToken, _ := f.tokenFor(t, "other-av@b.com")
	body, ct := multipartAvatar(t, makePNG(t, 100, 100))
	rec := f.doRaw(http.MethodPost, "/v1/channels/"+ch.ID+"/avatar", otherToken, ct, body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("upload as non-owner status = %d, want 403", rec.Code)
	}
	if rec = f.do(http.MethodDelete, "/v1/channels/"+ch.ID+"/avatar", otherToken, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("delete as non-owner status = %d, want 403", rec.Code)
	}
}

// Deleting a channel must not leave its avatar blob behind.
func TestDeleteChannelRemovesAvatarBlob(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "cascade-av@b.com")
	ch := createChannel(t, f, token, "Alerts")

	body, ct := multipartAvatar(t, makePNG(t, 300, 300))
	rec := f.doRaw(http.MethodPost, "/v1/channels/"+ch.ID+"/avatar", token, ct, body)
	var got domain.Channel
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.AvatarID == "" {
		t.Fatalf("expected avatarId set")
	}

	if rec = f.do(http.MethodDelete, "/v1/channels/"+ch.ID, token, nil); rec.Code != http.StatusNoContent &&
		rec.Code != http.StatusOK {
		t.Fatalf("delete channel status = %d; body=%s", rec.Code, rec.Body)
	}
	if rec = f.do(http.MethodGet, "/v1/images/"+got.AvatarID, "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("avatar blob outlived its channel: status = %d, want 404", rec.Code)
	}
}

// The chat list renders each channel's avatar, so the id must survive the trip.
func TestChannelListCarriesAvatarID(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "list-av@b.com")
	ch := createChannel(t, f, token, "Alerts")

	if _, err := f.store.SetChannelAvatar(context.Background(), ch.ID, "blob-123"); err != nil {
		t.Fatalf("set avatar: %v", err)
	}

	rec := f.do(http.MethodGet, "/v1/channels", token, nil)
	var list struct {
		Channels []channelView `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list.Channels) != 1 || list.Channels[0].AvatarID != "blob-123" {
		t.Fatalf("channel list lost the avatarId: %+v", list.Channels)
	}
}

var _ store.Store = (*store.Memory)(nil)
