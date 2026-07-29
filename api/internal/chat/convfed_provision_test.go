package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rh1tech/pheme/api/internal/chat"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/federation"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/store"
)

// mirrorHost is a receiving host with one local user, "bob".
func mirrorHost(t *testing.T) (*chat.ConvFederation, store.Store) {
	t.Helper()
	st := store.NewMemory(nil)
	seedLocalUser(t, st, "bob")
	return &chat.ConvFederation{
		Store: st, Live: live.NewMemoryBus(), HostDomain: "b.example",
	}, st
}

// goodSpec is a provision request a legitimate hub would send.
func goodSpec() federation.MirrorSpec {
	return federation.MirrorSpec{
		ConversationID: "conv-1",
		Kind:           string(domain.ConversationGroup),
		Title:          "Project",
		LocalUserID:    "bob",
		RemoteMembers: []federation.RemoteMember{
			{UserID: "alice", Domain: "a.example", DisplayName: "Alice", Username: "alice"},
		},
	}
}

func TestAValidProvisionIsAccepted(t *testing.T) {
	fed, st := mirrorHost(t)
	if err := fed.ProvisionMirror(context.Background(), "a.example", goodSpec()); err != nil {
		t.Fatalf("a legitimate hub was refused: %v", err)
	}
	if _, err := st.ConversationByID(context.Background(), "conv-1"); err != nil {
		t.Fatalf("the mirror was not created: %v", err)
	}
}

// The finding: ProvisionMirror had NO authorization. Any nodelist peer could
// create a conversation in any local user's list, with plaintext it chose.
func TestProvisioningIsRefusedWithoutAStake(t *testing.T) {
	cases := []struct {
		name string
		hub  string
		spec func(federation.MirrorSpec) federation.MirrorSpec
	}{
		{
			// A host with no member in the conversation has no business creating
			// it on someone else's server.
			name: "the calling hub has no member in the conversation",
			hub:  "evil.example",
			spec: func(s federation.MirrorSpec) federation.MirrorSpec { return s },
		},
		{
			name: "the subject is not a user here",
			hub:  "a.example",
			spec: func(s federation.MirrorSpec) federation.MirrorSpec {
				s.LocalUserID = "nobody-at-all"
				return s
			},
		},
		{
			// A peer asserting into this host's own namespace could shadow a
			// local member.
			name: "a remote member claims to live on the receiving host",
			hub:  "a.example",
			spec: func(s federation.MirrorSpec) federation.MirrorSpec {
				s.RemoteMembers = append(s.RemoteMembers,
					federation.RemoteMember{UserID: "mallory", Domain: "b.example"})
				return s
			},
		},
		{
			name: "a remote member claims the local user's id",
			hub:  "a.example",
			spec: func(s federation.MirrorSpec) federation.MirrorSpec {
				s.RemoteMembers = append(s.RemoteMembers,
					federation.RemoteMember{UserID: "bob", Domain: "a.example"})
				return s
			},
		},
		{
			// Title is unencrypted, attacker-chosen, and shown to the user: the
			// one plaintext delivery channel across the federation boundary.
			name: "an oversized title",
			hub:  "a.example",
			spec: func(s federation.MirrorSpec) federation.MirrorSpec {
				s.Title = strings.Repeat("A", 5000)
				return s
			},
		},
		{
			name: "an oversized member display name",
			hub:  "a.example",
			spec: func(s federation.MirrorSpec) federation.MirrorSpec {
				s.RemoteMembers[0].DisplayName = strings.Repeat("A", 5000)
				return s
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fed, st := mirrorHost(t)
			err := fed.ProvisionMirror(context.Background(), tc.hub, tc.spec(goodSpec()))
			if err == nil {
				t.Fatal("the provision was accepted")
			}
			if _, err := st.ConversationByID(context.Background(), "conv-1"); err == nil {
				t.Fatal("a refused provision still created a conversation")
			}
		})
	}
}

// A blocked account must not have conversations provisioned for it.
func TestProvisioningForABlockedUserIsRefused(t *testing.T) {
	st := store.NewMemory(nil)
	if _, err := st.CreateUser(context.Background(), domain.User{
		ID: "bob", Email: "bob@example.test", Username: "bob",
		Status: domain.UserBlocked, Role: domain.RoleUser,
	}); err != nil {
		t.Fatal(err)
	}
	fed := &chat.ConvFederation{Store: st, Live: live.NewMemoryBus(), HostDomain: "b.example"}

	if err := fed.ProvisionMirror(context.Background(), "a.example", goodSpec()); err == nil {
		t.Fatal("a mirror was provisioned for a blocked account")
	}
}

// Provisioning stays idempotent — a hub may legitimately retry.
func TestProvisioningTwiceIsANoOp(t *testing.T) {
	fed, _ := mirrorHost(t)
	ctx := context.Background()
	if err := fed.ProvisionMirror(ctx, "a.example", goodSpec()); err != nil {
		t.Fatal(err)
	}
	if err := fed.ProvisionMirror(ctx, "a.example", goodSpec()); err != nil {
		t.Fatalf("a retried provision was refused: %v", err)
	}
}
