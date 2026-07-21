package chat_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/calls"
	"github.com/rh1tech/pheme/api/internal/chat"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/federation"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/mlschain"
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
	if err := aFed.AddRemoteMember(ctx, conv, "bob", "b.example", "Bob", "bob"); err != nil {
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

	// Every message the mirror holds carries the hub's sequence number, not one the
	// mirror invented — same ciphertext, same seq on both hosts.
	hubSeq := seqOf(aMsgs, "sealed-hello")
	if hubSeq == 0 {
		t.Fatal("hub assigned no sequence to alice's message")
	}
	mMsgs, _ := bStore.ChatMessagesByConversation(ctx, "conv-1", "", 10, time.Time{})
	if got := seqOf(mMsgs, "sealed-hello"); got != hubSeq {
		t.Errorf("mirror seq for alice's message = %d, want the hub's %d", got, hubSeq)
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
	if err := aFed.AddRemoteMember(ctx, conv, "bob", "b.example", "Bob", "bob"); err != nil {
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

// Cross-host key-package claim: host A, adding a remote member, claims that
// member's device key packages from their home host B (the F5c consumption).
func TestCrossHostKeyPackageClaim(t *testing.T) {
	aKey := hostKey5d(1)
	bKey := hostKey5d(2)
	roster := fakeRoster{
		"a.example": aKey.Public().(ed25519.PublicKey),
		"b.example": bKey.Public().(ed25519.PublicKey),
	}
	aClient := federation.NewClient("a.example", "ak", aKey)
	var bURL string
	aClient.PeerURL = func(string) string { return bURL }
	aFed := &chat.ConvFederation{Store: store.NewMemory(nil), HostDomain: "a.example", Peers: aClient}

	// Host B serves its user bob's key packages over the F5c endpoint.
	bKPS := fakeKeyPackages{"bob": {"phone": []byte("bob-phone-kp"), "laptop": []byte("bob-laptop-kp")}}
	bMux := http.NewServeMux()
	federation.NewHandler("b.example", roster, nil).WithKeyPackages(bKPS).Register(bMux)
	bSrv := httptest.NewServer(bMux)
	defer bSrv.Close()
	bURL = bSrv.URL

	got, err := aFed.ClaimRemoteKeyPackages(context.Background(), "b.example", "bob")
	if err != nil {
		t.Fatalf("claim remote: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("claimed %d packages, want 2 (bob's two devices)", len(got))
	}
	byDevice := map[string]string{}
	for _, p := range got {
		byDevice[p.DeviceID] = string(p.KeyPackage)
	}
	if byDevice["phone"] != "bob-phone-kp" || byDevice["laptop"] != "bob-laptop-kp" {
		t.Errorf("claimed packages = %v, want bob's phone and laptop", byDevice)
	}
}

// fakeKeyPackages is a per-user, per-device key-package store for host B's F5c
// endpoint: userID -> deviceID -> package bytes.
type fakeKeyPackages map[string]map[string][]byte

func (f fakeKeyPackages) DevicesWithKeyPackages(_ context.Context, userID string) ([]string, error) {
	var ids []string
	for id := range f[userID] {
		ids = append(ids, id)
	}
	return ids, nil
}

func (f fakeKeyPackages) ClaimKeyPackage(_ context.Context, userID, deviceID string) ([]byte, error) {
	return f[userID][deviceID], nil
}

// Federated TURN: bob's host fetches alice's host's TURN for a shared call, so
// both ends can relay through a server each can reach. A host without a stake in
// the conversation gets nothing.
func TestCrossHostTurnCredentials(t *testing.T) {
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

	// Host A offers TURN; host B does not need to for this test.
	aFed := &chat.ConvFederation{Store: aStore, Live: live.NewMemoryBus(), Peers: aClient, HostDomain: "a.example",
		ICE: chat.ICEConfig{URLs: "stun:stun.a:3478,turn:turn.a:3478?transport=udp", Secret: "a-secret", TTL: time.Hour}}
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
		ID: "conv-turn", Kind: domain.ConversationGroup, CreatedAt: now,
	}, []domain.ConversationMember{{UserID: "alice", Role: domain.RoleAdmin, JoinedAt: now}})
	if err := aFed.AddRemoteMember(ctx, conv, "bob", "b.example", "Bob", "bob"); err != nil {
		t.Fatal(err)
	}

	// bob's host fetches A's TURN for the shared conversation.
	grants := bFed.RemoteTurn(ctx, "conv-turn")
	if len(grants) != 1 {
		t.Fatalf("got %d turn grants, want 1 from host A", len(grants))
	}
	g := grants[0]
	if len(g.URLs) != 1 || g.URLs[0] != "turn:turn.a:3478?transport=udp" {
		t.Errorf("turn urls = %v, want A's turn url only (stun excluded)", g.URLs)
	}
	if g.Username == "" || g.Credential == "" {
		t.Errorf("grant missing credential: %+v", g)
	}

	// A host with no member in the conversation is refused.
	if _, err := aFed.TurnCredentials(ctx, "c.example", "conv-turn"); err == nil {
		t.Error("host A minted TURN for a host with no member in the conversation")
	}
}

// Federated calls: a signal alice places on host A reaches bob's host B — it lands
// in B's own call mailbox (so bob's device fetches it in order) and rings bob.
func TestCrossHostCallSignalReachesCallee(t *testing.T) {
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

	bMailbox := calls.NewMemory()
	ringer := &recordingRinger{}
	aFed := &chat.ConvFederation{Store: aStore, Live: live.NewMemoryBus(), Peers: aClient, HostDomain: "a.example"}
	bFed := &chat.ConvFederation{Store: bStore, Live: live.NewMemoryBus(), Peers: bClient, HostDomain: "b.example", Mailbox: bMailbox, Ringer: ringer}

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
		ID: "conv-call", Kind: domain.ConversationGroup, CreatedAt: now,
	}, []domain.ConversationMember{{UserID: "alice", Role: domain.RoleAdmin, JoinedAt: now}})
	if err := aFed.AddRemoteMember(ctx, conv, "bob", "b.example", "Bob", "bob"); err != nil {
		t.Fatal(err)
	}

	// alice's host relays her ringing invite to every participant host.
	aFed.RelayCallSignal(ctx, federation.CallSignalRelay{
		ConversationID: "conv-call", CallID: "call-1", FromUserID: "alice",
		Ciphertext: []byte("sealed-sdp-offer"), Ring: true,
	})

	// B's mailbox must now hold alice's offer, in order, for bob to fetch.
	sigs, err := bMailbox.Since(ctx, "conv-call:call-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs) != 1 || string(sigs[0].Ciphertext) != "sealed-sdp-offer" {
		t.Fatalf("B's mailbox = %d signals, want alice's offer", len(sigs))
	}
	// bob was rung, on behalf of the remote caller.
	if ringer.calls != 1 || ringer.lastCaller != "alice" || ringer.lastCancel {
		t.Errorf("ringer = %d calls (caller %q, cancel %v), want 1 ring for alice", ringer.calls, ringer.lastCaller, ringer.lastCancel)
	}

	// A host may not place a call as a user it does not home.
	err = bFed.DeliverCallSignal(ctx, "c.example", federation.CallSignalRelay{
		ConversationID: "conv-call", CallID: "call-1", FromUserID: "alice", Ciphertext: []byte("forged"),
	})
	if err == nil {
		t.Error("a host delivered a call signal for a user it does not home")
	}
}

type recordingRinger struct {
	calls      int
	lastCaller string
	lastCancel bool
}

func (r *recordingRinger) RingForCall(_ context.Context, _, callerID, _ string, cancel bool) {
	r.calls++
	r.lastCaller = callerID
	r.lastCancel = cancel
}

// F6: a receipt reported on the follower reaches the hub, so a sender there sees
// the reader's ticks move. The watermark is a sequence number, so no clock skew
// between the two hosts can misplace it.
func TestCrossHostReceiptReachesHub(t *testing.T) {
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
		ID: "conv-r", Kind: domain.ConversationGroup, CreatedAt: now,
	}, []domain.ConversationMember{{UserID: "alice", Role: domain.RoleAdmin, JoinedAt: now}})
	if err := aFed.AddRemoteMember(ctx, conv, "bob", "b.example", "Bob", "bob"); err != nil {
		t.Fatal(err)
	}

	// bob, on his own host (the mirror), reports he has read up to sequence 5.
	mirror, _ := bStore.ConversationByID(ctx, "conv-r")
	bFed.ReportReceipt(ctx, mirror, federation.ReceiptUpdate{
		ConversationID: "conv-r", UserID: "bob", DeliveredSeq: 5, ReadSeq: 5,
	})

	// On the hub, bob's watermark must have advanced — alice's ticks can move.
	m, err := aStore.ConversationMembership(ctx, "conv-r", "bob")
	if err != nil {
		t.Fatal(err)
	}
	if m.ReadSeq != 5 || m.DeliveredSeq != 5 {
		t.Errorf("bob on the hub = delivered %d / read %d, want 5/5 — the receipt did not cross hosts", m.DeliveredSeq, m.ReadSeq)
	}

	// A host cannot move a receipt for a member it does not home.
	err = aFed.SubmitReceipt(ctx, "c.example", federation.ReceiptUpdate{
		ConversationID: "conv-r", UserID: "bob", ReadSeq: 99,
	})
	if err == nil {
		t.Error("a host forwarded a receipt for a member it does not home")
	}
}

// F5b: with host keys configured, the hub signs each commit's ordering link and
// the follower verifies both the link and the signature. After a round trip the
// mirror's chain head is byte-for-byte the hub's — proof the two computed the
// same order independently.
func TestSignedOrderingChainConvergesAcrossHosts(t *testing.T) {
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

	// Each host signs with its own key and verifies peers against the shared roster.
	aFed := &chat.ConvFederation{Store: aStore, Live: live.NewMemoryBus(), Peers: aClient, HostDomain: "a.example", HostKey: aKey, Keys: roster}
	bFed := &chat.ConvFederation{Store: bStore, Live: live.NewMemoryBus(), Peers: bClient, HostDomain: "b.example", HostKey: bKey, Keys: roster}

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
		ID: "conv-s", Kind: domain.ConversationGroup, CreatedAt: now,
	}, []domain.ConversationMember{{UserID: "alice", Role: domain.RoleAdmin, JoinedAt: now}})
	if err := aFed.AddRemoteMember(ctx, conv, "bob", "b.example", "Bob", "bob"); err != nil {
		t.Fatal(err)
	}

	res, err := bFed.ForwardCommit(ctx, "a.example", federation.SubmittedCommit{
		ConversationID: "conv-s", SenderID: "bob", GroupID: "grp-s", BaseEpoch: 0,
		Welcome: []byte("w"), Commit: []byte("c1"),
	})
	if err != nil {
		t.Fatalf("signed forward commit: %v", err)
	}
	if len(res.ChainHash) == 0 || len(res.ChainSig) == 0 {
		t.Fatalf("hub must return a signed chain link, got hash=%d sig=%d", len(res.ChainHash), len(res.ChainSig))
	}
	// The hub's signature must verify under the hub's key over the returned hash.
	if !mlschain.Verify(aKey.Public().(ed25519.PublicKey), res.ChainHash, res.ChainSig) {
		t.Error("hub signature does not verify")
	}
	aState, _ := aStore.MLSGroupState(ctx, "conv-s")
	bState, _ := bStore.MLSGroupState(ctx, "conv-s")
	if len(aState.ChainHash) == 0 {
		t.Fatal("hub advanced no chain head")
	}
	if !bytes.Equal(aState.ChainHash, bState.ChainHash) {
		t.Errorf("chain heads diverged: hub %x vs mirror %x", aState.ChainHash, bState.ChainHash)
	}
}

// F5b: a mirror refuses a relayed commit whose ordering link has been tampered
// with, and one whose signature is not the hub's — it does not advance its group.
func TestMirrorRefusesTamperedOrderingLink(t *testing.T) {
	aKey := hostKey5d(1)
	bKey := hostKey5d(2)
	roster := fakeRoster{
		"a.example": aKey.Public().(ed25519.PublicKey),
		"b.example": bKey.Public().(ed25519.PublicKey),
	}
	bStore := store.NewMemory(nil)
	bFed := &chat.ConvFederation{Store: bStore, Live: live.NewMemoryBus(), HostDomain: "b.example", Keys: roster}

	// Stand up a fresh mirror whose hub is a.example (no commits yet).
	if err := bFed.ProvisionMirror(context.Background(), "a.example", federation.MirrorSpec{
		ConversationID: "conv-t", Kind: "group", LocalUserID: "bob",
		RemoteMembers: []federation.RemoteMember{{UserID: "alice", Domain: "a.example"}},
	}); err != nil {
		t.Fatal(err)
	}

	commit := []byte("c1")
	goodHash := mlschain.Link(nil, 1, "grp-t", commit) // epoch 1, prevHash nil
	goodSig := mlschain.Sign(aKey, goodHash)
	base := federation.RelayedMessage{
		SenderID: "alice", SenderDomain: "a.example", Ciphertext: commit,
		ContentType: domain.ContentTypeMLSCommit, MLSEpoch: 1, MLSGroupID: "grp-t",
		ChainHash: goodHash, ChainSig: goodSig, CreatedAt: time.Now().UTC(),
	}

	// Tampered hash → rejected, mirror epoch stays 0.
	bad := base
	bad.ChainHash = append([]byte{}, goodHash...)
	bad.ChainHash[0] ^= 0xff
	if err := bFed.DeliverRelayed(context.Background(), "a.example", "conv-t", []federation.RelayedMessage{bad}); err == nil {
		t.Error("mirror accepted a commit with a tampered ordering hash")
	}
	if st, _ := bStore.MLSGroupState(context.Background(), "conv-t"); st.Epoch != 0 {
		t.Errorf("mirror advanced to epoch %d on a tampered commit", st.Epoch)
	}

	// Right hash, but signed by the wrong host → rejected.
	forged := base
	forged.ChainSig = mlschain.Sign(bKey, goodHash) // not the hub's key
	if err := bFed.DeliverRelayed(context.Background(), "a.example", "conv-t", []federation.RelayedMessage{forged}); err == nil {
		t.Error("mirror accepted a commit signed by a non-hub key")
	}
	if st, _ := bStore.MLSGroupState(context.Background(), "conv-t"); st.Epoch != 0 {
		t.Errorf("mirror advanced to epoch %d on a forged signature", st.Epoch)
	}

	// The genuine, hub-signed link is accepted and advances the mirror.
	if err := bFed.DeliverRelayed(context.Background(), "a.example", "conv-t", []federation.RelayedMessage{base}); err != nil {
		t.Fatalf("mirror refused a valid hub-signed commit: %v", err)
	}
	if st, _ := bStore.MLSGroupState(context.Background(), "conv-t"); st.Epoch != 1 || !bytes.Equal(st.ChainHash, goodHash) {
		t.Errorf("mirror did not apply the valid commit: epoch=%d hash=%x", st.Epoch, st.ChainHash)
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

func seqOf(msgs []domain.ChatMessage, ciphertext string) int64 {
	for _, m := range msgs {
		if string(m.Ciphertext) == ciphertext {
			return m.Seq
		}
	}
	return 0
}
