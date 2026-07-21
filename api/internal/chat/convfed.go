package chat

import (
	"context"
	"log/slog"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/federation"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/store"
)

// peerConversations is the outbound federation surface the chat handler needs:
// relay to a participant host, forward to a hub, provision a mirror. *federation.Client
// satisfies it; the interface keeps the chat package testable without a network.
type peerConversations interface {
	ProvisionRemoteMirror(ctx context.Context, peerDomain string, spec federation.MirrorSpec) error
	RelayToPeer(ctx context.Context, peerDomain, conversationID string, msgs []federation.RelayedMessage) error
	SubmitMessageToHub(ctx context.Context, hubDomain string, s federation.SubmittedMessage) (federation.RelayedMessage, error)
	SubmitCommitToHub(ctx context.Context, hubDomain string, s federation.SubmittedCommit) (federation.CommitResult, error)
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
	Logger     *slog.Logger
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
		relayed[i] = toRelayed(m, senderDomain)
	}
	for _, host := range hosts {
		if err := c.Peers.RelayToPeer(ctx, host, conv.ID, relayed); err != nil {
			c.log().Warn("conversation relay failed", "conversation", conv.ID, "peer", host, "error", err)
		}
	}
}

func toRelayed(m domain.ChatMessage, senderDomain string) federation.RelayedMessage {
	return federation.RelayedMessage{
		ID:           m.ID,
		SenderID:     m.SenderID,
		SenderDomain: senderDomain,
		Ciphertext:   m.Ciphertext,
		ContentType:  m.ContentType,
		MLSEpoch:     m.MLSEpoch,
		MLSGroupID:   m.MLSGroupID,
		CreatedAt:    m.CreatedAt,
	}
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
		HubDomain: hubDomain,
		CreatedAt: now,
	}
	members := []domain.ConversationMember{
		{UserID: spec.LocalUserID, Role: domain.RoleUser, JoinedAt: now},
	}
	for _, rm := range spec.RemoteMembers {
		members = append(members, domain.ConversationMember{
			UserID: rm.UserID, Domain: rm.Domain, Role: domain.RoleUser, JoinedAt: now,
		})
	}
	if spec.GroupState != nil {
		conv.MLS.GroupID = spec.GroupState.GroupID
		conv.MLS.Epoch = spec.GroupState.Epoch
	}
	_, err := c.Store.CreateConversation(ctx, conv, members)
	return err
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
	return toRelayed(stored, fromDomain), nil
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
	return federation.CommitResult{Status: "accepted", Epoch: epoch, GroupID: s.GroupID}, nil
}

// relayExcept relays to every participant host but one (the submitter).
func (c *ConvFederation) relayExcept(ctx context.Context, conv domain.Conversation, except string, msgs []domain.ChatMessage) {
	if c.Peers == nil {
		return
	}
	relayed := make([]federation.RelayedMessage, len(msgs))
	for i, m := range msgs {
		relayed[i] = toRelayed(m, c.HostDomain)
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
		CreatedAt:      rm.CreatedAt,
	}
}

// Errors returned to the federation handler, mapped to opaque HTTP statuses there.
var (
	errNoMirror  = convFedError("no mirror for this conversation from this hub")
	errNotHub    = convFedError("this host is not the hub for this conversation")
	errNotMember = convFedError("sender is not a member from that host")
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
// caller then reconciles the MLS group to add the member's devices.
func (c *ConvFederation) AddRemoteMember(ctx context.Context, conv domain.Conversation, remoteUserID, remoteDomain string) error {
	now := time.Now().UTC()
	if _, err := c.Store.AddConversationMember(ctx, domain.ConversationMember{
		ConversationID: conv.ID, UserID: remoteUserID, Domain: remoteDomain,
		Role: domain.RoleUser, JoinedAt: now,
	}); err != nil {
		return err
	}
	// Tell the remote host to stand up its mirror, with this member local to it
	// and every current member (including us) as remote.
	members, err := c.Store.ConversationMembers(ctx, conv.ID)
	if err != nil {
		return err
	}
	var remotes []federation.RemoteMember
	for _, m := range members {
		if m.UserID == remoteUserID {
			continue // this is the local member on the remote host
		}
		d := m.Domain
		if d == "" {
			d = c.HostDomain
		}
		remotes = append(remotes, federation.RemoteMember{UserID: m.UserID, Domain: d})
	}
	spec := federation.MirrorSpec{
		ConversationID: conv.ID,
		Kind:           string(conv.Kind),
		Title:          conv.Title,
		LocalUserID:    remoteUserID,
		RemoteMembers:  remotes,
	}
	if conv.MLS.GroupID != "" {
		spec.GroupState = &federation.MirrorGroupSt{GroupID: conv.MLS.GroupID, Epoch: conv.MLS.Epoch}
	}
	return c.Peers.ProvisionRemoteMirror(ctx, remoteDomain, spec)
}
