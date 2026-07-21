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
}

// MirrorSpec describes the conversation a follower should stand up.
type MirrorSpec struct {
	ConversationID string         `json:"conversationId"`
	Kind           string         `json:"kind"`
	Title          string         `json:"title,omitempty"`
	LocalUserID    string         `json:"localUserId"` // the member who lives on the receiving host
	RemoteMembers  []RemoteMember `json:"remoteMembers"`
	GroupState     *MirrorGroupSt `json:"groupState,omitempty"`
}

// RemoteMember is one member who lives on another host.
type RemoteMember struct {
	UserID string `json:"userId"`
	Domain string `json:"domain"`
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

// --- client side ---

// ProvisionRemoteMirror tells a peer host to stand up a mirror for one of its users.
func (c *Client) ProvisionRemoteMirror(ctx context.Context, peerDomain string, spec MirrorSpec) error {
	return c.PostJSON(ctx, c.PeerURL(peerDomain)+"/federation/v1/conversation-provision", spec, nil)
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
