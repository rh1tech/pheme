package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// The everyday paths — users, channels, members, subscriptions, messages, comments — run against
// BOTH implementations, for the same reason as conformance_test.go: production is the Mongo one and
// almost none of it had ever been executed by a test.
//
// These assert behaviour rather than merely calling each method. A test that only checks a write
// does not return an error proves the database was reachable, not that it stored the right thing.

func mustChannel(t *testing.T, s Store, ownerID, publicID, name string) domain.Channel {
	t.Helper()
	ch, err := s.CreateChannel(context.Background(), domain.Channel{
		PublicID:         publicID,
		OwnerID:          ownerID,
		Name:             name,
		SubscriptionMode: domain.ModeOpen,
		Status:           domain.ChannelActive,
		CreatedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return ch
}

func TestConformance_UserLookups(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		u := mustUser(t, s.store, "lookup@pheme.test")

		byEmail, err := s.store.UserByEmail(ctx, "lookup@pheme.test")
		if err != nil || byEmail.ID != u.ID {
			t.Fatalf("UserByEmail = %v, %v", byEmail.ID, err)
		}
		// An address that was never registered is not found, rather than returning a zero user —
		// a zero user would authenticate as somebody.
		if _, err := s.store.UserByEmail(ctx, "nobody@pheme.test"); !errors.Is(err, ErrNotFound) {
			t.Errorf("UserByEmail for a stranger = %v, want ErrNotFound", err)
		}

		if _, err := s.store.UpdateUserProfile(ctx, u.ID, domain.UserProfileUpdate{
			Username: ptrTo("Ada"), DisplayName: ptrTo("Ada"),
		}); err != nil {
			t.Fatalf("set username: %v", err)
		}
		// Looked up by the LOWERCASED handle: that is the uniqueness key.
		byName, err := s.store.UserByUsername(ctx, "ada")
		if err != nil || byName.ID != u.ID {
			t.Errorf("UserByUsername(lowercased) = %v, %v", byName.ID, err)
		}

		other := mustUser(t, s.store, "lookup2@pheme.test")
		many, err := s.store.UsersByIDs(ctx, []string{u.ID, other.ID, "does-not-exist"})
		if err != nil {
			t.Fatalf("UsersByIDs: %v", err)
		}
		if len(many) != 2 {
			t.Errorf("UsersByIDs returned %d, want the 2 that exist", len(many))
		}
		// An unknown id is absent, not a zero value — a caller ranging over this must not render a
		// blank user as though it were real.
		if _, ok := many["does-not-exist"]; ok {
			t.Error("UsersByIDs invented an entry for an id that does not exist")
		}
	})
}

func TestConformance_UserAdminFields(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		u := mustUser(t, s.store, "admin-fields@pheme.test")

		if err := s.store.UpdateUserRole(ctx, u.ID, domain.RoleAdmin); err != nil {
			t.Fatalf("role: %v", err)
		}
		if err := s.store.UpdateUserStatus(ctx, u.ID, domain.UserBlocked); err != nil {
			t.Fatalf("status: %v", err)
		}
		if err := s.store.UpdateUserPassword(ctx, u.ID, "new-hash"); err != nil {
			t.Fatalf("password: %v", err)
		}

		got, err := s.store.UserByID(ctx, u.ID)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if got.Role != domain.RoleAdmin {
			t.Errorf("role = %q, want admin", got.Role)
		}
		if got.Status != domain.UserBlocked {
			t.Errorf("status = %q, want blocked", got.Status)
		}
		if got.PasswordHash != "new-hash" {
			t.Errorf("passwordHash was not updated")
		}

		avatared, err := s.store.SetUserAvatar(ctx, u.ID, "img-1")
		if err != nil {
			t.Fatalf("avatar: %v", err)
		}
		if avatared.AvatarID != "img-1" {
			t.Errorf("avatarId = %q, want img-1", avatared.AvatarID)
		}
	})
}

func TestConformance_UserSearchAndDelete(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		u := mustUser(t, s.store, "searchable@pheme.test")
		if _, err := s.store.UpdateUserProfile(ctx, u.ID, domain.UserProfileUpdate{
			DisplayName: ptrTo("Grace Hopper"), Username: ptrTo("grace"),
		}); err != nil {
			t.Fatalf("profile: %v", err)
		}

		found, err := s.store.SearchUsers(ctx, "grace", 10)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		var sawSearched bool
		for _, f := range found {
			if f.ID == u.ID {
				sawSearched = true
			}
			// An address is not a handle: search must never expose one.
			if f.Email != "" && f.ID != u.ID {
				t.Errorf("search returned an email address for another user")
			}
		}
		if !sawSearched {
			t.Error("searching for a username did not find the user who holds it")
		}

		all, err := s.store.ListUsers(ctx)
		if err != nil || len(all) == 0 {
			t.Fatalf("ListUsers = %d, %v", len(all), err)
		}

		if err := s.store.DeleteUser(ctx, u.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := s.store.UserByID(ctx, u.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("a deleted user is still readable: %v", err)
		}
	})
}

func TestConformance_ChannelLifecycle(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		owner := mustUser(t, s.store, "chan-owner@pheme.test")
		ch := mustChannel(t, s.store, owner.ID, "ch_life", "Life")

		byID, err := s.store.ChannelByID(ctx, ch.ID)
		if err != nil || byID.Name != "Life" {
			t.Fatalf("ChannelByID = %+v, %v", byID, err)
		}
		byPublic, err := s.store.ChannelByPublicID(ctx, "ch_life")
		if err != nil || byPublic.ID != ch.ID {
			t.Errorf("ChannelByPublicID = %v, %v", byPublic.ID, err)
		}

		aliased, err := s.store.SetChannelAlias(ctx, ch.ID, "LifeAlias")
		if err != nil {
			t.Fatalf("alias: %v", err)
		}
		if aliased.Alias == "" {
			t.Error("alias was not stored")
		}
		// Looked up case-insensitively, like a username.
		if got, err := s.store.ChannelByAlias(ctx, "lifealias"); err != nil || got.ID != ch.ID {
			t.Errorf("ChannelByAlias(lowercased) = %v, %v", got.ID, err)
		}

		owned, err := s.store.ChannelsByOwner(ctx, owner.ID)
		if err != nil || len(owned) != 1 {
			t.Errorf("ChannelsByOwner = %d, %v", len(owned), err)
		}

		updated, err := s.store.UpdateChannel(ctx, ch.ID, "Renamed", domain.ModeApproval)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if updated.Name != "Renamed" || updated.SubscriptionMode != domain.ModeApproval {
			t.Errorf("update did not apply: %+v", updated)
		}

		if _, err := s.store.UpdateChannelStatus(ctx, ch.ID, domain.ChannelDisabled); err != nil {
			t.Fatalf("status: %v", err)
		}
		if got, _ := s.store.ChannelByID(ctx, ch.ID); got.Status != domain.ChannelDisabled {
			t.Errorf("status = %q, want disabled", got.Status)
		}

		if err := s.store.DeleteChannel(ctx, ch.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := s.store.ChannelByID(ctx, ch.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("a deleted channel is still readable: %v", err)
		}
	})
}

func TestConformance_ChannelMembership(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		owner := mustUser(t, s.store, "mem-owner@pheme.test")
		member := mustUser(t, s.store, "mem-member@pheme.test")
		ch := mustChannel(t, s.store, owner.ID, "ch_mem", "Members")

		if _, err := s.store.UpsertMember(ctx, domain.ChannelMember{
			ChannelID: ch.ID, UserID: member.ID,
			Role: domain.RoleUser, Status: domain.MemberActive, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}

		got, err := s.store.MembershipForUser(ctx, ch.ID, member.ID)
		if err != nil || got.Status != domain.MemberActive {
			t.Fatalf("membership = %+v, %v", got, err)
		}

		list, total, err := s.store.ListMembers(ctx, ch.ID, domain.MemberActive, 0, 10)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if total < 1 || len(list) < 1 {
			t.Errorf("ListMembers = %d (total %d), want at least the one member", len(list), total)
		}

		if err := s.store.UpdateMemberStatus(ctx, ch.ID, member.ID, domain.MemberBlocked); err != nil {
			t.Fatalf("status: %v", err)
		}
		if got, _ := s.store.MembershipForUser(ctx, ch.ID, member.ID); got.Status != domain.MemberBlocked {
			t.Errorf("status = %q, want banned", got.Status)
		}
		// A banned member must not appear among the active ones, or a block is cosmetic.
		active, _, err := s.store.ListMembers(ctx, ch.ID, domain.MemberActive, 0, 10)
		if err != nil {
			t.Fatalf("list active: %v", err)
		}
		for _, m := range active {
			if m.UserID == member.ID {
				t.Error("a blocked member is still listed as active")
			}
		}

		if err := s.store.UpdateMemberStatus(ctx, ch.ID, member.ID, domain.MemberActive); err != nil {
			t.Fatalf("restore: %v", err)
		}
		joined, err := s.store.ChannelsForMember(ctx, member.ID)
		if err != nil || len(joined) != 1 {
			t.Errorf("ChannelsForMember = %d, %v", len(joined), err)
		}

		if err := s.store.RemoveMember(ctx, ch.ID, member.ID); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if _, err := s.store.MembershipForUser(ctx, ch.ID, member.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("a removed member still has a membership: %v", err)
		}
	})
}

func TestConformance_SubscriptionsAndDelivery(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		owner := mustUser(t, s.store, "sub-owner@pheme.test")
		ch := mustChannel(t, s.store, owner.ID, "ch_sub", "Subs")

		dev, err := s.store.CreateDevice(ctx, domain.Device{
			UserID: owner.ID, Platform: domain.PlatformAndroid, FCMToken: "sub-token",
		})
		if err != nil {
			t.Fatalf("device: %v", err)
		}

		if _, err := s.store.Subscribe(ctx, domain.Subscription{
			ChannelID: ch.ID, DeviceID: dev.ID,
			Status: domain.SubActive, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("subscribe: %v", err)
		}

		if got, err := s.store.SubscriptionForDevice(ctx, ch.ID, dev.ID); err != nil || got.DeviceID != dev.ID {
			t.Fatalf("SubscriptionForDevice = %+v, %v", got, err)
		}

		devices, err := s.store.ActiveDevicesForChannel(ctx, ch.ID)
		if err != nil || len(devices) != 1 {
			t.Fatalf("ActiveDevicesForChannel = %d, %v", len(devices), err)
		}

		if err := s.store.Unsubscribe(ctx, ch.ID, dev.ID); err != nil {
			t.Fatalf("unsubscribe: %v", err)
		}
		// An unsubscribed device must stop being a delivery target at once — this is the same rule
		// as a removed member no longer being notified.
		after, err := s.store.ActiveDevicesForChannel(ctx, ch.ID)
		if err != nil {
			t.Fatalf("devices after: %v", err)
		}
		if len(after) != 0 {
			t.Errorf("an unsubscribed device is still an active delivery target (%d remain)", len(after))
		}
	})
}

func TestConformance_MessagesAndComments(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		owner := mustUser(t, s.store, "msg-owner@pheme.test")
		ch := mustChannel(t, s.store, owner.ID, "ch_msg", "Messages")

		var last domain.Message
		for _, title := range []string{"first", "second", "third"} {
			m, err := s.store.CreateMessage(ctx, domain.Message{
				ChannelID: ch.ID, Title: title, Body: title + " body",
				CommentsAllowed: true, CreatedAt: time.Now().UTC(),
			})
			if err != nil {
				t.Fatalf("create %s: %v", title, err)
			}
			last = m
			time.Sleep(2 * time.Millisecond) // distinct timestamps, so "latest" is well defined
		}

		byID, err := s.store.MessageByID(ctx, last.ID)
		if err != nil || byID.Title != "third" {
			t.Fatalf("MessageByID = %+v, %v", byID, err)
		}

		page, err := s.store.MessagesByChannel(ctx, ch.ID, "", "", 10)
		if err != nil || len(page) != 3 {
			t.Fatalf("MessagesByChannel = %d, %v", len(page), err)
		}
		// Newest first: the feed reads backwards in time.
		if page[0].Title != "third" {
			t.Errorf("first message of the page is %q, want the newest", page[0].Title)
		}

		// Search narrows it.
		found, err := s.store.MessagesByChannel(ctx, ch.ID, "", "second", 10)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(found) != 1 || found[0].Title != "second" {
			t.Errorf("searching for 'second' returned %d messages", len(found))
		}

		latest, err := s.store.LastMessagesByChannels(ctx, []string{ch.ID})
		if err != nil {
			t.Fatalf("LastMessagesByChannels: %v", err)
		}
		if latest[ch.ID].Title != "third" {
			t.Errorf("last message = %q, want the newest", latest[ch.ID].Title)
		}

		c, err := s.store.CreateComment(ctx, domain.Comment{
			MessageID: last.ID, UserID: owner.ID, Body: "a comment", CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("comment: %v", err)
		}
		gotComment, err := s.store.CommentByID(ctx, c.ID)
		if err != nil || gotComment.Body != "a comment" {
			t.Errorf("CommentByID = %+v, %v", gotComment, err)
		}
	})
}

func TestConformance_APIKeys(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		owner := mustUser(t, s.store, "key-owner@pheme.test")
		ch := mustChannel(t, s.store, owner.ID, "ch_key", "Keys")

		k, err := s.store.CreateAPIKey(ctx, domain.APIKey{
			ChannelID: ch.ID, Label: "ci", HashedKey: "hashed", Prefix: "pk_abc",
			CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("create key: %v", err)
		}

		keys, err := s.store.APIKeysByChannel(ctx, ch.ID)
		if err != nil || len(keys) != 1 {
			t.Fatalf("APIKeysByChannel = %d, %v", len(keys), err)
		}
		// The secret itself is never stored, only its hash — a listing must never be able to hand
		// back a usable key.
		if keys[0].HashedKey == "" {
			t.Error("the stored key has no hash")
		}

		if err := s.store.RevokeAPIKey(ctx, k.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		after, err := s.store.APIKeysByChannel(ctx, ch.ID)
		if err != nil {
			t.Fatalf("list after revoke: %v", err)
		}
		for _, key := range after {
			if key.ID == k.ID && key.RevokedAt == nil {
				t.Error("a revoked key is still listed as live")
			}
		}
	})
}
