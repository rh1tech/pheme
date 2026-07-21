package chat_test

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/chat"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/federation"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/store"
)

// A cross-host conversation, end to end over the signed S2S transport: a group
// on hub A with a member on host B. A relays messages to B's mirror; B forwards
// its member's messages to A, which orders them and relays back. Nothing here is
// mocked but the two nodelists. The ciphertext is opaque bytes — the point is
// that the RELAY moves and orders them; it never reads them.
func TestCrossHostConversationRoundTrip(t *testing.T) {
	aKey := hostKey5d(1)
	bKey := hostKey5d(2)
	roster := fakeRoster{
		"a.example": aKey.Public().(ed25519.PublicKey),
		"b.example": bKey.Public().(ed25519.PublicKey),
	}

	aStore := store.NewMemory(nil)
	bStore := store.NewMemory(nil)
	aLive := live.NewMemoryBus()
	bLive := live.NewMemoryBus()

	// Clients resolve a peer to the other's httptest server.
	var aURL, bURL string
	aClient := federation.NewClient("a.example", "ak", aKey)
	aClient.PeerURL = func(d string) string { return map[string]string{"b.example": bURL}[d] }
	bClient := federation.NewClient("b.example", "bk", bKey)
	bClient.PeerURL = func(d string) string { return map[string]string{"a.example": aURL}[d] }

	aFed := &chat.ConvFederation{Store: aStore, Live: aLive, Peers: aClient, HostDomain: "a.example"}
	bFed := &chat.ConvFederation{Store: bStore, Live: bLive, Peers: bClient, HostDomain: "b.example"}

	aMux := http.NewServeMux()
	federation.NewHandler("a.example", roster, nil).WithConversations(aFed).Register(aMux)
	aSrv := httptest.NewServer(aMux)
	defer aSrv.Close()
	aURL = aSrv.URL

	bMux := http.NewServeMux()
	federation.NewHandler("b.example", roster, nil).WithConversations(bFed).Register(bMux)
	bSrv := httptest.NewServer(bMux)
	defer bSrv.Close()
	bURL = bSrv.URL

	ctx := context.Background()

	// Host A creates a native group conversation with alice (local).
	conv, err := aStore.CreateConversation(ctx, domain.Conversation{
		ID: "conv-1", Kind: domain.ConversationGroup, CreatedBy: "alice", CreatedAt: time.Now().UTC(),
	}, []domain.ConversationMember{{UserID: "alice", Role: domain.RoleAdmin, JoinedAt: time.Now().UTC()}})
	if err != nil {
		t.Fatal(err)
	}

	// alice adds bob@b.example: records the remote member and provisions B's mirror.
	if err := aFed.AddRemoteMember(ctx, conv, "bob", "b.example"); err != nil {
		t.Fatalf("add remote member: %v", err)
	}
	// B now mirrors the conversation with bob as a local member.
	mirror, err := bStore.ConversationByID(ctx, "conv-1")
	if err != nil {
		t.Fatalf("B has no mirror: %v", err)
	}
	if mirror.HubDomain != "a.example" || !mirror.IsMirror() {
		t.Errorf("mirror hub = %q, want a.example", mirror.HubDomain)
	}
	if m, err := bStore.ConversationMembership(ctx, "conv-1", "bob"); err != nil || m.Domain != "" {
		t.Errorf("bob should be a LOCAL member on B, got %+v (%v)", m, err)
	}

	// alice (on A) sends a message. It is appended on A and relayed to B's mirror.
	aConv, _ := aStore.ConversationByID(ctx, "conv-1")
	sent, err := aStore.AppendChatMessage(ctx, domain.ChatMessage{
		ConversationID: "conv-1", SenderID: "alice", Ciphertext: []byte("sealed-hello"),
		ContentType: "application/mls", CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	aFed.RelayAppended(ctx, aConv, "a.example", []domain.ChatMessage{sent})

	// B's mirror must now hold alice's message.
	bMsgs, err := bStore.ChatMessagesByConversation(ctx, "conv-1", "", 10, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasCiphertext(bMsgs, "sealed-hello") {
		t.Fatalf("B's mirror missing alice's relayed message, has %d", len(bMsgs))
	}

	// bob (on B) replies. B is a follower, so it forwards to the hub A, which
	// appends and relays back to B.
	if _, err := bFed.ForwardMessage(ctx, "a.example", federation.SubmittedMessage{
		ConversationID: "conv-1", SenderID: "bob", Ciphertext: []byte("sealed-reply"),
		ContentType: "application/mls",
	}); err != nil {
		t.Fatalf("bob forward: %v", err)
	}

	// A (the hub) must now hold bob's reply...
	aMsgs, _ := aStore.ChatMessagesByConversation(ctx, "conv-1", "", 10, time.Time{})
	if !hasCiphertext(aMsgs, "sealed-reply") {
		t.Fatal("hub A missing bob's forwarded reply")
	}
	// ...and it must have been relayed back to B's mirror too.
	bMsgs, _ = bStore.ChatMessagesByConversation(ctx, "conv-1", "", 10, time.Time{})
	if !hasCiphertext(bMsgs, "sealed-reply") {
		t.Fatal("bob's reply was not relayed back to B's mirror")
	}
}

// A commit forwarded by a follower is ordered by the hub and applied to both the
// hub and the follower's mirror, so their group state advances in lockstep.
func TestCrossHostCommitRoundTrip(t *testing.T) {
	aKey := hostKey5d(1)
	bKey := hostKey5d(2)
	roster := fakeRoster{
		"a.example": aKey.Public().(ed25519.PublicKey),
		"b.example": bKey.Public().(ed25519.PublicKey),
	}
	aStore := store.NewMemory(nil)
	bStore := store.NewMemory(nil)

	var aURL, bURL string
	aClient := federation.NewClient("a.example", "ak", aKey)
	aClient.PeerURL = func(d string) string { return map[string]string{"b.example": bURL}[d] }
	bClient := federation.NewClient("b.example", "bk", bKey)
	bClient.PeerURL = func(d string) string { return map[string]string{"a.example": aURL}[d] }

	aFed := &chat.ConvFederation{Store: aStore, Live: live.NewMemoryBus(), Peers: aClient, HostDomain: "a.example"}
	bFed := &chat.ConvFederation{Store: bStore, Live: live.NewMemoryBus(), Peers: bClient, HostDomain: "b.example"}

	aMux := http.NewServeMux()
	federation.NewHandler("a.example", roster, nil).WithConversations(aFed).Register(aMux)
	aSrv := httptest.NewServer(aMux)
	defer aSrv.Close()
	aURL = aSrv.URL
	bMux := http.NewServeMux()
	federation.NewHandler("b.example", roster, nil).WithConversations(bFed).Register(bMux)
	bSrv := httptest.NewServer(bMux)
	defer bSrv.Close()
	bURL = bSrv.URL

	ctx := context.Background()
	now := time.Now().UTC()
	conv, _ := aStore.CreateConversation(ctx, domain.Conversation{
		ID: "conv-c", Kind: domain.ConversationGroup, CreatedAt: now,
	}, []domain.ConversationMember{{UserID: "alice", Role: domain.RoleAdmin, JoinedAt: now}})
	if err := aFed.AddRemoteMember(ctx, conv, "bob", "b.example"); err != nil {
		t.Fatal(err)
	}

	// bob (on the mirror B) establishes the group at epoch 0→1, forwarding to the hub.
	res, err := bFed.ForwardCommit(ctx, "a.example", federation.SubmittedCommit{
		ConversationID: "conv-c", SenderID: "bob", GroupID: "grp-1", BaseEpoch: 0,
		Welcome: []byte("sealed-welcome"), Commit: []byte("sealed-commit"),
	})
	if err != nil {
		t.Fatalf("forward commit: %v", err)
	}
	if res.Status != "accepted" || res.Epoch != 1 {
		t.Fatalf("commit result = %+v, want accepted epoch 1", res)
	}

	// Both the hub and the mirror must now be at epoch 1 on the same group.
	aState, _ := aStore.MLSGroupState(ctx, "conv-c")
	bState, _ := bStore.MLSGroupState(ctx, "conv-c")
	if aState.GroupID != "grp-1" || aState.Epoch != 1 {
		t.Errorf("hub state = %+v, want grp-1 epoch 1", aState)
	}
	if bState.GroupID != "grp-1" || bState.Epoch != 1 {
		t.Errorf("mirror state = %+v, want grp-1 epoch 1", bState)
	}
	// The commit's control messages are logged on both sides (MLS protocol traffic
	// is kept out of the transcript, so read the control log directly).
	aMsgs, _ := aStore.MLSControlMessagesSince(ctx, "conv-c", "grp-1", 0)
	bMsgs, _ := bStore.MLSControlMessagesSince(ctx, "conv-c", "grp-1", 0)
	if !hasCiphertext(aMsgs, "sealed-commit") || !hasCiphertext(bMsgs, "sealed-commit") {
		t.Fatal("commit not logged on both hub and mirror")
	}

	// A second commit on the SAME base epoch is a conflict — the hub already advanced.
	res2, err := bFed.ForwardCommit(ctx, "a.example", federation.SubmittedCommit{
		ConversationID: "conv-c", SenderID: "bob", GroupID: "grp-1", BaseEpoch: 0,
		Commit: []byte("sealed-stale"),
	})
	if err != nil {
		t.Fatalf("forward stale commit: %v", err)
	}
	if res2.Status != "conflict" || res2.Epoch != 1 {
		t.Errorf("stale commit = %+v, want conflict at epoch 1", res2)
	}
}

// A host cannot forward a message on behalf of a user it does not home.
func TestHubRejectsForwardForANonLocalSender(t *testing.T) {
	aKey := hostKey5d(1)
	bKey := hostKey5d(2)
	roster := fakeRoster{
		"a.example": aKey.Public().(ed25519.PublicKey),
		"b.example": bKey.Public().(ed25519.PublicKey),
	}
	aStore := store.NewMemory(nil)
	aFed := &chat.ConvFederation{Store: aStore, Live: live.NewMemoryBus(), HostDomain: "a.example"}

	// A conversation where bob is homed at b.example.
	now := time.Now().UTC()
	_, _ = aStore.CreateConversation(context.Background(), domain.Conversation{
		ID: "conv-2", Kind: domain.ConversationGroup, CreatedAt: now,
	}, []domain.ConversationMember{
		{UserID: "alice", Role: domain.RoleAdmin, JoinedAt: now},
		{UserID: "bob", Domain: "b.example", Role: domain.RoleUser, JoinedAt: now},
	})

	aMux := http.NewServeMux()
	federation.NewHandler("a.example", roster, nil).WithConversations(aFed).Register(aMux)
	aSrv := httptest.NewServer(aMux)
	defer aSrv.Close()

	// c.example (a third host) tries to forward a message as bob — who is not homed there.
	cKey := hostKey5d(9)
	roster["c.example"] = cKey.Public().(ed25519.PublicKey)
	cClient := federation.NewClient("c.example", "ck", cKey)
	cClient.PeerURL = func(string) string { return aSrv.URL }

	_, err := cClient.SubmitMessageToHub(context.Background(), "a.example", federation.SubmittedMessage{
		ConversationID: "conv-2", SenderID: "bob", Ciphertext: []byte("forged"), ContentType: "application/mls",
	})
	if err == nil {
		t.Fatal("a host forwarded a message on behalf of a user it does not home")
	}
}

// --- helpers ---

func hostKey5d(seed byte) ed25519.PrivateKey {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = seed
	}
	return ed25519.NewKeyFromSeed(s)
}

type fakeRoster map[string]ed25519.PublicKey

func (f fakeRoster) KeyFor(domain string) (ed25519.PublicKey, error) { return f[domain], nil }

func hasCiphertext(msgs []domain.ChatMessage, want string) bool {
	for _, m := range msgs {
		if string(m.Ciphertext) == want {
			return true
		}
	}
	return false
}
