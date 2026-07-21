package federation

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rh1tech/pheme/api/internal/httpx"
)

// ConversationService is what the federation handler needs to carry an encrypted
// conversation across hosts. The hub relays every appended message to the
// participant hosts; a follower forwards its devices' posts to the hub; and a
// mirror is provisioned on a host when one of its users is added to a remote
// conversation.
//
// The server never reads any of it — a relayed message is MLS ciphertext or an
// opaque control message. This layer moves bytes and orders them; it does not
// interpret them.
type ConversationService interface {
	// ProvisionMirror runs on the SUBSCRIBER'S host. It creates (idempotently) a
	// local mirror of a conversation whose hub is elsewhere, with localUserID as a
	// local member and the given remote members, so that user's devices can read
	// and post through the ordinary conversation endpoints.
	ProvisionMirror(ctx context.Context, hubDomain string, m MirrorSpec) error

	// DeliverRelayed runs on a follower. Messages the hub has accepted arrive here
	// and are appended to the mirror's log and published to local devices — the
	// same local delivery a native message gets.
	DeliverRelayed(ctx context.Context, hubDomain, conversationID string, msgs []RelayedMessage) error

	// SubmitMessage runs on the HUB. A follower forwards one of its devices'
	// messages; the hub appends it and relays it to every participant host.
	SubmitMessage(ctx context.Context, fromDomain string, s SubmittedMessage) (RelayedMessage, error)

	// SubmitCommit runs on the HUB. A follower forwards a device's MLS commit; the
	// hub runs the same epoch check and compare-and-set a local commit gets, then
	// relays the accepted control messages out. The result says whether it was
	// accepted or lost the epoch race.
	SubmitCommit(ctx context.Context, fromDomain string, s SubmittedCommit) (CommitResult, error)

	// SubmitReceipt runs on the HUB. A follower forwards one of its members' receipt
	// watermarks moving; the hub advances that member's watermarks and relays the
	// change to every participant host, so a sender anywhere sees the ticks move.
	SubmitReceipt(ctx context.Context, fromDomain string, s ReceiptUpdate) error

	// DeliverReceipt runs on a FOLLOWER. A member's watermarks that the hub accepted
	// arrive here and advance the local copy, so a sender on this host sees them.
	DeliverReceipt(ctx context.Context, hubDomain string, s ReceiptUpdate) error

	// DeliverCallSignal delivers a sealed call signal a member on another host sent:
	// it lands in this host's call mailbox (so a local device fetches it in order),
	// nudges the local members, and rings them if the signal asked to. Calls have no
	// hub — every participant host relays its members' signals to every other, so
	// each host's mailbox is a complete copy and every device fetches from home.
	DeliverCallSignal(ctx context.Context, fromDomain string, s CallSignalRelay) error

	// DeliverCallNudge re-nudges local members that a still-ringing call has
	// something to refetch — the cross-host half of the keep-ringing re-ping.
	DeliverCallNudge(ctx context.Context, fromDomain string, s CallNudge) error

	// TurnCredentials mints this host's TURN credential for a peer whose member
	// shares the conversation, so a cross-host call can relay through a server both
	// ends can reach. Returns a zero grant with no error when this host has no TURN.
	TurnCredentials(ctx context.Context, fromDomain, conversationID string) (TurnGrant, error)

	// DeleteMirror deletes this host's copy of a conversation because a peer that
	// shares it deleted theirs. fromDomain is the proven caller; the implementation
	// verifies that host is actually part of the conversation before deleting.
	DeleteMirror(ctx context.Context, fromDomain, conversationID string) error
}

// TurnGrant is one host's TURN relay offered to a peer for a cross-host call: the
// relay URLs and a short-lived credential the host minted from its own secret. The
// secret never travels — only a credential that expires on its own.
type TurnGrant struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username"`
	Credential string   `json:"credential"`
}

// CallSignalRelay is one sealed call signal on the wire between hosts. The
// ciphertext is opaque — SDP/ICE sealed under the conversation's MLS exporter key,
// which no server holds.
type CallSignalRelay struct {
	ConversationID string `json:"conversationId"`
	CallID         string `json:"callId"`
	FromUserID     string `json:"fromUserId"`
	Ciphertext     []byte `json:"ciphertext"`
	Ring           bool   `json:"ring,omitempty"`
	Cancel         bool   `json:"cancel,omitempty"`
}

// CallNudge asks a peer to re-nudge its members about a ringing call, without a new
// signal — the cross-host form of postCallRing.
type CallNudge struct {
	ConversationID string `json:"conversationId"`
	CallID         string `json:"callId"`
	FromUserID     string `json:"fromUserId"`
}

// ReceiptUpdate is one member's receipt watermarks moving, on the wire between
// hosts. The watermarks are sequence numbers, so they mean the same thing on
// every host without a shared clock.
type ReceiptUpdate struct {
	ConversationID string `json:"conversationId"`
	UserID         string `json:"userId"`
	DeliveredSeq   int64  `json:"deliveredSeq,omitempty"`
	ReadSeq        int64  `json:"readSeq,omitempty"`
}

// MirrorSpec describes the conversation a follower should stand up.
type MirrorSpec struct {
	ConversationID string `json:"conversationId"`
	Kind           string `json:"kind"`
	Title          string `json:"title,omitempty"`
	// DirectKey is the pair-dedup key for a direct chat. It is derived from the two
	// members' domain-qualified identities, so BOTH hosts compute the same value —
	// carrying it here is what lets the mirror side recognise an existing chat and
	// refuse to create a second when its user starts one back the other way.
	DirectKey     string         `json:"directKey,omitempty"`
	LocalUserID   string         `json:"localUserId"` // the member who lives on the receiving host
	RemoteMembers []RemoteMember `json:"remoteMembers"`
	GroupState    *MirrorGroupSt `json:"groupState,omitempty"`
}

// RemoteMember is one member who lives on another host.
type RemoteMember struct {
	UserID string `json:"userId"`
	Domain string `json:"domain"`
	// DisplayName and Username let the receiving host show this member by name
	// instead of a bare id. They are the member's own profile as their home host
	// knows it; a host that cannot supply them (an older peer) simply sends blanks.
	DisplayName string `json:"displayName,omitempty"`
	Username    string `json:"username,omitempty"`
}

// MirrorGroupSt carries the MLS group id/epoch a mirror should start from, when
// the conversation already has a group.
type MirrorGroupSt struct {
	GroupID string `json:"groupId"`
	Epoch   int64  `json:"epoch"`
	// ChainHash is the ordering-chain head at provisioning time, so a mirror stood
	// up mid-conversation verifies the next commit against the same prevHash the hub
	// holds. Empty when the group has no commits yet.
	ChainHash []byte `json:"chainHash,omitempty"`
}

// RelayedMessage is one conversation message on the wire between hosts. The
// ciphertext is opaque — MLS ciphertext or a control message.
type RelayedMessage struct {
	ID           string    `json:"id"`
	SenderID     string    `json:"senderId"`
	SenderDomain string    `json:"senderDomain,omitempty"`
	Ciphertext   []byte    `json:"ciphertext"`
	ContentType  string    `json:"contentType"`
	MLSEpoch     int64     `json:"mlsEpoch,omitempty"`
	MLSGroupID   string    `json:"mlsGroupId,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	// Seq is the hub's per-conversation sequence number for this message. A mirror
	// stores it verbatim so its transcript orders identically to the hub's.
	Seq int64 `json:"seq,omitempty"`
	// ChainHash and ChainSig carry the signed ordering chain for a Commit (see
	// internal/mlschain). ChainHash is the hub-computed link; ChainSig is the hub's
	// signature over it. A mirror recomputes the hash from its own head and checks
	// both before applying the commit — that is what makes the hub's ordering
	// tamper-evident rather than merely trusted. Empty on non-commit messages.
	ChainHash []byte `json:"chainHash,omitempty"`
	ChainSig  []byte `json:"chainSig,omitempty"`
}

// SubmittedMessage is a follower's device message, forwarded to the hub.
type SubmittedMessage struct {
	ConversationID string `json:"conversationId"`
	SenderID       string `json:"senderId"`
	Ciphertext     []byte `json:"ciphertext"`
	ContentType    string `json:"contentType"`
}

// SubmittedCommit is a follower's device commit, forwarded to the hub.
type SubmittedCommit struct {
	ConversationID string `json:"conversationId"`
	SenderID       string `json:"senderId"`
	GroupID        string `json:"groupId"`
	BaseEpoch      int64  `json:"baseEpoch"`
	Welcome        []byte `json:"welcome,omitempty"`
	Commit         []byte `json:"commit"`
}

// CommitResult is the hub's answer to a forwarded commit.
type CommitResult struct {
	Status string `json:"status"` // "accepted" | "conflict"
	Epoch  int64  `json:"epoch"`
	// GroupID/Epoch of the conversation as it actually is, for a conflict so the
	// follower can tell its device to catch up.
	GroupID string `json:"groupId,omitempty"`
	// ChainHash and ChainSig are the hub's ordering-chain link for an accepted
	// commit, so the follower can confirm the link it computed locally matches the
	// hub's and is signed by it. Empty on a conflict.
	ChainHash []byte `json:"chainHash,omitempty"`
	ChainSig  []byte `json:"chainSig,omitempty"`
}

func (h *Handler) registerConversations(mux *http.ServeMux) {
	mux.Handle("POST /federation/v1/conversation-provision", h.verified(http.HandlerFunc(h.convProvision)))
	mux.Handle("POST /federation/v1/conversation-relay", h.verified(http.HandlerFunc(h.convRelay)))
	mux.Handle("POST /federation/v1/conversation-submit-message", h.verified(http.HandlerFunc(h.convSubmitMessage)))
	mux.Handle("POST /federation/v1/conversation-submit-commit", h.verified(http.HandlerFunc(h.convSubmitCommit)))
	mux.Handle("POST /federation/v1/conversation-submit-receipt", h.verified(http.HandlerFunc(h.convSubmitReceipt)))
	mux.Handle("POST /federation/v1/conversation-relay-receipt", h.verified(http.HandlerFunc(h.convRelayReceipt)))
	mux.Handle("POST /federation/v1/conversation-call-signal", h.verified(http.HandlerFunc(h.convCallSignal)))
	mux.Handle("POST /federation/v1/conversation-call-nudge", h.verified(http.HandlerFunc(h.convCallNudge)))
	mux.Handle("POST /federation/v1/conversation-turn", h.verified(http.HandlerFunc(h.convTurn)))
	mux.Handle("POST /federation/v1/conversation-delete", h.verified(http.HandlerFunc(h.convDelete)))
}

// convDelete: a peer that shares a conversation has deleted it and asks us to
// delete our copy too, so a deletion is not one-sided.
func (h *Handler) convDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConversationID string `json:"conversationId"`
	}
	if err := json.Unmarshal(verifiedBody(r), &req); err != nil || req.ConversationID == "" {
		httpx.Error(w, http.StatusBadRequest, "conversationId required")
		return
	}
	if err := h.Conversations.DeleteMirror(r.Context(), caller(r).Origin, req.ConversationID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not delete mirror")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// convProvision (follower): the hub asks us to stand up a mirror for one of our
// users who has been added to a remote conversation.
func (h *Handler) convProvision(w http.ResponseWriter, r *http.Request) {
	var spec MirrorSpec
	if err := json.Unmarshal(verifiedBody(r), &spec); err != nil || spec.ConversationID == "" || spec.LocalUserID == "" {
		httpx.Error(w, http.StatusBadRequest, "conversationId and localUserId required")
		return
	}
	// The hub is the proven caller — not a field in the body a peer could forge.
	if err := h.Conversations.ProvisionMirror(r.Context(), caller(r).Origin, spec); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not provision mirror")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"provisioned": true})
}

// convRelay (follower): the hub relays messages it has accepted; deliver them to
// our local devices.
func (h *Handler) convRelay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConversationID string           `json:"conversationId"`
		Messages       []RelayedMessage `json:"messages"`
	}
	if err := json.Unmarshal(verifiedBody(r), &req); err != nil || req.ConversationID == "" {
		httpx.Error(w, http.StatusBadRequest, "malformed relay")
		return
	}
	if err := h.Conversations.DeliverRelayed(r.Context(), caller(r).Origin, req.ConversationID, req.Messages); err != nil {
		httpx.Error(w, http.StatusNotFound, "no such mirror")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"delivered": len(req.Messages)})
}

// convSubmitMessage (hub): a follower forwards a device's message; append and
// relay it.
func (h *Handler) convSubmitMessage(w http.ResponseWriter, r *http.Request) {
	var s SubmittedMessage
	if err := json.Unmarshal(verifiedBody(r), &s); err != nil || s.ConversationID == "" {
		httpx.Error(w, http.StatusBadRequest, "malformed submission")
		return
	}
	msg, err := h.Conversations.SubmitMessage(r.Context(), caller(r).Origin, s)
	if err != nil {
		httpx.Error(w, http.StatusForbidden, "could not accept message")
		return
	}
	httpx.JSON(w, http.StatusOK, msg)
}

// convSubmitCommit (hub): a follower forwards a device's commit; order it and
// relay the result.
func (h *Handler) convSubmitCommit(w http.ResponseWriter, r *http.Request) {
	var s SubmittedCommit
	if err := json.Unmarshal(verifiedBody(r), &s); err != nil || s.ConversationID == "" {
		httpx.Error(w, http.StatusBadRequest, "malformed submission")
		return
	}
	res, err := h.Conversations.SubmitCommit(r.Context(), caller(r).Origin, s)
	if err != nil {
		httpx.Error(w, http.StatusForbidden, "could not accept commit")
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// convSubmitReceipt (hub): a follower forwards a member's receipt; apply and relay.
func (h *Handler) convSubmitReceipt(w http.ResponseWriter, r *http.Request) {
	var s ReceiptUpdate
	if err := json.Unmarshal(verifiedBody(r), &s); err != nil || s.ConversationID == "" || s.UserID == "" {
		httpx.Error(w, http.StatusBadRequest, "malformed receipt")
		return
	}
	if err := h.Conversations.SubmitReceipt(r.Context(), caller(r).Origin, s); err != nil {
		httpx.Error(w, http.StatusForbidden, "could not accept receipt")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"accepted": true})
}

// convRelayReceipt (follower): the hub relays a member's receipt; advance our copy.
func (h *Handler) convRelayReceipt(w http.ResponseWriter, r *http.Request) {
	var s ReceiptUpdate
	if err := json.Unmarshal(verifiedBody(r), &s); err != nil || s.ConversationID == "" || s.UserID == "" {
		httpx.Error(w, http.StatusBadRequest, "malformed receipt")
		return
	}
	if err := h.Conversations.DeliverReceipt(r.Context(), caller(r).Origin, s); err != nil {
		httpx.Error(w, http.StatusNotFound, "no such mirror")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"delivered": true})
}

// convCallSignal: a peer relays a member's sealed call signal; deliver it locally.
func (h *Handler) convCallSignal(w http.ResponseWriter, r *http.Request) {
	var s CallSignalRelay
	if err := json.Unmarshal(verifiedBody(r), &s); err != nil || s.ConversationID == "" || s.CallID == "" || s.FromUserID == "" {
		httpx.Error(w, http.StatusBadRequest, "malformed call signal")
		return
	}
	if err := h.Conversations.DeliverCallSignal(r.Context(), caller(r).Origin, s); err != nil {
		httpx.Error(w, http.StatusForbidden, "could not accept call signal")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"delivered": true})
}

// convCallNudge: a peer asks us to re-nudge a ringing call's local members.
func (h *Handler) convCallNudge(w http.ResponseWriter, r *http.Request) {
	var s CallNudge
	if err := json.Unmarshal(verifiedBody(r), &s); err != nil || s.ConversationID == "" || s.CallID == "" {
		httpx.Error(w, http.StatusBadRequest, "malformed call nudge")
		return
	}
	if err := h.Conversations.DeliverCallNudge(r.Context(), caller(r).Origin, s); err != nil {
		httpx.Error(w, http.StatusNotFound, "no such conversation")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"nudged": true})
}

// convTurn: a peer asks this host to mint a TURN credential for a shared call.
func (h *Handler) convTurn(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConversationID string `json:"conversationId"`
	}
	if err := json.Unmarshal(verifiedBody(r), &req); err != nil || req.ConversationID == "" {
		httpx.Error(w, http.StatusBadRequest, "conversationId required")
		return
	}
	grant, err := h.Conversations.TurnCredentials(r.Context(), caller(r).Origin, req.ConversationID)
	if err != nil {
		httpx.Error(w, http.StatusForbidden, "no turn for this conversation")
		return
	}
	httpx.JSON(w, http.StatusOK, grant)
}

// --- client side ---

// ProvisionRemoteMirror tells a peer host to stand up a mirror for one of its users.
func (c *Client) ProvisionRemoteMirror(ctx context.Context, peerDomain string, spec MirrorSpec) error {
	return c.PostJSON(ctx, c.PeerURL(peerDomain)+"/federation/v1/conversation-provision", spec, nil)
}

// DeleteConversationOnPeer asks peerDomain to delete its copy of a conversation
// this host has just deleted, so the deletion reaches both sides.
func (c *Client) DeleteConversationOnPeer(ctx context.Context, peerDomain, conversationID string) error {
	return c.PostJSON(ctx, c.PeerURL(peerDomain)+"/federation/v1/conversation-delete",
		map[string]string{"conversationId": conversationID}, nil)
}

// RelayToPeer relays accepted messages to a participant host's mirror.
func (c *Client) RelayToPeer(ctx context.Context, peerDomain, conversationID string, msgs []RelayedMessage) error {
	return c.PostJSON(ctx, c.PeerURL(peerDomain)+"/federation/v1/conversation-relay",
		map[string]any{"conversationId": conversationID, "messages": msgs}, nil)
}

// SubmitMessageToHub forwards a local device's message to the conversation's hub.
func (c *Client) SubmitMessageToHub(ctx context.Context, hubDomain string, s SubmittedMessage) (RelayedMessage, error) {
	var out RelayedMessage
	err := c.PostJSON(ctx, c.PeerURL(hubDomain)+"/federation/v1/conversation-submit-message", s, &out)
	return out, err
}

// SubmitCommitToHub forwards a local device's commit to the conversation's hub.
func (c *Client) SubmitCommitToHub(ctx context.Context, hubDomain string, s SubmittedCommit) (CommitResult, error) {
	var out CommitResult
	err := c.PostJSON(ctx, c.PeerURL(hubDomain)+"/federation/v1/conversation-submit-commit", s, &out)
	return out, err
}

// SubmitReceiptToHub forwards a local member's receipt watermarks to the hub.
func (c *Client) SubmitReceiptToHub(ctx context.Context, hubDomain string, s ReceiptUpdate) error {
	return c.PostJSON(ctx, c.PeerURL(hubDomain)+"/federation/v1/conversation-submit-receipt", s, nil)
}

// RelayReceiptToPeer relays a member's advanced watermarks to a participant host.
func (c *Client) RelayReceiptToPeer(ctx context.Context, peerDomain string, s ReceiptUpdate) error {
	return c.PostJSON(ctx, c.PeerURL(peerDomain)+"/federation/v1/conversation-relay-receipt", s, nil)
}

// RelayCallSignalToPeer sends a local member's sealed call signal to a participant host.
func (c *Client) RelayCallSignalToPeer(ctx context.Context, peerDomain string, s CallSignalRelay) error {
	return c.PostJSON(ctx, c.PeerURL(peerDomain)+"/federation/v1/conversation-call-signal", s, nil)
}

// RelayCallNudgeToPeer re-nudges a participant host's members about a ringing call.
func (c *Client) RelayCallNudgeToPeer(ctx context.Context, peerDomain string, s CallNudge) error {
	return c.PostJSON(ctx, c.PeerURL(peerDomain)+"/federation/v1/conversation-call-nudge", s, nil)
}

// RequestTurnFromPeer asks a participant host for a TURN credential so a cross-host
// call can relay through it.
func (c *Client) RequestTurnFromPeer(ctx context.Context, peerDomain, conversationID string) (TurnGrant, error) {
	var out TurnGrant
	err := c.PostJSON(ctx, c.PeerURL(peerDomain)+"/federation/v1/conversation-turn",
		map[string]any{"conversationId": conversationID}, &out)
	return out, err
}
