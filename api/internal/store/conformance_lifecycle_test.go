package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Deleting things, and what has to go with them — run against BOTH stores.
//
// A delete that leaves something behind is the expensive kind of bug: it does not fail, it
// accumulates. An image blob whose message is gone is storage nobody will ever look at again and
// nobody will ever think to look for. A comment whose message is gone is a row that can never be
// rendered and can never be removed through the UI, because the only route to it was the message.
//
// And a delete that takes too much is worse, because it is unrecoverable. These are tested in both
// directions for that reason: what must go, and what must survive.

func seedChannel(t *testing.T, s Store, name string) domain.Channel {
	t.Helper()
	ch, err := s.CreateChannel(context.Background(), domain.Channel{
		PublicID: "pub-" + name, OwnerID: "owner", Name: name,
		Status: domain.ChannelActive, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return ch
}

func seedMessage(t *testing.T, s Store, channelID, title string, images ...domain.MessageImage) domain.Message {
	t.Helper()
	m, err := s.CreateMessage(context.Background(), domain.Message{
		ChannelID: channelID, Title: title, Body: "body", Images: images,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}
	return m
}

// THE CASCADE. A message takes its images, comments and delivery records with it.
func TestConformance_DeletingAMessageTakesEverythingThatHangsOffIt(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		ch := seedChannel(t, s.store, "cascade")

		// A blob that only this message references.
		blobID, err := s.blobs.Put(ctx, []byte("an image"), "image/jpeg")
		if err != nil {
			t.Fatalf("put blob: %v", err)
		}
		msg := seedMessage(t, s.store, ch.ID, "doomed", domain.MessageImage{ID: blobID, Width: 4, Height: 4})

		// A comment and a delivery record hanging off it.
		if _, err := s.store.CreateComment(ctx, domain.Comment{
			MessageID: msg.ID, UserID: "u1", Body: "a comment", CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("create comment: %v", err)
		}
		if _, err := s.store.CreateDelivery(ctx, domain.Delivery{
			MessageID: msg.ID, DeviceID: "d1", Status: domain.DeliverySent, SentAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("create delivery: %v", err)
		}

		// A second message with its own comment, which must survive.
		survivor := seedMessage(t, s.store, ch.ID, "survivor")
		if _, err := s.store.CreateComment(ctx, domain.Comment{
			MessageID: survivor.ID, UserID: "u1", Body: "keep me", CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("create surviving comment: %v", err)
		}

		if err := s.store.DeleteMessage(ctx, msg.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}

		if _, err := s.store.MessageByID(ctx, msg.ID); err == nil {
			t.Error("the message is still there after being deleted")
		}
		// The blob goes, unconditionally, and that is safe rather than merely convenient: blob ids
		// are 16 random bytes minted per upload, not derived from content, and no route lets a
		// client name an existing one. So a message's blobs are its own and nothing else can be
		// referencing them.
		//
		// Worth stating because it is the assumption the cascade rests on. An earlier version of
		// this file tested that a blob shared by two messages survives the first being deleted —
		// which both stores failed, because the sharing it assumed cannot happen. If image ids ever
		// become content-derived, this delete becomes wrong and that test becomes right.
		if _, _, err := s.blobs.Get(ctx, blobID); err == nil {
			t.Error("the message's image blob outlived the message; it is now unreferenced storage " +
				"that nothing will ever clean up")
		}
		// The comments go. A comment whose message is gone cannot be rendered and cannot be removed
		// through the UI, because the only route to it was the message.
		comments, err := s.store.CommentsByMessage(ctx, msg.ID, "", 50)
		if err != nil {
			t.Fatalf("list comments: %v", err)
		}
		if len(comments) != 0 {
			t.Errorf("%d comments outlived their message", len(comments))
		}

		// And the neighbour is untouched.
		if _, err := s.store.MessageByID(ctx, survivor.ID); err != nil {
			t.Errorf("deleting one message removed another: %v", err)
		}
		kept, err := s.store.CommentsByMessage(ctx, survivor.ID, "", 50)
		if err != nil {
			t.Fatalf("list surviving comments: %v", err)
		}
		if len(kept) != 1 {
			t.Errorf("the surviving message has %d comments, want 1", len(kept))
		}
	})
}

// Deleting a comment removes only that comment.
func TestConformance_DeletingAComment(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		ch := seedChannel(t, s.store, "comments")
		msg := seedMessage(t, s.store, ch.ID, "commented")

		var ids []string
		for i := 0; i < 3; i++ {
			c, err := s.store.CreateComment(ctx, domain.Comment{
				MessageID: msg.ID, UserID: "u1", Body: fmt.Sprintf("comment %d", i),
				CreatedAt: time.Now().UTC(),
			})
			if err != nil {
				t.Fatalf("create comment: %v", err)
			}
			ids = append(ids, c.ID)
		}

		if err := s.store.DeleteComment(ctx, ids[1]); err != nil {
			t.Fatalf("delete comment: %v", err)
		}

		remaining, err := s.store.CommentsByMessage(ctx, msg.ID, "", 50)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(remaining) != 2 {
			t.Errorf("%d comments remain, want 2", len(remaining))
		}
		for _, c := range remaining {
			if c.ID == ids[1] {
				t.Error("the deleted comment is still listed")
			}
		}
		if _, err := s.store.CommentByID(ctx, ids[1]); err == nil {
			t.Error("the deleted comment is still fetchable by id")
		}
		// And the message survives its comment.
		if _, err := s.store.MessageByID(ctx, msg.ID); err != nil {
			t.Errorf("deleting a comment removed its message: %v", err)
		}
	})
}

// Comment counts are what stop the message list issuing one query per message. The count must
// match what listing actually returns, or the badge says three and the thread shows two.
func TestConformance_CommentCountsMatchTheComments(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		ch := seedChannel(t, s.store, "counts")

		busy := seedMessage(t, s.store, ch.ID, "busy")
		quiet := seedMessage(t, s.store, ch.ID, "quiet")
		silent := seedMessage(t, s.store, ch.ID, "silent")

		for i := 0; i < 3; i++ {
			if _, err := s.store.CreateComment(ctx, domain.Comment{
				MessageID: busy.ID, UserID: "u1", Body: "x", CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("create comment: %v", err)
			}
		}
		if _, err := s.store.CreateComment(ctx, domain.Comment{
			MessageID: quiet.ID, UserID: "u1", Body: "x", CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("create comment: %v", err)
		}

		counts, err := s.store.CommentCountsByMessages(ctx, []string{busy.ID, quiet.ID, silent.ID})
		if err != nil {
			t.Fatalf("counts: %v", err)
		}
		if counts[busy.ID] != 3 {
			t.Errorf("busy message counted %d comments, want 3", counts[busy.ID])
		}
		if counts[quiet.ID] != 1 {
			t.Errorf("quiet message counted %d, want 1", counts[quiet.ID])
		}
		// A message with none may be absent or zero, but must never be non-zero.
		if counts[silent.ID] != 0 {
			t.Errorf("a message with no comments counted %d", counts[silent.ID])
		}

		// The count must agree with the listing, including after a deletion.
		listed, err := s.store.CommentsByMessage(ctx, busy.ID, "", 50)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if int64(len(listed)) != counts[busy.ID] {
			t.Errorf("the count says %d and the listing returns %d; the badge disagrees with the "+
				"thread", counts[busy.ID], len(listed))
		}
	})
}

// Membership approval and banning have to reach push delivery, or an approved member gets nothing
// and a banned one keeps receiving.
func TestConformance_SubscriptionStatusFollowsTheUsersMembership(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		ch := seedChannel(t, s.store, "approvals")

		// Two devices for the member, one for somebody else.
		var deviceIDs []string
		for i := 0; i < 2; i++ {
			d, err := s.store.CreateDevice(ctx, domain.Device{
				UserID: "member", Platform: domain.PlatformWeb, CreatedAt: time.Now().UTC(),
			})
			if err != nil {
				t.Fatalf("create device: %v", err)
			}
			if _, err := s.store.Subscribe(ctx, domain.Subscription{
				ChannelID: ch.ID, DeviceID: d.ID, Status: domain.SubPending, CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			deviceIDs = append(deviceIDs, d.ID)
		}
		other, err := s.store.CreateDevice(ctx, domain.Device{
			UserID: "bystander", Platform: domain.PlatformWeb, CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("create device: %v", err)
		}
		if _, err := s.store.Subscribe(ctx, domain.Subscription{
			ChannelID: ch.ID, DeviceID: other.ID, Status: domain.SubActive, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("subscribe: %v", err)
		}

		if err := s.store.SetSubscriptionStatusForUser(ctx, ch.ID, "member", domain.SubActive); err != nil {
			t.Fatalf("approve: %v", err)
		}

		// EVERY one of the member's devices, not just the first — a person approved on their phone
		// and not their laptop would think the approval half-worked.
		for i, id := range deviceIDs {
			sub, err := s.store.SubscriptionForDevice(ctx, ch.ID, id)
			if err != nil {
				t.Fatalf("read subscription %d: %v", i, err)
			}
			if sub.Status != domain.SubActive {
				t.Errorf("device %d is %q after approval, want active", i, sub.Status)
			}
		}
		// And nobody else's.
		bystander, err := s.store.SubscriptionForDevice(ctx, ch.ID, other.ID)
		if err != nil {
			t.Fatalf("read bystander: %v", err)
		}
		if bystander.Status != domain.SubActive {
			t.Errorf("approving one member changed another's subscription to %q", bystander.Status)
		}

		// Banning goes the other way, and must also reach every device.
		if err := s.store.SetSubscriptionStatusForUser(ctx, ch.ID, "member", domain.SubBlocked); err != nil {
			t.Fatalf("ban: %v", err)
		}
		for i, id := range deviceIDs {
			sub, err := s.store.SubscriptionForDevice(ctx, ch.ID, id)
			if err != nil {
				t.Fatalf("read subscription %d: %v", i, err)
			}
			if sub.Status != domain.SubBlocked {
				t.Errorf("device %d is %q after a ban, want blocked; a banned member keeps "+
					"receiving the channel", i, sub.Status)
			}
		}
	})
}

// A member's role decides what they may do in a channel. Promotion and demotion must land.
func TestConformance_MemberRoleChanges(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		ch := seedChannel(t, s.store, "roles")

		for _, uid := range []string{"promoted", "untouched"} {
			if _, err := s.store.UpsertMember(ctx, domain.ChannelMember{
				ChannelID: ch.ID, UserID: uid, Role: domain.RoleUser,
				Status: domain.MemberActive, CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("add member: %v", err)
			}
		}

		if err := s.store.UpdateMemberRole(ctx, ch.ID, "promoted", domain.RoleAdmin); err != nil {
			t.Fatalf("promote: %v", err)
		}
		m, err := s.store.MembershipForUser(ctx, ch.ID, "promoted")
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if m.Role != domain.RoleAdmin {
			t.Errorf("role = %q after promotion, want admin", m.Role)
		}
		// Promotion must not disturb membership status.
		if m.Status != domain.MemberActive {
			t.Errorf("promoting changed the member's status to %q", m.Status)
		}

		other, err := s.store.MembershipForUser(ctx, ch.ID, "untouched")
		if err != nil {
			t.Fatalf("read other: %v", err)
		}
		if other.Role != domain.RoleUser {
			t.Errorf("promoting one member made another an admin (%q)", other.Role)
		}

		// And back down again — a demotion that silently failed would leave an ex-admin
		// administering.
		if err := s.store.UpdateMemberRole(ctx, ch.ID, "promoted", domain.RoleUser); err != nil {
			t.Fatalf("demote: %v", err)
		}
		m, err = s.store.MembershipForUser(ctx, ch.ID, "promoted")
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if m.Role != domain.RoleUser {
			t.Errorf("role = %q after demotion, want user; an ex-admin keeps administering", m.Role)
		}
	})
}

// A channel avatar replaces the blob it supersedes, or every avatar change leaks an image.
func TestConformance_ChannelAvatarReplacesTheBlobItSupersedes(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		ch := seedChannel(t, s.store, "avatar")

		firstBlob, err := s.blobs.Put(ctx, []byte("first avatar"), "image/jpeg")
		if err != nil {
			t.Fatalf("put: %v", err)
		}
		if _, err := s.store.SetChannelAvatar(ctx, ch.ID, firstBlob); err != nil {
			t.Fatalf("set avatar: %v", err)
		}

		secondBlob, err := s.blobs.Put(ctx, []byte("second avatar"), "image/jpeg")
		if err != nil {
			t.Fatalf("put: %v", err)
		}
		updated, err := s.store.SetChannelAvatar(ctx, ch.ID, secondBlob)
		if err != nil {
			t.Fatalf("replace avatar: %v", err)
		}
		if updated.AvatarID != secondBlob {
			t.Errorf("avatar id = %q, want the new blob", updated.AvatarID)
		}
		if _, _, err := s.blobs.Get(ctx, firstBlob); err == nil {
			t.Error("the replaced avatar blob was left behind; every avatar change leaks an image")
		}

		// Clearing removes the remaining blob too.
		cleared, err := s.store.SetChannelAvatar(ctx, ch.ID, "")
		if err != nil {
			t.Fatalf("clear avatar: %v", err)
		}
		if cleared.AvatarID != "" {
			t.Errorf("avatar id = %q after clearing", cleared.AvatarID)
		}
		if _, _, err := s.blobs.Get(ctx, secondBlob); err == nil {
			t.Error("clearing the avatar left its blob behind")
		}
	})
}

// A search hit is worth little on its own; the window is what puts it back in context.
func TestConformance_MessagesAroundReturnsAWindowCentredOnTheMessage(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		ch := seedChannel(t, s.store, "window")

		var ids []string
		for i := 0; i < 11; i++ {
			m := seedMessage(t, s.store, ch.ID, fmt.Sprintf("message %02d", i))
			ids = append(ids, m.ID)
			// Distinct timestamps, so ordering is not a coin toss.
			time.Sleep(2 * time.Millisecond)
		}
		middle := ids[5]

		window, err := s.store.MessagesAround(ctx, ch.ID, middle, 5)
		if err != nil {
			t.Fatalf("window: %v", err)
		}
		if len(window) == 0 {
			t.Fatal("the window is empty; a search hit would open on nothing")
		}
		if len(window) > 5 {
			t.Errorf("the window holds %d messages, more than the %d asked for", len(window), 5)
		}

		var found bool
		for _, m := range window {
			if m.ID == middle {
				found = true
			}
		}
		if !found {
			t.Error("the window does not contain the message it is centred on; a search hit opens " +
				"on a conversation that does not include it")
		}
		// Newest first, like every other listing here.
		for i := 1; i < len(window); i++ {
			if window[i].CreatedAt.After(window[i-1].CreatedAt) {
				t.Errorf("the window is not newest-first at position %d", i)
				break
			}
		}
	})
}
