package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Conversations, membership, cleared history and attachments — on both implementations.
//
// This is what a chat IS from the server's point of view: who is in it, what they can see, and what
// stops being visible when they clear it. The rules here decide whether a removed member keeps
// reading and whether "clear history" clears it for one person or for everybody, and until now they
// had only ever run against the in-memory store.

func directChat(t *testing.T, s Store, a, b string) domain.Conversation {
	t.Helper()
	conv, err := s.CreateConversation(context.Background(),
		domain.Conversation{
			Kind: domain.ConversationDirect, CreatedBy: a,
			DirectKey: a + ":" + b, CreatedAt: time.Now().UTC(),
		},
		[]domain.ConversationMember{
			{UserID: a, Role: domain.RoleAdmin, JoinedAt: time.Now().UTC()},
			{UserID: b, Role: domain.RoleUser, JoinedAt: time.Now().UTC()},
		},
	)
	if err != nil {
		t.Fatalf("create direct chat: %v", err)
	}
	return conv
}

// The direct key is what stops two people accumulating a new conversation every time either of
// them taps "message". Looking one up by it must find the existing chat.
func TestConformance_DirectChatIsFoundByItsKey(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		alice := mustUser(t, s.store, "dk-alice@pheme.test")
		bob := mustUser(t, s.store, "dk-bob@pheme.test")
		conv := directChat(t, s.store, alice.ID, bob.ID)

		got, err := s.store.ConversationByDirectKey(ctx, alice.ID+":"+bob.ID)
		if err != nil {
			t.Fatalf("by direct key: %v", err)
		}
		if got.ID != conv.ID {
			t.Errorf("found %q, want the existing chat %q", got.ID, conv.ID)
		}

		// A pair who have never spoken has none — and the caller must be told so rather than
		// handed a zero conversation it would then write messages into.
		if _, err := s.store.ConversationByDirectKey(ctx, "nobody:nobody"); !errors.Is(err, ErrNotFound) {
			t.Errorf("an unknown direct key returned %v, want ErrNotFound", err)
		}
	})
}

func TestConformance_ConversationsForUserListsOnlyTheirs(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		alice := mustUser(t, s.store, "cfu-alice@pheme.test")
		bob := mustUser(t, s.store, "cfu-bob@pheme.test")
		carol := mustUser(t, s.store, "cfu-carol@pheme.test")

		shared := directChat(t, s.store, alice.ID, bob.ID)
		_ = directChat(t, s.store, bob.ID, carol.ID)

		mine, err := s.store.ConversationsForUser(ctx, alice.ID)
		if err != nil {
			t.Fatalf("for user: %v", err)
		}
		if len(mine) != 1 || mine[0].ID != shared.ID {
			t.Fatalf("alice sees %d conversations, want only her own", len(mine))
		}

		// Bob is in both.
		his, err := s.store.ConversationsForUser(ctx, bob.ID)
		if err != nil {
			t.Fatalf("for user: %v", err)
		}
		if len(his) != 2 {
			t.Errorf("bob sees %d conversations, want 2", len(his))
		}

		// Somebody with none gets an empty list, not an error.
		none, err := s.store.ConversationsForUser(ctx, "stranger")
		if err != nil {
			t.Errorf("for a user with no conversations: %v", err)
		}
		if len(none) != 0 {
			t.Errorf("a stranger sees %d conversations", len(none))
		}
	})
}

func TestConformance_MembershipAddRemoveAndRole(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		owner := mustUser(t, s.store, "mem-owner@pheme.test")
		joiner := mustUser(t, s.store, "mem-joiner@pheme.test")
		conv := mustConversation(t, s.store, owner.ID)

		// Not a member yet: the check that guards every read of this conversation.
		if _, err := s.store.ConversationMembership(ctx, conv.ID, joiner.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("a non-member had a membership: %v", err)
		}

		if _, err := s.store.AddConversationMember(ctx, domain.ConversationMember{
			ConversationID: conv.ID, UserID: joiner.ID, Role: domain.RoleUser, JoinedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("add: %v", err)
		}
		if _, err := s.store.ConversationMembership(ctx, conv.ID, joiner.ID); err != nil {
			t.Fatalf("after adding: %v", err)
		}

		members, err := s.store.ConversationMembers(ctx, conv.ID)
		if err != nil || len(members) != 2 {
			t.Fatalf("members = %d, %v", len(members), err)
		}

		if err := s.store.SetConversationMemberRole(ctx, conv.ID, joiner.ID, domain.RoleAdmin); err != nil {
			t.Fatalf("set role: %v", err)
		}
		got, err := s.store.ConversationMembership(ctx, conv.ID, joiner.ID)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if got.Role != domain.RoleAdmin {
			t.Errorf("role = %q, want admin", got.Role)
		}

		if err := s.store.RemoveConversationMember(ctx, conv.ID, joiner.ID); err != nil {
			t.Fatalf("remove: %v", err)
		}
		// Removal is what cuts a person off. If the membership survived, every authorisation check
		// in the chat surface would still pass for them.
		if _, err := s.store.ConversationMembership(ctx, conv.ID, joiner.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("a removed member still has a membership: %v", err)
		}
		if convs, _ := s.store.ConversationsForUser(ctx, joiner.ID); len(convs) != 0 {
			t.Errorf("a removed member still lists the conversation")
		}
	})
}

// Clearing history is PER MEMBER. One person tidying their own view must not delete the
// conversation for everybody else — that would be data loss dressed up as a UI preference.
func TestConformance_ClearHistoryIsPerMember(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		alice := mustUser(t, s.store, "clr-alice@pheme.test")
		bob := mustUser(t, s.store, "clr-bob@pheme.test")
		conv := directChat(t, s.store, alice.ID, bob.ID)

		older := time.Now().UTC().Add(-time.Hour)
		for _, at := range []time.Time{older, older.Add(30 * time.Minute), time.Now().UTC()} {
			if _, err := s.store.AppendChatMessage(ctx, domain.ChatMessage{
				ConversationID: conv.ID, SenderID: alice.ID, Ciphertext: []byte("c"),
				ContentType: domain.ContentTypeMLSApplication, CreatedAt: at,
			}); err != nil {
				t.Fatalf("append: %v", err)
			}
		}

		// Alice clears everything up to half an hour ago.
		cutoff := older.Add(45 * time.Minute)
		if err := s.store.ClearConversationHistory(ctx, conv.ID, alice.ID, cutoff); err != nil {
			t.Fatalf("clear: %v", err)
		}

		aliceMembership, err := s.store.ConversationMembership(ctx, conv.ID, alice.ID)
		if err != nil {
			t.Fatalf("membership: %v", err)
		}
		aliceSees, err := s.store.ChatMessagesByConversation(ctx, conv.ID, "", 50, aliceMembership.ClearedAt)
		if err != nil {
			t.Fatalf("alice's view: %v", err)
		}
		if len(aliceSees) != 1 {
			t.Errorf("alice sees %d messages after clearing, want the 1 newer than her cutoff", len(aliceSees))
		}

		// Bob cleared nothing and must still see everything.
		bobMembership, err := s.store.ConversationMembership(ctx, conv.ID, bob.ID)
		if err != nil {
			t.Fatalf("bob's membership: %v", err)
		}
		bobSees, err := s.store.ChatMessagesByConversation(ctx, conv.ID, "", 50, bobMembership.ClearedAt)
		if err != nil {
			t.Fatalf("bob's view: %v", err)
		}
		if len(bobSees) != 3 {
			t.Errorf("bob sees %d messages, want all 3 — one member clearing their own view must "+
				"not delete the conversation for the other", len(bobSees))
		}
	})
}

// The conversation list is ordered by activity, and each row needs its last message. A list that
// showed the wrong "last message" would misrepresent every conversation at a glance.
func TestConformance_LastMessagePerConversation(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		alice := mustUser(t, s.store, "last-alice@pheme.test")
		bob := mustUser(t, s.store, "last-bob@pheme.test")
		first := directChat(t, s.store, alice.ID, bob.ID)
		second := mustConversation(t, s.store, alice.ID)

		add := func(conv, body string, at time.Time) {
			t.Helper()
			if _, err := s.store.AppendChatMessage(ctx, domain.ChatMessage{
				ConversationID: conv, SenderID: alice.ID, Ciphertext: []byte(body),
				ContentType: domain.ContentTypeMLSApplication, CreatedAt: at,
			}); err != nil {
				t.Fatalf("append: %v", err)
			}
		}
		base := time.Now().UTC().Add(-time.Hour)
		add(first.ID, "old", base)
		add(first.ID, "newest-in-first", base.Add(10*time.Minute))
		add(second.ID, "only-in-second", base.Add(5*time.Minute))

		got, err := s.store.LastChatMessagesByConversations(ctx, []string{first.ID, second.ID, "nope"})
		if err != nil {
			t.Fatalf("last messages: %v", err)
		}
		if string(got[first.ID].Ciphertext) != "newest-in-first" {
			t.Errorf("first conversation's last message = %q", got[first.ID].Ciphertext)
		}
		if string(got[second.ID].Ciphertext) != "only-in-second" {
			t.Errorf("second conversation's last message = %q", got[second.ID].Ciphertext)
		}
		// A conversation with no messages is absent, not a zero message a list would render as
		// an empty row.
		if _, ok := got["nope"]; ok {
			t.Error("a conversation that does not exist got a last message")
		}
	})
}

func TestConformance_AttachmentsRoundTripAndAreScoped(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		owner := mustUser(t, s.store, "att-owner@pheme.test")
		conv := mustConversation(t, s.store, owner.ID)
		other := mustConversation(t, s.store, owner.ID)

		for _, id := range []string{"blob-1", "blob-2"} {
			if err := s.store.CreateAttachment(ctx, domain.Attachment{
				ID: id, ConversationID: conv.ID, Size: 1234, CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("create %s: %v", id, err)
			}
		}
		if err := s.store.CreateAttachment(ctx, domain.Attachment{
			ID: "blob-elsewhere", ConversationID: other.ID, Size: 1, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("create in the other conversation: %v", err)
		}

		got, err := s.store.GetAttachment(ctx, "blob-1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.ConversationID != conv.ID || got.Size != 1234 {
			t.Errorf("attachment = %+v", got)
		}
		// Which conversation an attachment belongs to is the ONLY authorisation an encrypted
		// attachment has; without it, holding the id would be enough for anyone.
		if _, err := s.store.GetAttachment(ctx, "never-uploaded"); !errors.Is(err, ErrNotFound) {
			t.Errorf("an unknown attachment returned %v, want ErrNotFound", err)
		}

		ids, err := s.store.ListAttachmentIDs(ctx, conv.ID)
		if err != nil || len(ids) != 2 {
			t.Fatalf("ListAttachmentIDs = %v, %v", ids, err)
		}

		// Deleting a conversation's attachments must not touch another's.
		if err := s.store.DeleteAttachments(ctx, conv.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if ids, _ := s.store.ListAttachmentIDs(ctx, conv.ID); len(ids) != 0 {
			t.Errorf("%d attachments survived the delete", len(ids))
		}
		if ids, _ := s.store.ListAttachmentIDs(ctx, other.ID); len(ids) != 1 {
			t.Errorf("deleting one conversation's attachments removed another's")
		}
	})
}

// Deleting a conversation must take its membership with it, or its members keep listing a
// conversation that no longer exists and every open of it 404s.
func TestConformance_DeletingAConversationRemovesItsMembership(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		alice := mustUser(t, s.store, "del-alice@pheme.test")
		bob := mustUser(t, s.store, "del-bob@pheme.test")
		conv := directChat(t, s.store, alice.ID, bob.ID)

		if err := s.store.DeleteConversation(ctx, conv.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := s.store.ConversationByID(ctx, conv.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("a deleted conversation is still readable: %v", err)
		}
		for _, u := range []domain.User{alice, bob} {
			if convs, _ := s.store.ConversationsForUser(ctx, u.ID); len(convs) != 0 {
				t.Errorf("%s still lists a deleted conversation", u.Email)
			}
		}
	})
}
