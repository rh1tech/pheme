package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/domain"
)

// TestDeleteChannelCascadesImageBlobs verifies that deleting a channel removes
// the image blobs of its messages via the injected blob store.
func TestDeleteChannelCascadesImageBlobs(t *testing.T) {
	ctx := context.Background()
	blobs := blob.NewMemory()
	db := NewMemory(blobs)

	ch, err := db.CreateChannel(ctx, domain.Channel{
		PublicID: "ch_test", OwnerID: "u1", Name: "Photos",
		SubscriptionMode: domain.ModeOpen, Status: domain.ChannelActive, CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	id, err := blobs.Put(ctx, []byte("jpeg-bytes"), "image/jpeg")
	if err != nil {
		t.Fatalf("blob Put: %v", err)
	}
	if _, err := db.CreateMessage(ctx, domain.Message{
		ChannelID: ch.ID,
		Title:     "hi",
		Images:    []domain.MessageImage{{ID: id, Width: 800, Height: 600}},
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	if err := db.DeleteChannel(ctx, ch.ID); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}

	if _, _, err := blobs.Get(ctx, id); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("blob still present after channel delete (err=%v)", err)
	}
}

// TestMemberCascades verifies that deleting a channel removes its member rows,
// and deleting a user removes that user's memberships across all channels —
// including channels they do not own.
func TestMemberCascades(t *testing.T) {
	ctx := context.Background()
	db := NewMemory(nil)

	owner, _ := db.CreateUser(ctx, domain.User{Email: "o@x.com", Role: domain.RoleUser, Status: domain.UserActive, CreatedAt: time.Now()})
	joiner, _ := db.CreateUser(ctx, domain.User{Email: "j@x.com", Role: domain.RoleUser, Status: domain.UserActive, CreatedAt: time.Now()})
	ch, _ := db.CreateChannel(ctx, domain.Channel{
		PublicID: "ch_m", OwnerID: owner.ID, Name: "M",
		SubscriptionMode: domain.ModeOpen, Status: domain.ChannelActive, CreatedAt: time.Now(),
	})
	if _, err := db.UpsertMember(ctx, domain.ChannelMember{
		ChannelID: ch.ID, UserID: joiner.ID, Role: domain.RoleUser, Status: domain.MemberActive, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}

	// Deleting the joining user removes their membership (in a channel they don't own).
	if err := db.DeleteUser(ctx, joiner.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := db.MembershipForUser(ctx, ch.ID, joiner.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("membership still present after user delete (err=%v)", err)
	}

	// Re-add a member, then delete the channel: the member row goes too.
	other, _ := db.CreateUser(ctx, domain.User{Email: "k@x.com", Role: domain.RoleUser, Status: domain.UserActive, CreatedAt: time.Now()})
	_, _ = db.UpsertMember(ctx, domain.ChannelMember{ChannelID: ch.ID, UserID: other.ID, Status: domain.MemberActive, CreatedAt: time.Now()})
	if err := db.DeleteChannel(ctx, ch.ID); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}
	if _, err := db.MembershipForUser(ctx, ch.ID, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("membership still present after channel delete (err=%v)", err)
	}
}

// TestCommentCascades verifies that deleting a channel removes its comments, and
// deleting a user removes that user's comments across all channels.
func TestCommentCascades(t *testing.T) {
	ctx := context.Background()
	db := NewMemory(nil)

	owner, _ := db.CreateUser(ctx, domain.User{Email: "o@x.com", Role: domain.RoleUser, Status: domain.UserActive, CreatedAt: time.Now()})
	author, _ := db.CreateUser(ctx, domain.User{Email: "a@x.com", Role: domain.RoleUser, Status: domain.UserActive, CreatedAt: time.Now()})
	ch, _ := db.CreateChannel(ctx, domain.Channel{PublicID: "ch_c", OwnerID: owner.ID, Name: "C", SubscriptionMode: domain.ModeOpen, Status: domain.ChannelActive, CreatedAt: time.Now()})
	msg, _ := db.CreateMessage(ctx, domain.Message{ChannelID: ch.ID, Title: "p", CommentsAllowed: true, CreatedAt: time.Now()})
	c1, _ := db.CreateComment(ctx, domain.Comment{MessageID: msg.ID, ChannelID: ch.ID, UserID: author.ID, Body: "one", CreatedAt: time.Now()})

	// Deleting the author removes their comment.
	if err := db.DeleteUser(ctx, author.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := db.CommentByID(ctx, c1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("comment present after author delete (err=%v)", err)
	}

	// A comment in the channel is removed when the channel is deleted.
	c2, _ := db.CreateComment(ctx, domain.Comment{MessageID: msg.ID, ChannelID: ch.ID, UserID: owner.ID, Body: "two", CreatedAt: time.Now()})
	if err := db.DeleteChannel(ctx, ch.ID); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}
	if _, err := db.CommentByID(ctx, c2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("comment present after channel delete (err=%v)", err)
	}
}

// TestDeleteUserCascadesAvatarBlob verifies the user's avatar blob is reclaimed
// when the account is deleted.
func TestDeleteUserCascadesAvatarBlob(t *testing.T) {
	ctx := context.Background()
	blobs := blob.NewMemory()
	db := NewMemory(blobs)

	u, _ := db.CreateUser(ctx, domain.User{Email: "av@x.com", Role: domain.RoleUser, Status: domain.UserActive, CreatedAt: time.Now()})
	id, _ := blobs.Put(ctx, []byte("jpeg"), "image/jpeg")
	if _, err := db.SetUserAvatar(ctx, u.ID, id); err != nil {
		t.Fatalf("SetUserAvatar: %v", err)
	}
	if err := db.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, _, err := blobs.Get(ctx, id); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("avatar blob still present after user delete (err=%v)", err)
	}
}

// TestUpdateUserProfileUsernameUniqueness verifies case-insensitive username
// uniqueness and that clearing a username frees it for reuse.
func TestUpdateUserProfileUsernameUniqueness(t *testing.T) {
	ctx := context.Background()
	db := NewMemory(nil)
	a, _ := db.CreateUser(ctx, domain.User{Email: "a@x.com", Role: domain.RoleUser, Status: domain.UserActive, CreatedAt: time.Now()})
	b, _ := db.CreateUser(ctx, domain.User{Email: "b@x.com", Role: domain.RoleUser, Status: domain.UserActive, CreatedAt: time.Now()})

	if _, err := db.UpdateUserProfile(ctx, a.ID, domain.UserProfileUpdate{Username: ptr("News")}); err != nil {
		t.Fatalf("set username: %v", err)
	}
	if got, _ := db.UserByUsername(ctx, "news"); got.ID != a.ID {
		t.Fatalf("UserByUsername(news) = %q, want %q", got.ID, a.ID)
	}
	if _, err := db.UpdateUserProfile(ctx, b.ID, domain.UserProfileUpdate{Username: ptr("news")}); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("duplicate username err = %v, want ErrUsernameTaken", err)
	}
	if _, err := db.UpdateUserProfile(ctx, a.ID, domain.UserProfileUpdate{Username: ptr("")}); err != nil {
		t.Fatalf("clear username: %v", err)
	}
	if _, err := db.UpdateUserProfile(ctx, b.ID, domain.UserProfileUpdate{Username: ptr("news")}); err != nil {
		t.Fatalf("reuse freed username: %v", err)
	}
}

// TestSetChannelAliasUniqueness verifies case-insensitive alias uniqueness and
// that clearing an alias frees it for reuse.
func TestSetChannelAliasUniqueness(t *testing.T) {
	ctx := context.Background()
	db := NewMemory(nil)
	a, _ := db.CreateChannel(ctx, domain.Channel{PublicID: "ch_a", OwnerID: "u1", Name: "A", SubscriptionMode: domain.ModeOpen, Status: domain.ChannelActive, CreatedAt: time.Now()})
	b, _ := db.CreateChannel(ctx, domain.Channel{PublicID: "ch_b", OwnerID: "u2", Name: "B", SubscriptionMode: domain.ModeOpen, Status: domain.ChannelActive, CreatedAt: time.Now()})

	if _, err := db.SetChannelAlias(ctx, a.ID, "News"); err != nil {
		t.Fatalf("set alias: %v", err)
	}
	if got, _ := db.ChannelByAlias(ctx, "news"); got.ID != a.ID {
		t.Fatalf("ChannelByAlias(news) = %q, want %q", got.ID, a.ID)
	}
	if _, err := db.SetChannelAlias(ctx, b.ID, "news"); !errors.Is(err, ErrAliasTaken) {
		t.Fatalf("duplicate alias err = %v, want ErrAliasTaken", err)
	}
	// Clearing A's alias frees it.
	if _, err := db.SetChannelAlias(ctx, a.ID, ""); err != nil {
		t.Fatalf("clear alias: %v", err)
	}
	if _, err := db.SetChannelAlias(ctx, b.ID, "news"); err != nil {
		t.Fatalf("reuse freed alias: %v", err)
	}
}

// TestDeleteUserCascadesImageBlobs verifies the same through the user-delete path
// (which deletes the user's channels and their messages' images).
func TestDeleteUserCascadesImageBlobs(t *testing.T) {
	ctx := context.Background()
	blobs := blob.NewMemory()
	db := NewMemory(blobs)

	u, err := db.CreateUser(ctx, domain.User{Email: "u@x.com", Role: domain.RoleUser, Status: domain.UserActive, CreatedAt: time.Now()})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	ch, err := db.CreateChannel(ctx, domain.Channel{
		PublicID: "ch_u", OwnerID: u.ID, Name: "Photos",
		SubscriptionMode: domain.ModeOpen, Status: domain.ChannelActive, CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	id, _ := blobs.Put(ctx, []byte("jpeg"), "image/jpeg")
	if _, err := db.CreateMessage(ctx, domain.Message{
		ChannelID: ch.ID,
		Images:    []domain.MessageImage{{ID: id}},
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	if err := db.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, _, err := blobs.Get(ctx, id); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("blob still present after user delete (err=%v)", err)
	}
}

// ptr makes an optional profile field explicit: see domain.UserProfileUpdate, where nil means
// "leave it alone" and a non-nil empty string means "clear it".
func ptr(v string) *string { return &v }
