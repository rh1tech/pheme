package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/domain"
)

// One set of assertions, run against BOTH store implementations.
//
// Everything else in the suite tests the in-memory store, and production runs Mongo. The two are
// separate implementations of one interface, written by hand, and they have already disagreed: the
// profile update that blanked people's display names was a Mongo behaviour, and the memory store
// did something else — so a test could pass while the real thing lost data.
//
// A conformance suite is the only honest way to test two implementations of one contract. Where
// they are allowed to differ, that is stated; everywhere else, differing is the bug.
//
// The Mongo half is skipped unless PHEME_TEST_MONGO_URI is set, so `go test ./...` still works with
// nothing installed:
//
//	docker run -d --rm -p 27117:27017 mongo:7
//	PHEME_TEST_MONGO_URI=mongodb://localhost:27117 go test ./internal/store/

// storeUnderTest is one implementation, ready to use.
type storeUnderTest struct {
	name  string
	store Store
	blobs blob.Store
}

// eachStore runs fn against every implementation available in this environment.
func eachStore(t *testing.T, fn func(t *testing.T, s storeUnderTest)) {
	t.Helper()

	memBlobs := blob.NewMemory()
	t.Run("memory", func(t *testing.T) {
		fn(t, storeUnderTest{name: "memory", store: NewMemory(memBlobs), blobs: memBlobs})
	})

	uri := os.Getenv("PHEME_TEST_MONGO_URI")
	if uri == "" {
		t.Log("PHEME_TEST_MONGO_URI not set — skipping the implementation that runs in production")
		return
	}
	t.Run("mongo", func(t *testing.T) {
		ctx := context.Background()
		blobs := blob.NewMemory()
		// A database per test, dropped afterwards, so tests cannot see each other's data.
		db := "pheme_test_" + t.Name()
		db = db[:min(len(db), 60)]
		for i := 0; i < len(db); i++ {
			if db[i] == '/' || db[i] == ' ' || db[i] == '.' || db[i] == '$' {
				db = db[:i] + "_" + db[i+1:]
			}
		}
		m, err := NewMongo(ctx, uri, db, blobs)
		if err != nil {
			t.Fatalf("connect to mongo: %v", err)
		}
		// DISCONNECT, not just drop. Each subtest opens its own client, and a client holds a
		// connection pool — leaving them open accumulated one pool per test until mongod fell over
		// mid-run ("socket was unexpectedly closed: EOF", then thirty-second selection timeouts on
		// everything after it). It survived locally and died on a host that also runs prod and dev.
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = m.db.Drop(cleanup)
			_ = m.client.Disconnect(cleanup)
		})
		fn(t, storeUnderTest{name: "mongo", store: m, blobs: blobs})
	})
}

func mustUser(t *testing.T, s Store, email string) domain.User {
	t.Helper()
	u, err := s.CreateUser(context.Background(), domain.User{
		Email:     email,
		Status:    domain.UserActive,
		Role:      domain.RoleUser,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func ptrTo(v string) *string { return &v }

// The bug that reached production: a partial profile update must leave the fields it does not
// mention alone. Turning on message previews sent {"notificationPrivacy": …} and nothing else, and
// the display name, bio, phone and website went with it.
func TestConformance_PartialProfileUpdateLeavesOtherFieldsAlone(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		u := mustUser(t, s.store, "partial@pheme.test")

		if _, err := s.store.UpdateUserProfile(ctx, u.ID, domain.UserProfileUpdate{
			DisplayName: ptrTo("Ada Lovelace"),
			Bio:         ptrTo("counting"),
			Website:     ptrTo("https://example.com"),
		}); err != nil {
			t.Fatalf("initial update: %v", err)
		}

		privacy := domain.NotificationPrivacyPreview
		got, err := s.store.UpdateUserProfile(ctx, u.ID, domain.UserProfileUpdate{
			NotificationPrivacy: &privacy,
		})
		if err != nil {
			t.Fatalf("partial update: %v", err)
		}

		if got.DisplayName != "Ada Lovelace" {
			t.Errorf("displayName = %q after a privacy-only update, want it untouched", got.DisplayName)
		}
		if got.Bio != "counting" {
			t.Errorf("bio = %q, want it untouched", got.Bio)
		}
		if got.Website != "https://example.com" {
			t.Errorf("website = %q, want it untouched", got.Website)
		}
		if got.NotificationPrivacy != domain.NotificationPrivacyPreview {
			t.Errorf("notificationPrivacy = %q, want the change to have applied", got.NotificationPrivacy)
		}
	})
}

// Clearing is still possible; it just has to be asked for.
func TestConformance_ProfileFieldsCanBeCleared(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		u := mustUser(t, s.store, "clearing@pheme.test")

		if _, err := s.store.UpdateUserProfile(ctx, u.ID, domain.UserProfileUpdate{
			DisplayName: ptrTo("Ada"), Bio: ptrTo("counting"),
		}); err != nil {
			t.Fatalf("set: %v", err)
		}
		got, err := s.store.UpdateUserProfile(ctx, u.ID, domain.UserProfileUpdate{Bio: ptrTo("")})
		if err != nil {
			t.Fatalf("clear: %v", err)
		}
		if got.Bio != "" {
			t.Errorf("bio = %q, want it cleared when explicitly set to empty", got.Bio)
		}
		if got.DisplayName != "Ada" {
			t.Errorf("displayName = %q, want it untouched", got.DisplayName)
		}
	})
}

// A username is unique system-wide, case-insensitively, and clearing one frees it.
func TestConformance_UsernameUniqueness(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		a := mustUser(t, s.store, "uniq-a@pheme.test")
		b := mustUser(t, s.store, "uniq-b@pheme.test")

		if _, err := s.store.UpdateUserProfile(ctx, a.ID, domain.UserProfileUpdate{
			Username: ptrTo("News"), DisplayName: ptrTo("A"),
		}); err != nil {
			t.Fatalf("claim: %v", err)
		}
		// Different case, same handle.
		_, err := s.store.UpdateUserProfile(ctx, b.ID, domain.UserProfileUpdate{
			Username: ptrTo("news"), DisplayName: ptrTo("B"),
		})
		if !errors.Is(err, ErrUsernameTaken) {
			t.Errorf("second claim err = %v, want ErrUsernameTaken", err)
		}
		// Releasing it frees the handle for somebody else.
		if _, err := s.store.UpdateUserProfile(ctx, a.ID, domain.UserProfileUpdate{
			Username: ptrTo(""), DisplayName: ptrTo("A"),
		}); err != nil {
			t.Fatalf("release: %v", err)
		}
		if _, err := s.store.UpdateUserProfile(ctx, b.ID, domain.UserProfileUpdate{
			Username: ptrTo("news"), DisplayName: ptrTo("B"),
		}); err != nil {
			t.Errorf("claiming a released username failed: %v", err)
		}
	})
}

// Terminating a device must tombstone it rather than forget it — its absence was
// indistinguishable from a device that had never published, which is the case co-members
// deliberately leave alone.
func TestConformance_RevokedDevicesAreVisibleToCoMembers(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		u := mustUser(t, s.store, "revoked@pheme.test")

		for _, id := range []string{"dev-live", "dev-dead"} {
			if err := s.store.UpsertMLSDevice(ctx, domain.MLSDevice{
				UserID: u.ID, DeviceID: id, Label: id, CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("upsert %s: %v", id, err)
			}
		}
		if err := s.store.RevokeMLSDevice(ctx, u.ID, "dev-dead", time.Now().UTC()); err != nil {
			t.Fatalf("revoke: %v", err)
		}

		revoked, err := s.store.RevokedDeviceIDs(ctx, []string{u.ID})
		if err != nil {
			t.Fatalf("revoked: %v", err)
		}
		if len(revoked[u.ID]) != 1 || revoked[u.ID][0] != "dev-dead" {
			t.Errorf("revoked = %v, want exactly [dev-dead]", revoked[u.ID])
		}
	})
}

// A push address belongs to an MLS device, and terminating that device takes it away. Nothing used
// to delete a push row at all, so a revoked browser kept receiving messages.
func TestConformance_PushDevicesAreDeletedWithTheirMLSDevice(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		u := mustUser(t, s.store, "pushdel@pheme.test")

		for _, d := range []struct{ mls, token string }{
			{"dev-keep", "keep-token"},
			{"dev-gone", "gone-token"},
		} {
			if _, err := s.store.CreateDevice(ctx, domain.Device{
				UserID: u.ID, Platform: domain.PlatformAndroid,
				FCMToken: d.token, MLSDeviceID: d.mls,
			}); err != nil {
				t.Fatalf("create device: %v", err)
			}
		}

		removed, err := s.store.DeletePushDevicesForMLSDevice(ctx, u.ID, "dev-gone")
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		if removed != 1 {
			t.Errorf("removed %d push addresses, want 1", removed)
		}

		devices, err := s.store.DevicesForUsers(ctx, []string{u.ID})
		if err != nil {
			t.Fatalf("devices: %v", err)
		}
		for _, d := range devices {
			if d.MLSDeviceID == "dev-gone" {
				t.Error("a terminated device kept its push address")
			}
		}
		if len(devices) != 1 {
			t.Errorf("got %d devices, want the surviving one only", len(devices))
		}
	})
}

// A blank MLS device id must not match every legacy row and wipe the whole account's push.
func TestConformance_BlankMLSDeviceIDDeletesNothing(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		u := mustUser(t, s.store, "blankid@pheme.test")
		if _, err := s.store.CreateDevice(ctx, domain.Device{
			UserID: u.ID, Platform: domain.PlatformWeb, WebPushEndpoint: "https://push/x",
		}); err != nil {
			t.Fatalf("create: %v", err)
		}

		removed, err := s.store.DeletePushDevicesForMLSDevice(ctx, u.ID, "")
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		if removed != 0 {
			t.Errorf("a blank device id removed %d push addresses; it must match nothing", removed)
		}
	})
}

// Catch-up is scoped to ONE group. An epoch is unique only within a group, and a re-established
// conversation starts counting again — so without the scope the retired group's epoch 1 comes back
// alongside the live group's, in no defined order.
func TestConformance_ControlMessagesAreScopedToTheirGroup(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		owner := mustUser(t, s.store, "ctl-owner@pheme.test")
		conv, err := s.store.CreateConversation(ctx,
			domain.Conversation{Kind: domain.ConversationGroup, Title: "Scoped", CreatedBy: owner.ID, CreatedAt: time.Now().UTC()},
			[]domain.ConversationMember{{UserID: owner.ID, Role: domain.RoleAdmin, JoinedAt: time.Now().UTC()}},
		)
		if err != nil {
			t.Fatalf("create conversation: %v", err)
		}

		add := func(groupID string, epoch int64, contentType string) {
			t.Helper()
			if _, err := s.store.AppendChatMessage(ctx, domain.ChatMessage{
				ConversationID: conv.ID, SenderID: owner.ID,
				Ciphertext: []byte("x"), ContentType: contentType,
				MLSEpoch: epoch, MLSGroupID: groupID, CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("append: %v", err)
			}
		}
		// Two group lifetimes, sharing epoch numbers — exactly what a reset produces.
		add("retired", 1, domain.ContentTypeMLSCommit)
		add("retired", 2, domain.ContentTypeMLSCommit)
		add("live", 1, domain.ContentTypeMLSWelcome)
		add("live", 1, domain.ContentTypeMLSCommit)

		msgs, err := s.store.MLSControlMessagesSince(ctx, conv.ID, "live", 0)
		if err != nil {
			t.Fatalf("control messages: %v", err)
		}
		for _, m := range msgs {
			if m.MLSGroupID != "" && m.MLSGroupID != "live" {
				t.Errorf("catch-up returned a message for group %q at epoch %d", m.MLSGroupID, m.MLSEpoch)
			}
		}
		if len(msgs) != 2 {
			t.Errorf("got %d control messages, want the live group's 2", len(msgs))
		}
		// The Welcome must come first, or a device being admitted meets a Commit for a group it has
		// not joined.
		if len(msgs) == 2 && msgs[0].ContentType != domain.ContentTypeMLSWelcome {
			t.Errorf("first message is %q, want the Welcome", msgs[0].ContentType)
		}
	})
}

// Untagged control messages predate the group id and must still be returned, or every conversation
// older than that field could not catch up at all.
func TestConformance_UntaggedControlMessagesStillCatchUp(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		owner := mustUser(t, s.store, "untagged@pheme.test")
		conv, err := s.store.CreateConversation(ctx,
			domain.Conversation{Kind: domain.ConversationGroup, Title: "Legacy", CreatedBy: owner.ID, CreatedAt: time.Now().UTC()},
			[]domain.ConversationMember{{UserID: owner.ID, Role: domain.RoleAdmin, JoinedAt: time.Now().UTC()}},
		)
		if err != nil {
			t.Fatalf("create conversation: %v", err)
		}
		if _, err := s.store.AppendChatMessage(ctx, domain.ChatMessage{
			ConversationID: conv.ID, SenderID: owner.ID, Ciphertext: []byte("x"),
			ContentType: domain.ContentTypeMLSCommit, MLSEpoch: 1, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("append: %v", err)
		}

		msgs, err := s.store.MLSControlMessagesSince(ctx, conv.ID, "some-group", 0)
		if err != nil {
			t.Fatalf("control messages: %v", err)
		}
		if len(msgs) != 1 {
			t.Errorf("got %d, want the untagged message returned — a conversation that predates the "+
				"group id must still be able to catch up", len(msgs))
		}
	})
}

// The transcript is what people wrote. Protocol traffic is not part of it, and a history offer must
// be reachable through its own door instead.
func TestConformance_HistoryOffersAreOutOfTheTranscriptButFetchable(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		owner := mustUser(t, s.store, "hist@pheme.test")
		conv, err := s.store.CreateConversation(ctx,
			domain.Conversation{Kind: domain.ConversationGroup, Title: "Hist", CreatedBy: owner.ID, CreatedAt: time.Now().UTC()},
			[]domain.ConversationMember{{UserID: owner.ID, Role: domain.RoleAdmin, JoinedAt: time.Now().UTC()}},
		)
		if err != nil {
			t.Fatalf("create conversation: %v", err)
		}
		for _, ct := range []string{domain.ContentTypeMLSApplication, domain.ContentTypeMLSHistoryOffer} {
			if _, err := s.store.AppendChatMessage(ctx, domain.ChatMessage{
				ConversationID: conv.ID, SenderID: owner.ID, Ciphertext: []byte("x"),
				ContentType: ct, CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("append %s: %v", ct, err)
			}
		}

		transcript, err := s.store.ChatMessagesByConversation(ctx, conv.ID, "", 50, time.Time{})
		if err != nil {
			t.Fatalf("transcript: %v", err)
		}
		for _, m := range transcript {
			if m.ContentType == domain.ContentTypeMLSHistoryOffer {
				t.Error("a history offer appeared in the transcript")
			}
		}

		offers, err := s.store.MLSHistoryOffers(ctx, conv.ID, 20)
		if err != nil {
			t.Fatalf("offers: %v", err)
		}
		if len(offers) != 1 {
			t.Errorf("got %d offers, want 1 — a device that missed the live delivery must still find it",
				len(offers))
		}
	})
}

// Registering the same push address twice is one device, not two. Without this a phone accumulates
// a row per registration and every message fans out to all of them.
func TestConformance_PushDevicesDedupeByAddress(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		u := mustUser(t, s.store, "dedupe@pheme.test")

		for i := 0; i < 3; i++ {
			if _, err := s.store.CreateDevice(ctx, domain.Device{
				UserID: u.ID, Platform: domain.PlatformAndroid, FCMToken: "same-token",
			}); err != nil {
				t.Fatalf("create %d: %v", i, err)
			}
		}
		devices, err := s.store.DevicesForUsers(ctx, []string{u.ID})
		if err != nil {
			t.Fatalf("devices: %v", err)
		}
		if len(devices) != 1 {
			t.Errorf("registering one address three times produced %d devices, want 1", len(devices))
		}
	})
}

// A user's tokens can be refused wholesale, for the device whose session id was never recorded and
// which no per-session revocation can name.
func TestConformance_UserTokenRevocationRoundTrips(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		now := time.Now().UTC()

		if err := s.store.RevokeUserTokensBefore(ctx, "u1", now, now.Add(time.Hour)); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		active, err := s.store.ActiveUserRevocations(ctx, now)
		if err != nil {
			t.Fatalf("active: %v", err)
		}
		if got, ok := active["u1"]; !ok || got.Before(now.Add(-time.Second)) {
			t.Errorf("active = %v, want a cutoff at or after the revocation", active)
		}
		// Expired ones are not returned; the token is rejected on its own expiry by then.
		expired, err := s.store.ActiveUserRevocations(ctx, now.Add(2*time.Hour))
		if err != nil {
			t.Fatalf("active later: %v", err)
		}
		if _, ok := expired["u1"]; ok {
			t.Error("an expired revocation is still being reported as active")
		}
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
