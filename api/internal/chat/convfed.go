package chat

import (
	"context"
	"crypto/ed25519"
	"log/slog"
	"strings"
	"time"

	"github.com/rh1tech/pheme/api/internal/calls"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/federation"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/mlschain"
	"github.com/rh1tech/pheme/api/internal/store"
)

// hubKeys resolves a host domain to its nodelist public key, so a follower can
// verify a hub's signature on an ordering-chain link. The nodelist store
// satisfies it.
type hubKeys interface {
	KeyFor(domain string) (ed25519.PublicKey, error)
}

// hostAliases resolves the friendly, network-wide host names carried in the
// nodelist. The nodelist store satisfies it.
type hostAliases interface {
	DomainForAlias(alias string) (string, bool)
	AliasForDomain(domain string) (string, bool)
}

// peerConversations is the outbound federation surface the chat handler needs:
// relay to a participant host, forward to a hub, provision a mirror. *federation.Client
// satisfies it; the interface keeps the chat package testable without a network.
type peerConversations interface {
	ProvisionRemoteMirror(ctx context.Context, peerDomain string, spec federation.MirrorSpec) error
	RelayToPeer(ctx context.Context, peerDomain, conversationID string, msgs []federation.RelayedMessage) error
	SubmitMessageToHub(ctx context.Context, hubDomain string, s federation.SubmittedMessage) (federation.RelayedMessage, error)
	SubmitCommitToHub(ctx context.Context, hubDomain string, s federation.SubmittedCommit) (federation.CommitResult, error)
	SubmitReceiptToHub(ctx context.Context, hubDomain string, s federation.ReceiptUpdate) error
	RelayReceiptToPeer(ctx context.Context, peerDomain string, s federation.ReceiptUpdate) error
	RelayCallSignalToPeer(ctx context.Context, peerDomain string, s federation.CallSignalRelay) error
	RelayCallNudgeToPeer(ctx context.Context, peerDomain string, s federation.CallNudge) error
	RequestTurnFromPeer(ctx context.Context, peerDomain, conversationID string) (federation.TurnGrant, error)
	ClaimRemoteKeyPackages(ctx context.Context, homeDomain, userID string) ([]federation.ClaimedKeyPackage, error)
	ResolveRemoteUser(ctx context.Context, homeDomain, username string) (federation.RemoteUser, error)
	DeleteConversationOnPeer(ctx context.Context, peerDomain, conversationID string) error
}

// callMailbox is the append side of the per-call signalling channel — enough for a
// relayed signal to land in this host's mailbox so a local device fetches it in
// order. *calls.Memory / the Redis mailbox satisfy it.
type callMailbox interface {
	Append(ctx context.Context, callID string, ciphertext []byte) (calls.Signal, error)
}

// callRinger wakes a conversation's local devices for an incoming (or cancelled)
// call. The chat Handler implements it over its existing push fan-out.
type callRinger interface {
	RingForCall(ctx context.Context, convID, callerID, callID string, cancel bool)
}

// ConvFederation carries an encrypted conversation across hosts. It is both the
// inbound service (implementing federation.ConversationService) and the outbound
// helper the chat handlers call after they append a message.
//
// The hub is the single ordering authority: every commit and every message is
// appended on the hub and relayed out from there, so all participant hosts see
// one ordered log. A follower's device posts to its own host, which forwards to
// the hub. The server reads none of it — a message is MLS ciphertext or an opaque
// control message.
type ConvFederation struct {
	Store      store.Store
	Live       live.Bus
	Peers      peerConversations
	HostDomain string
	// HostKey signs this host's ordering-chain links when it is the hub. Nil leaves
	// links unsigned — a follower with a hub key configured then rejects them, which
	// is the safe default: unsigned ordering is not to be trusted across hosts.
	HostKey ed25519.PrivateKey
	// Keys resolves a hub domain to its public key for verifying an incoming link.
	// Nil skips verification (single-host or a deployment without a nodelist here).
	Keys hubKeys
	// Aliases maps a host alias to its domain, so a user typed as `name@pheme1`
	// reaches the same host as `name@its-full-domain`. Nil disables aliasing —
	// only full domains resolve then.
	Aliases hostAliases
	// Mailbox and Ringer let a relayed call signal land locally: appended to this
	// host's call mailbox and rung to its devices. Nil disables cross-host calling
	// (a deployment with no calling configured).
	Mailbox callMailbox
	Ringer  callRinger
	// ICE is this host's TURN config, used to mint a credential a peer's member can
	// use to relay a cross-host call through this host. Zero value = no TURN offered.
	ICE    ICEConfig
	Logger *slog.Logger
}

func (c *ConvFederation) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

// participantHosts returns the distinct peer domains with a member in the
// conversation — the hosts a hub relays to. This host's own domain is excluded.
func (c *ConvFederation) participantHosts(ctx context.Context, convID string) []string {
	members, err := c.Store.ConversationMembers(ctx, convID)
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var hosts []string
	for _, m := range members {
		if m.Domain == "" || m.Domain == c.HostDomain {
			continue
		}
		if _, dup := seen[m.Domain]; dup {
			continue
		}
		seen[m.Domain] = struct{}{}
		hosts = append(hosts, m.Domain)
	}
	return hosts
}

// RelayAppended fans a batch of just-appended messages out to every participant
// host. Called on the HUB after a local append. Fire-and-forget: a peer that is
// down must not hold up a message that already reached local devices — the peer
// can catch up from history.
func (c *ConvFederation) RelayAppended(ctx context.Context, conv domain.Conversation, senderDomain string, msgs []domain.ChatMessage) {
	if c.Peers == nil || conv.IsMirror() {
		return // only the hub relays, and only for a native conversation
	}
	hosts := c.participantHosts(ctx, conv.ID)
	if len(hosts) == 0 {
		return
	}
	relayed := make([]federation.RelayedMessage, len(msgs))
	for i, m := range msgs {
		relayed[i] = c.toRelayed(m, senderDomain)
	}
	for _, host := range hosts {
		if err := c.Peers.RelayToPeer(ctx, host, conv.ID, relayed); err != nil {
			c.log().Warn("conversation relay failed", "conversation", conv.ID, "peer", host, "error", err)
		}
	}
}

// toRelayed puts a stored message on the wire. For a Commit it attaches the
// ordering-chain link the store stamped and, as the hub, signs it with the host
// key so the receiving mirror can verify the hub — not the relay — set this
// position.
func (c *ConvFederation) toRelayed(m domain.ChatMessage, senderDomain string) federation.RelayedMessage {
	r := federation.RelayedMessage{
		ID:           m.ID,
		SenderID:     m.SenderID,
		SenderDomain: senderDomain,
		Ciphertext:   m.Ciphertext,
		ContentType:  m.ContentType,
		MLSEpoch:     m.MLSEpoch,
		MLSGroupID:   m.MLSGroupID,
		CreatedAt:    m.CreatedAt,
		Seq:          m.Seq,
		ChainHash:    m.MLSChainHash,
	}
	if len(m.MLSChainHash) > 0 && len(c.HostKey) == ed25519.PrivateKeySize {
		r.ChainSig = mlschain.Sign(c.HostKey, m.MLSChainHash)
	}
	return r
}

// --- inbound: federation.ConversationService ---

// ProvisionMirror stands up a local mirror for one of our users added to a
// remote conversation. Idempotent: a second provision of the same conversation
// is a no-op, since a hub may retry.
func (c *ConvFederation) ProvisionMirror(ctx context.Context, hubDomain string, spec federation.MirrorSpec) error {
	if _, err := c.Store.ConversationByID(ctx, spec.ConversationID); err == nil {
		return nil // already mirrored
	}
	now := time.Now().UTC()
	conv := domain.Conversation{
		ID:        spec.ConversationID,
		Kind:      domain.ConversationKind(spec.Kind),
		Title:     spec.Title,
		DirectKey: spec.DirectKey,
		HubDomain: hubDomain,
		CreatedAt: now,
	}
	members := []domain.ConversationMember{
		{UserID: spec.LocalUserID, Role: domain.RoleUser, JoinedAt: now},
	}
	for _, rm := range spec.RemoteMembers {
		members = append(members, domain.ConversationMember{
			UserID: rm.UserID, Domain: rm.Domain,
			DisplayName: rm.DisplayName, Username: rm.Username,
			Role: domain.RoleUser, JoinedAt: now,
		})
	}
	if spec.GroupState != nil {
		conv.MLS.GroupID = spec.GroupState.GroupID
		conv.MLS.Epoch = spec.GroupState.Epoch
		conv.MLS.ChainHash = spec.GroupState.ChainHash
	}
	_, err := c.Store.CreateConversation(ctx, conv, members)
	return err
}

// DeleteMirror deletes this host's copy of a conversation because fromDomain — a
// peer that shares it — deleted theirs. It verifies fromDomain is actually part of
// the conversation (its hub, or the home of one of its members) before deleting,
// so a signed peer cannot delete a conversation it has nothing to do with.
func (c *ConvFederation) DeleteMirror(ctx context.Context, fromDomain, conversationID string) error {
	conv, err := c.Store.ConversationByID(ctx, conversationID)
	if err != nil {
		return nil // already gone; nothing to do
	}
	members, err := c.Store.ConversationMembers(ctx, conversationID)
	if err != nil {
		return err
	}
	if !peerInConversation(conv, members, fromDomain) {
		return nil // not this caller's conversation — ignore
	}
	recipients := make([]string, 0, len(members))
	for _, m := range members {
		recipients = append(recipients, m.UserID)
	}
	if err := c.Store.DeleteConversation(ctx, conversationID); err != nil {
		return err
	}
	c.Live.Publish(live.Event{ConversationID: conversationID, ConversationDeleted: true, Recipients: recipients})
	return nil
}

// PropagateDelete tells every OTHER host in a conversation to delete their copy,
// after this host deleted its own. Best effort: a peer that is unreachable keeps a
// stale mirror, a nuisance rather than a correctness problem — the local delete has
// already happened.
func (c *ConvFederation) PropagateDelete(ctx context.Context, conv domain.Conversation, members []domain.ConversationMember) {
	if c.Peers == nil {
		return
	}
	peers := make(map[string]struct{})
	if conv.IsMirror() && conv.HubDomain != "" && conv.HubDomain != c.HostDomain {
		peers[conv.HubDomain] = struct{}{}
	}
	for _, m := range members {
		if m.Domain != "" && m.Domain != c.HostDomain {
			peers[m.Domain] = struct{}{}
		}
	}
	for d := range peers {
		if err := c.Peers.DeleteConversationOnPeer(ctx, d, conv.ID); err != nil {
			c.log().Warn("federation: propagate delete", "peer", d, "conversation", conv.ID, "error", err)
		}
	}
}

// peerInConversation reports whether peerDomain is the hub of conv or the home of
// one of its members — the hosts entitled to act on it.
func peerInConversation(conv domain.Conversation, members []domain.ConversationMember, peerDomain string) bool {
	if conv.HubDomain == peerDomain {
		return true
	}
	for _, m := range members {
		if m.Domain == peerDomain {
			return true
		}
	}
	return false
}

// DeliverRelayed appends relayed messages to the mirror's log and publishes them
// to local devices — the same local delivery a native message gets. Runs on a
// follower.
func (c *ConvFederation) DeliverRelayed(ctx context.Context, hubDomain, conversationID string, msgs []federation.RelayedMessage) error {
	conv, err := c.Store.ConversationByID(ctx, conversationID)
	if err != nil || conv.HubDomain != hubDomain {
		// We do not mirror this conversation from this hub. A peer cannot inject
		// into a conversation it does not host for us.
		return errNoMirror
	}
	// A relayed commit is not an ordinary message: it advances the group's epoch and
	// ordering chain, so it must go through CommitMLSGroup after the chain is
	// verified — never be appended as plain content. Everything else is delivered
	// as-is.
	for i := range msgs {
		if msgs[i].ContentType == contentTypeMLSCommit {
			return c.applyRelayedCommit(ctx, conv, hubDomain, msgs, msgs[i])
		}
	}
	for _, rm := range msgs {
		stored, err := c.Store.AppendChatMessage(ctx, fromRelayed(conversationID, rm))
		if err != nil {
			c.log().Error("append relayed", "conversation", conversationID, "error", err)
			continue
		}
		c.publishLocal(ctx, conversationID, stored)
	}
	return nil
}

// applyRelayedCommit verifies a hub's ordering link against the mirror's own head
// and the hub's signature, then advances the mirror to the hub's epoch. A hash
// that does not match is the hub reordering, dropping, or forking the log — the
// mirror refuses it rather than silently diverging.
func (c *ConvFederation) applyRelayedCommit(ctx context.Context, conv domain.Conversation, hubDomain string, batch []federation.RelayedMessage, commit federation.RelayedMessage) error {
	state, err := c.Store.MLSGroupState(ctx, conv.ID)
	if err != nil {
		return err
	}
	expected := mlschain.Link(state.ChainHash, commit.MLSEpoch, commit.MLSGroupID, commit.Ciphertext)
	if !bytesEqual(expected, commit.ChainHash) {
		c.log().Error("ordering chain mismatch — refusing relayed commit",
			"conversation", conv.ID, "hub", hubDomain, "epoch", commit.MLSEpoch)
		return errChainMismatch
	}
	if !c.verifyHubSig(hubDomain, commit.ChainHash, commit.ChainSig) {
		c.log().Error("ordering chain signature invalid — refusing relayed commit",
			"conversation", conv.ID, "hub", hubDomain, "epoch", commit.MLSEpoch)
		return errChainUnsigned
	}
	// Apply the whole batch (Welcome first, then Commit) atomically. CommitMLSGroup
	// recomputes the identical hash from the mirror's head, so the mirror's stored
	// chain stays byte-for-byte the hub's.
	ordered := make([]domain.ChatMessage, 0, len(batch))
	for _, rm := range batch {
		ordered = append(ordered, fromRelayed(conv.ID, rm))
	}
	_, stored, err := c.Store.CommitMLSGroup(ctx, conv.ID, commit.MLSGroupID, commit.MLSEpoch-1, ordered)
	if err == store.ErrEpochConflict {
		// A duplicate relay, or we are behind — not corruption. The verified hash
		// already proved the order; nothing to apply twice.
		c.log().Warn("relayed commit not applicable at this epoch", "conversation", conv.ID, "epoch", commit.MLSEpoch)
		return nil
	}
	if err != nil {
		return err
	}
	for i := range stored {
		c.publishLocal(ctx, conv.ID, stored[i])
	}
	return nil
}

// SubmitMessage runs on the hub: a follower forwards a device's message. Append
// it (attributed to the remote sender) and relay to every participant host.
func (c *ConvFederation) SubmitMessage(ctx context.Context, fromDomain string, s federation.SubmittedMessage) (federation.RelayedMessage, error) {
	conv, err := c.Store.ConversationByID(ctx, s.ConversationID)
	if err != nil || conv.IsMirror() {
		return federation.RelayedMessage{}, errNotHub
	}
	// The sender must actually be a member from the forwarding host — a host may
	// not post on behalf of a user it does not home.
	if !c.isMemberFromHost(ctx, s.ConversationID, s.SenderID, fromDomain) {
		return federation.RelayedMessage{}, errNotMember
	}
	stored, err := c.Store.AppendChatMessage(ctx, domain.ChatMessage{
		ConversationID: s.ConversationID,
		SenderID:       s.SenderID,
		Ciphertext:     s.Ciphertext,
		ContentType:    s.ContentType,
		CreatedAt:      time.Now().UTC(),
	})
	if err != nil {
		return federation.RelayedMessage{}, err
	}
	c.publishLocal(ctx, s.ConversationID, stored)
	// Relay to every participant host EXCEPT the one that submitted it — it will
	// echo the message to its own devices from the response.
	c.relayExcept(ctx, conv, fromDomain, []domain.ChatMessage{stored})
	return c.toRelayed(stored, fromDomain), nil
}

// SubmitCommit runs on the hub: a follower forwards a device's MLS commit. Run
// the same epoch-guarded compare-and-set a local commit gets, then relay the
// accepted control messages to every participant host.
func (c *ConvFederation) SubmitCommit(ctx context.Context, fromDomain string, s federation.SubmittedCommit) (federation.CommitResult, error) {
	conv, err := c.Store.ConversationByID(ctx, s.ConversationID)
	if err != nil || conv.IsMirror() {
		return federation.CommitResult{}, errNotHub
	}
	if !c.isMemberFromHost(ctx, s.ConversationID, s.SenderID, fromDomain) {
		return federation.CommitResult{}, errNotMember
	}
	epoch := s.BaseEpoch + 1
	now := time.Now().UTC()
	var msgs []domain.ChatMessage
	if len(s.Welcome) > 0 {
		msgs = append(msgs, domain.ChatMessage{
			ConversationID: s.ConversationID, SenderID: s.SenderID, Ciphertext: s.Welcome,
			ContentType: contentTypeMLSWelcome, MLSEpoch: epoch, MLSGroupID: s.GroupID, CreatedAt: now,
		})
	}
	msgs = append(msgs, domain.ChatMessage{
		ConversationID: s.ConversationID, SenderID: s.SenderID, Ciphertext: s.Commit,
		ContentType: contentTypeMLSCommit, MLSEpoch: epoch, MLSGroupID: s.GroupID, CreatedAt: now,
	})
	state, stored, err := c.Store.CommitMLSGroup(ctx, s.ConversationID, s.GroupID, s.BaseEpoch, msgs)
	if err == store.ErrEpochConflict {
		return federation.CommitResult{Status: "conflict", Epoch: state.Epoch, GroupID: state.GroupID}, nil
	}
	if err != nil {
		return federation.CommitResult{}, err
	}
	for i := range stored {
		c.publishLocal(ctx, s.ConversationID, stored[i])
	}
	c.relayExcept(ctx, conv, fromDomain, stored)
	// Hand the follower the ordering link this commit got, signed, so it can apply
	// it under the same chain the other participant hosts will.
	hash := commitChainHashOf(stored)
	res := federation.CommitResult{Status: "accepted", Epoch: epoch, GroupID: s.GroupID, ChainHash: hash}
	if len(hash) > 0 && len(c.HostKey) == ed25519.PrivateKeySize {
		res.ChainSig = mlschain.Sign(c.HostKey, hash)
	}
	return res, nil
}

// commitChainHashOf returns the ordering-chain hash the store stamped on the
// Commit in a stored batch.
func commitChainHashOf(stored []domain.ChatMessage) []byte {
	for _, m := range stored {
		if m.ContentType == contentTypeMLSCommit {
			return m.MLSChainHash
		}
	}
	return nil
}

// verifyHubSig checks a hub's signature over a chain hash against its nodelist
// key. With no key lookup configured (single-host, or tests) verification is
// skipped — but a configured lookup that cannot produce the hub's key, or a bad
// signature, is a rejection: a missing signature must never pass for a present
// one across hosts.
func (c *ConvFederation) verifyHubSig(hubDomain string, hash, sig []byte) bool {
	if c.Keys == nil {
		return true
	}
	key, err := c.Keys.KeyFor(hubDomain)
	if err != nil {
		return false
	}
	return mlschain.Verify(key, hash, sig)
}

// SubmitReceipt runs on the hub: a follower forwards one of its members' receipt
// watermarks. Advance them and relay to every other participant host, so a sender
// on any host sees the ticks move.
func (c *ConvFederation) SubmitReceipt(ctx context.Context, fromDomain string, s federation.ReceiptUpdate) error {
	conv, err := c.Store.ConversationByID(ctx, s.ConversationID)
	if err != nil || conv.IsMirror() {
		return errNotHub
	}
	if !c.isMemberFromHost(ctx, s.ConversationID, s.UserID, fromDomain) {
		return errNotMember
	}
	if err := c.applyReceipt(ctx, s); err != nil {
		return err
	}
	c.relayReceipt(ctx, conv, fromDomain, s)
	return nil
}

// DeliverReceipt runs on a follower: the hub relays a member's advanced watermarks;
// apply them to the mirror so a sender on this host sees them.
func (c *ConvFederation) DeliverReceipt(ctx context.Context, hubDomain string, s federation.ReceiptUpdate) error {
	conv, err := c.Store.ConversationByID(ctx, s.ConversationID)
	if err != nil || conv.HubDomain != hubDomain {
		return errNoMirror
	}
	return c.applyReceipt(ctx, s)
}

// applyReceipt advances a member's watermarks and publishes the change locally, so
// a sender watching this host sees the ticks move now. $max in the store keeps it
// forward-only and idempotent, so a relayed duplicate is harmless.
func (c *ConvFederation) applyReceipt(ctx context.Context, s federation.ReceiptUpdate) error {
	receipt, err := c.Store.SetConversationReceipt(ctx, s.ConversationID, s.UserID, s.DeliveredSeq, s.ReadSeq)
	if err != nil {
		return err
	}
	if c.Live != nil {
		r := receipt
		c.Live.Publish(live.Event{ConversationID: s.ConversationID, Receipt: &r})
	}
	return nil
}

// relayReceipt (hub) relays a member's watermarks to every participant host but one.
func (c *ConvFederation) relayReceipt(ctx context.Context, conv domain.Conversation, except string, s federation.ReceiptUpdate) {
	if c.Peers == nil {
		return
	}
	for _, host := range c.participantHosts(ctx, conv.ID) {
		if host == except {
			continue
		}
		if err := c.Peers.RelayReceiptToPeer(ctx, host, s); err != nil {
			c.log().Warn("receipt relay failed", "conversation", conv.ID, "peer", host, "error", err)
		}
	}
}

// --- calls: a mesh, not a hub ---
//
// A call has no ordering authority. Every participant host relays its own members'
// sealed signals to every other participant host, which lands them in its mailbox
// and nudges its devices; so each host holds a complete copy and every device
// fetches from home. The answer lock stays host-local on purpose — a member's
// devices all live on that member's home host, which is exactly the set the lock
// arbitrates.

// RelayCallSignal sends a local member's sealed signal to every participant host.
// Called on the host the signal was posted to, after it has been delivered locally.
func (c *ConvFederation) RelayCallSignal(ctx context.Context, s federation.CallSignalRelay) {
	if c.Peers == nil {
		return
	}
	for _, host := range c.participantHosts(ctx, s.ConversationID) {
		if err := c.Peers.RelayCallSignalToPeer(ctx, host, s); err != nil {
			c.log().Warn("call signal relay failed", "conversation", s.ConversationID, "peer", host, "error", err)
		}
	}
}

// RelayCallNudge re-nudges every participant host about a still-ringing call.
func (c *ConvFederation) RelayCallNudge(ctx context.Context, s federation.CallNudge) {
	if c.Peers == nil {
		return
	}
	for _, host := range c.participantHosts(ctx, s.ConversationID) {
		if err := c.Peers.RelayCallNudgeToPeer(ctx, host, s); err != nil {
			c.log().Warn("call nudge relay failed", "conversation", s.ConversationID, "peer", host, "error", err)
		}
	}
}

// DeliverCallSignal (inbound): a peer relayed one of its members' signals. Append it
// to the local mailbox so a device here fetches it in order, nudge the local
// members, and ring them if the signal asked to.
func (c *ConvFederation) DeliverCallSignal(ctx context.Context, fromDomain string, s federation.CallSignalRelay) error {
	if c.Mailbox == nil {
		return errNoCalling
	}
	if _, err := c.Store.ConversationByID(ctx, s.ConversationID); err != nil {
		return errNotMember
	}
	// The signal must come from the host that homes its sender — a host cannot place
	// a call as a user it does not home.
	if !c.isMemberFromHost(ctx, s.ConversationID, s.FromUserID, fromDomain) {
		return errNotMember
	}
	signal, err := c.Mailbox.Append(ctx, callKey(s.ConversationID, s.CallID), s.Ciphertext)
	if err != nil {
		return err
	}
	c.nudgeCall(ctx, s.ConversationID, s.CallID, s.FromUserID, signal.Seq)
	if (s.Ring || s.Cancel) && c.Ringer != nil {
		c.Ringer.RingForCall(ctx, s.ConversationID, s.FromUserID, s.CallID, s.Cancel)
	}
	return nil
}

// DeliverCallNudge (inbound): re-nudge local members about a ringing call.
func (c *ConvFederation) DeliverCallNudge(ctx context.Context, fromDomain string, s federation.CallNudge) error {
	if _, err := c.Store.ConversationByID(ctx, s.ConversationID); err != nil {
		return errNotMember
	}
	c.nudgeCall(ctx, s.ConversationID, s.CallID, s.FromUserID, 0)
	return nil
}

// nudgeCall publishes a call nudge to the conversation's local members, the same
// "come and fetch" event a locally-posted signal raises.
func (c *ConvFederation) nudgeCall(ctx context.Context, convID, callID, fromUID string, seq int) {
	if c.Live == nil {
		return
	}
	members, err := c.Store.ConversationMembers(ctx, convID)
	if err != nil {
		return
	}
	to := make([]string, 0, len(members))
	for _, m := range members {
		to = append(to, m.UserID)
	}
	c.Live.Publish(live.Event{
		ConversationID: convID,
		Recipients:     to,
		CallSignal:     &live.CallSignal{CallID: callID, Seq: seq, FromUserID: fromUID},
	})
}

// ClaimRemoteKeyPackages claims a remote user's device key packages from their
// home host, so this host — acting as the hub adding them to a group — gets the
// leaf material it needs. Returns nil when federation is not wired.
func (c *ConvFederation) ClaimRemoteKeyPackages(ctx context.Context, homeDomain, userID string) ([]federation.ClaimedKeyPackage, error) {
	if c.Peers == nil {
		return nil, nil
	}
	return c.Peers.ClaimRemoteKeyPackages(ctx, homeDomain, userID)
}

// TurnCredentials (inbound): a peer asks this host to mint a TURN credential so a
// member of theirs can relay a cross-host call through our TURN. Only for a
// conversation that actually has a member from the asking host — otherwise a peer
// could farm credentials off us for calls it has no part in.
func (c *ConvFederation) TurnCredentials(ctx context.Context, fromDomain, conversationID string) (federation.TurnGrant, error) {
	members, err := c.Store.ConversationMembers(ctx, conversationID)
	if err != nil {
		return federation.TurnGrant{}, errNotMember
	}
	stake := false
	for _, m := range members {
		if m.Domain == fromDomain {
			stake = true
			break
		}
	}
	if !stake {
		return federation.TurnGrant{}, errNotMember
	}
	urls := turnURLsOf(c.ICE.URLs)
	if len(urls) == 0 {
		return federation.TurnGrant{}, nil // this host offers no TURN; not an error
	}
	username, credential := turnCredential(c.ICE.Secret, time.Now().Add(c.ICE.TTL))
	return federation.TurnGrant{URLs: urls, Username: username, Credential: credential}, nil
}

// RemoteTurn (outbound): fetch a participant host's TURN so both ends of a
// cross-host call share a relay each can reach. Best-effort — a host that is down
// or has no TURN simply contributes nothing, and the call falls back to STUN and
// direct paths.
func (c *ConvFederation) RemoteTurn(ctx context.Context, conversationID string) []federation.TurnGrant {
	if c.Peers == nil {
		return nil
	}
	var grants []federation.TurnGrant
	for _, host := range c.participantHosts(ctx, conversationID) {
		grant, err := c.Peers.RequestTurnFromPeer(ctx, host, conversationID)
		if err != nil {
			c.log().Warn("remote turn fetch failed", "conversation", conversationID, "peer", host, "error", err)
			continue
		}
		if len(grant.URLs) > 0 {
			grants = append(grants, grant)
		}
	}
	return grants
}

// ReportReceipt is the outbound hook the chat handler calls after it has advanced a
// local member's watermarks. On the hub it relays them to every participant host;
// on a mirror it forwards them to the hub, which relays them onward. Best-effort:
// the receipt is already recorded and shown locally, and a peer that is down can
// catch the watermark up from the member list on its next fetch.
func (c *ConvFederation) ReportReceipt(ctx context.Context, conv domain.Conversation, s federation.ReceiptUpdate) {
	if c.Peers == nil {
		return
	}
	if conv.IsMirror() {
		if err := c.Peers.SubmitReceiptToHub(ctx, conv.HubDomain, s); err != nil {
			c.log().Warn("receipt forward to hub failed", "conversation", conv.ID, "hub", conv.HubDomain, "error", err)
		}
		return
	}
	c.relayReceipt(ctx, conv, "", s) // hub: relay to all participant hosts
}

// relayExcept relays to every participant host but one (the submitter).
func (c *ConvFederation) relayExcept(ctx context.Context, conv domain.Conversation, except string, msgs []domain.ChatMessage) {
	if c.Peers == nil {
		return
	}
	relayed := make([]federation.RelayedMessage, len(msgs))
	for i, m := range msgs {
		relayed[i] = c.toRelayed(m, c.HostDomain)
	}
	for _, host := range c.participantHosts(ctx, conv.ID) {
		if host == except {
			continue
		}
		if err := c.Peers.RelayToPeer(ctx, host, conv.ID, relayed); err != nil {
			c.log().Warn("relay failed", "conversation", conv.ID, "peer", host, "error", err)
		}
	}
}

func (c *ConvFederation) publishLocal(_ context.Context, convID string, msg domain.ChatMessage) {
	if c.Live == nil {
		return
	}
	m := msg
	c.Live.Publish(live.Event{ConversationID: convID, ChatMessage: &m})
}

func (c *ConvFederation) isMemberFromHost(ctx context.Context, convID, userID, host string) bool {
	m, err := c.Store.ConversationMembership(ctx, convID, userID)
	return err == nil && m.Domain == host
}

func fromRelayed(convID string, rm federation.RelayedMessage) domain.ChatMessage {
	return domain.ChatMessage{
		ConversationID: convID,
		SenderID:       rm.SenderID,
		Ciphertext:     rm.Ciphertext,
		ContentType:    rm.ContentType,
		MLSEpoch:       rm.MLSEpoch,
		MLSGroupID:     rm.MLSGroupID,
		MLSChainHash:   rm.ChainHash,
		Seq:            rm.Seq,
		CreatedAt:      rm.CreatedAt,
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Errors returned to the federation handler, mapped to opaque HTTP statuses there.
var (
	errNoMirror      = convFedError("no mirror for this conversation from this hub")
	errNotHub        = convFedError("this host is not the hub for this conversation")
	errNotMember     = convFedError("sender is not a member from that host")
	errChainMismatch = convFedError("ordering chain hash does not match")
	errChainUnsigned = convFedError("ordering chain signature invalid")
	errNoCalling     = convFedError("calling is not configured on this host")
)

type convFedError string

func (e convFedError) Error() string { return string(e) }

// --- outbound helpers the chat handlers call ---

// ForwardMessage sends a local device's message to the conversation's hub and
// stores the hub's echo — with the hub-assigned id and order — into the local
// mirror, so the sender's own devices see exactly what the rest of the group
// sees. Runs on a follower. The hub does not relay back to the submitter, so
// this echo is how the mirror learns of its own member's message.
func (c *ConvFederation) ForwardMessage(ctx context.Context, hubDomain string, s federation.SubmittedMessage) (federation.RelayedMessage, error) {
	echo, err := c.Peers.SubmitMessageToHub(ctx, hubDomain, s)
	if err != nil {
		return federation.RelayedMessage{}, err
	}
	stored, err := c.Store.AppendChatMessage(ctx, fromRelayed(s.ConversationID, echo))
	if err != nil {
		return echo, err
	}
	c.publishLocal(ctx, s.ConversationID, stored)
	return echo, nil
}

// ForwardCommit sends a local device's commit to the hub. On acceptance it
// applies the same control messages to the local mirror with the hub-assigned
// epoch, so the follower's group state advances in lockstep with the hub. On a
// conflict it applies nothing and returns the result so the caller can tell its
// device to catch up. Runs on a follower.
func (c *ConvFederation) ForwardCommit(ctx context.Context, hubDomain string, s federation.SubmittedCommit) (federation.CommitResult, error) {
	res, err := c.Peers.SubmitCommitToHub(ctx, hubDomain, s)
	if err != nil || res.Status != "accepted" {
		return res, err
	}
	// The hub returned the ordering link it assigned. Confirm it is the link this
	// follower would compute from its own head, and that the hub signed it, before
	// advancing local state — the follower does not take the hub's order on faith
	// any more than a relayed commit does.
	state, serr := c.Store.MLSGroupState(ctx, s.ConversationID)
	if serr != nil {
		return res, serr
	}
	expected := mlschain.Link(state.ChainHash, res.Epoch, s.GroupID, s.Commit)
	if !bytesEqual(expected, res.ChainHash) {
		c.log().Error("hub returned a divergent ordering link — not applying",
			"conversation", s.ConversationID, "epoch", res.Epoch)
		return res, errChainMismatch
	}
	if !c.verifyHubSig(hubDomain, res.ChainHash, res.ChainSig) {
		c.log().Error("hub ordering link signature invalid — not applying",
			"conversation", s.ConversationID, "epoch", res.Epoch)
		return res, errChainUnsigned
	}
	now := time.Now().UTC()
	var msgs []domain.ChatMessage
	if len(s.Welcome) > 0 {
		msgs = append(msgs, domain.ChatMessage{
			ConversationID: s.ConversationID, SenderID: s.SenderID, Ciphertext: s.Welcome,
			ContentType: contentTypeMLSWelcome, MLSEpoch: res.Epoch, MLSGroupID: s.GroupID, CreatedAt: now,
		})
	}
	msgs = append(msgs, domain.ChatMessage{
		ConversationID: s.ConversationID, SenderID: s.SenderID, Ciphertext: s.Commit,
		ContentType: contentTypeMLSCommit, MLSEpoch: res.Epoch, MLSGroupID: s.GroupID, CreatedAt: now,
	})
	if _, _, cerr := c.Store.CommitMLSGroup(ctx, s.ConversationID, s.GroupID, s.BaseEpoch, msgs); cerr != nil {
		c.log().Warn("mirror commit apply failed", "conversation", s.ConversationID, "error", cerr)
	}
	return res, nil
}

// AddRemoteMember (hub side) records a member who lives on another host and
// provisions the mirror there, so that user's devices can read and post. The
// HostDomainFor maps the `@host` part of a handle — a full domain or a short
// alias — to a domain. An unknown value (or one that is already a domain) is
// returned unchanged, so a full domain always still works.
func (c *ConvFederation) HostDomainFor(hostOrAlias string) string {
	if c.Aliases != nil {
		if d, ok := c.Aliases.DomainForAlias(strings.ToLower(strings.TrimSpace(hostOrAlias))); ok {
			return d
		}
	}
	return hostOrAlias
}

// ResolveRemoteUser turns a `username@remoteDomain` handle into the local id that
// remote host knows the user by — the id AddRemoteMember then adds. It is how a
// person on another host is addressed by name instead of by an opaque id.
func (c *ConvFederation) ResolveRemoteUser(ctx context.Context, remoteDomain, username string) (federation.RemoteUser, error) {
	return c.Peers.ResolveRemoteUser(ctx, remoteDomain, username)
}

// caller then reconciles the MLS group to add the member's devices. displayName
// and username are the added member's own profile, carried so the mirror can show
// them by name instead of a bare id.
func (c *ConvFederation) AddRemoteMember(ctx context.Context, conv domain.Conversation, remoteUserID, remoteDomain, displayName, username string) error {
	now := time.Now().UTC()
	if _, err := c.Store.AddConversationMember(ctx, domain.ConversationMember{
		ConversationID: conv.ID, UserID: remoteUserID, Domain: remoteDomain,
		DisplayName: displayName, Username: username,
		Role: domain.RoleUser, JoinedAt: now,
	}); err != nil {
		return err
	}
	return c.ProvisionMirrorOn(ctx, conv, remoteUserID, remoteDomain)
}

// ProvisionMirrorOn tells peerDomain to stand up (or refresh) its mirror of conv,
// with mirrorLocalUserID as the member local to it and every OTHER current member —
// including this host's — carried as a remote member, each with the name this host
// knows them by. Shared by the group add path and the direct-chat create path.
func (c *ConvFederation) ProvisionMirrorOn(ctx context.Context, conv domain.Conversation, mirrorLocalUserID, peerDomain string) error {
	members, err := c.Store.ConversationMembers(ctx, conv.ID)
	if err != nil {
		return err
	}
	spec := federation.MirrorSpec{
		ConversationID: conv.ID,
		Kind:           string(conv.Kind),
		Title:          conv.Title,
		DirectKey:      conv.DirectKey,
		LocalUserID:    mirrorLocalUserID,
		RemoteMembers:  c.remoteMembersExcept(ctx, members, mirrorLocalUserID),
	}
	if conv.MLS.GroupID != "" {
		spec.GroupState = &federation.MirrorGroupSt{
			GroupID: conv.MLS.GroupID, Epoch: conv.MLS.Epoch, ChainHash: conv.MLS.ChainHash,
		}
	}
	return c.Peers.ProvisionRemoteMirror(ctx, peerDomain, spec)
}

// remoteMembersExcept turns this host's member rows into the peer's view of them:
// everyone but excludeUserID, each qualified with a domain (this host's, for a
// local member) and named. A local member's name is read authoritatively from the
// user store; an already-remote member carries the cached name it arrived with.
func (c *ConvFederation) remoteMembersExcept(ctx context.Context, members []domain.ConversationMember, excludeUserID string) []federation.RemoteMember {
	localIDs := make([]string, 0, len(members))
	for _, m := range members {
		if m.UserID != excludeUserID && m.Domain == "" {
			localIDs = append(localIDs, m.UserID)
		}
	}
	profiles, _ := c.Store.UsersByIDs(ctx, localIDs)

	out := make([]federation.RemoteMember, 0, len(members))
	for _, m := range members {
		if m.UserID == excludeUserID {
			continue
		}
		rm := federation.RemoteMember{
			UserID: m.UserID, Domain: m.Domain,
			DisplayName: m.DisplayName, Username: m.Username,
		}
		if m.Domain == "" {
			rm.Domain = c.HostDomain
			if u, ok := profiles[m.UserID]; ok {
				rm.DisplayName, rm.Username = u.DisplayName, u.Username
			}
		}
		out = append(out, rm)
	}
	return out
}
