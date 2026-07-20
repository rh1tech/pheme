package chat

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/mlswire"
	"github.com/rh1tech/pheme/api/internal/store"
)

// The MLS group behind a conversation.
//
// The server is an untrusted Delivery Service: it never sees a key, and every byte it
// relays here is opaque to it. But it is the only party all the members agree on, so
// two questions can only be answered here:
//
//   - "Which group IS this conversation?" Without a single recorded answer, two devices
//     of the same person each create their own group under the conversation's name and
//     then encrypt past each other forever.
//   - "Whose Commit came first?" Two members who Commit against the same epoch produce
//     two incompatible next epochs, and the group forks in half.
//
// Both are answered by one compare-and-set (store.CommitMLSGroup), which is also what
// relays the Welcome and Commit — so a Commit the group refuses is never seen by anyone.

// getMLSGroup reports the conversation's group id and epoch, so a member can tell
// whether the group exists yet, whether it holds the same group they do, and what epoch
// to base a Commit on.
func (h *Handler) getMLSGroup(w http.ResponseWriter, r *http.Request) {
	_, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	state, err := h.Store.MLSGroupState(r.Context(), convID)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "conversation not found")
		return
	}
	httpx.JSON(w, http.StatusOK, state)
}

type mlsGroupInfoRequest struct {
	GroupID   string `json:"groupId"`
	Epoch     int64  `json:"epoch"`
	GroupInfo []byte `json:"groupInfo"`
}

// postMLSGroupInfo records the GroupInfo a member exported after a Commit, so a new device can join
// the group by external commit without waiting to be admitted. Members only (requireMember), which is
// the whole safety of external join: only someone entitled to be in the group can supply — or later
// fetch — the material to join it.
func (h *Handler) postMLSGroupInfo(w http.ResponseWriter, r *http.Request) {
	_, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	var req mlsGroupInfoRequest
	if !httpx.DecodeLimited(w, r, &req, 2*maxCiphertextBytes) {
		return
	}
	if req.GroupID == "" {
		httpx.Error(w, http.StatusBadRequest, "groupId is required")
		return
	}
	if len(req.GroupInfo) == 0 || len(req.GroupInfo) > maxCiphertextBytes {
		httpx.Error(w, http.StatusBadRequest, "groupInfo is required")
		return
	}
	if req.Epoch < 0 {
		httpx.Error(w, http.StatusBadRequest, "epoch must not be negative")
		return
	}
	if err := h.Store.SetMLSGroupInfo(r.Context(), convID, req.GroupID, req.Epoch, req.GroupInfo); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not store group info")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getMLSGroupInfo returns the latest GroupInfo for the conversation's current group, for a member
// that holds no leaf yet and wants to external-join. 404 when none has been published — a real
// answer that tells the client to fall back to announcing itself and waiting.
func (h *Handler) getMLSGroupInfo(w http.ResponseWriter, r *http.Request) {
	_, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	gi, err := h.Store.MLSGroupInfo(r.Context(), convID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "no group info published yet")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load group info")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"groupId":   gi.GroupID,
		"epoch":     gi.Epoch,
		"groupInfo": gi.GroupInfo,
	})
}

// listMLSCommits returns the control messages (Welcomes and Commits) that carried the
// group past `since`, oldest first — exactly what a member holding an older epoch needs
// to apply, in the order it must apply them.
//
// This is the difference between a client that recovers and one that does not. A device
// that was closed while the group changed cannot decrypt anything until it applies the
// Commits it slept through, and those Commits may be far outside the page of history it
// loads on open. Asking for them by epoch is bounded and exact.
func (h *Handler) listMLSCommits(w http.ResponseWriter, r *http.Request) {
	_, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	var since int64
	if v := r.URL.Query().Get("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			httpx.Error(w, http.StatusBadRequest, "since must be a non-negative epoch")
			return
		}
		since = n
	}
	// Scoped to the CURRENT group. An epoch is unique only within a group, and a re-established
	// conversation starts counting again — so without this the retired group's history comes back
	// alongside the live one, sharing epoch numbers, and the caller applies both.
	state, err := h.Store.MLSGroupState(r.Context(), convID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load commits")
		return
	}
	msgs, err := h.Store.MLSControlMessagesSince(r.Context(), convID, state.GroupID, since)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load commits")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

// listHistoryOffers hands back the recent history offers in a conversation, so a device that asked
// for its history can find the answer even if it was not connected when the answer was posted.
//
// Offers are protocol traffic and are therefore absent from the transcript. That left exactly one
// delivery route — the live stream, at the instant of posting — and a restored device that missed
// it showed a blank history until it happened to ask again.
//
// Every member sees every offer, and that is safe: the transcript inside is sealed under a key
// derived from the group AND bound to the requesting device, so an offer opens for the one device
// it was addressed to and for nobody else, this server included.
func (h *Handler) listHistoryOffers(w http.ResponseWriter, r *http.Request) {
	_, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}
	offers, err := h.Store.MLSHistoryOffers(r.Context(), convID, limit)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load history offers")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"messages": offers})
}

// mayRemove applies the roster's own rule — "only a group admin can remove members" — to
// the Commits that change the encrypted group, so the two cannot disagree about who is
// allowed to throw somebody out.
//
// Three things are allowed for anyone: a Commit that removes nobody; removing yourself
// (which is not a Commit at all in MLS, but a client may declare it); and a removal in a
// direct chat, where there is no admin and the only removals are ghost-device pruning.
//
// See mlsCommitRequest.Removes for what this does and does not actually guarantee: the
// Commit is opaque, so this is the roster's rule applied to a claim the client makes about
// itself, not a check on the cryptography.
func (h *Handler) mayRemove(r *http.Request, convID string, member domain.ConversationMember, removes []string) bool {
	if len(removes) == 0 {
		return true
	}
	conv, err := h.Store.ConversationByID(r.Context(), convID)
	if err != nil {
		return false
	}
	if conv.Kind != domain.ConversationGroup {
		return true
	}
	if member.Role == domain.RoleAdmin {
		return true
	}
	// A non-admin may still prune leaves that are only their own.
	for _, uid := range removes {
		if uid != member.UserID {
			return false
		}
	}
	return true
}

// postMLSReset retires a group nobody can use, so the conversation can start a new one.
//
// A conversation's group can die outright. Every device that held it can lose its key material
// — a browser cleared, an iOS PWA whose storage was evicted on the seven-day rule — and there
// is no law saying that cannot happen to both people in the same week. Admission is a Commit,
// and only a member of the group can make one, so once nobody holds it there is nobody left who
// can let anybody in. Every device announces itself and waits, forever, and the conversation is
// dead with no way back.
//
// This is the way back. It retires the group and REMEMBERS it: anyone who still holds it can
// still read everything that was said to it, and a client decrypts each message against
// whichever of its groups that message belongs to. Nothing is destroyed — which is the whole
// difference between this and the "rebuild the group" behaviour it replaces, which deleted the
// old group and took every message in the conversation down with it.
//
// Because it destroys nothing, it does not need to be guarded by proof that the group is really
// dead — proof we could not obtain anyway, since the server cannot tell "nobody holds the key"
// from "nobody is online". A client only calls it after waiting to be let in and giving up, and
// the worst a spurious call can do is make everyone rejoin a fresh group.
func (h *Handler) postMLSReset(w http.ResponseWriter, r *http.Request) {
	uid, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	if h.Limiter != nil && !h.Limiter.Allow("mlsreset:"+uid) {
		httpx.Error(w, http.StatusTooManyRequests, "slow down")
		return
	}
	state, err := h.Store.ResetMLSGroup(r.Context(), convID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not reset the group")
		return
	}
	h.logger().Warn("mls group retired: no member could admit a waiting device",
		"conversation", convID, "by", uid, "retired", state.PriorGroupIDs)
	httpx.JSON(w, http.StatusOK, state)
}

type mlsCommitRequest struct {
	// GroupID is the MLS group this Commit belongs to. On the very first Commit it is
	// the id the establishing member minted; thereafter it must match what is recorded,
	// so a member holding a stale group from a previous fork cannot commit into the
	// live one.
	GroupID string `json:"groupId"`
	// BaseEpoch is the epoch the sender's group was at when it BUILT this Commit. The
	// server accepts the Commit only if the conversation is still at that epoch.
	BaseEpoch int64 `json:"baseEpoch"`
	// Welcome admits new devices; Commit advances everyone already in the group. Both
	// are opaque. A Commit that adds nobody (a removal) carries no Welcome.
	Welcome []byte `json:"welcome,omitempty"`
	Commit  []byte `json:"commit"`
	// Removes names the users whose leaves this Commit takes out of the group, so the
	// server can apply the same rule to the encrypted group that it applies to the
	// roster: in a group conversation, only an admin may remove anybody else.
	//
	// READ THIS BEFORE TRUSTING IT. The Commit itself is opaque — OpenMLS sends handshake
	// messages as PrivateMessage, so the server cannot see what is inside one and cannot
	// verify that this field describes it. A client that lies here, or omits it, still
	// gets its Commit accepted. This check therefore stops the honest paths (the UI, a
	// buggy client) from doing something the roster forbids; it does NOT stop a member who
	// has modified their client from evicting another member's devices.
	//
	// The bound on that residual risk is reconciliation: the victim stays on the roster,
	// so the next member to open the conversation adds their devices straight back
	// (reconcileDevices in web/src/lib/mls.ts). An attacker can force repeated re-adds —
	// a nuisance and a message-loss window for the target — but not a durable eviction,
	// and they are a member of the group already, so they could read those messages
	// anyway. Closing it properly means moving handshake messages to PublicMessage framing
	// so the server can parse the Remove proposals and enforce this itself; that is the
	// right fix and it is not done here.
	Removes []string `json:"removes,omitempty"`
}

// postMLSCommit accepts a membership Commit, but only if it is based on the epoch the
// conversation is actually at — and relays it in the same atomic step.
//
// A 409 is not a failure the caller should retry blindly: it means another member's
// Commit landed first, so this one is now built on a history that never happened. The
// caller must throw its Commit away (never apply it locally — that is what forks a
// client off the group for good), apply the winning Commit, and propose again. The
// current state comes back in the response so it can.
func (h *Handler) postMLSCommit(w http.ResponseWriter, r *http.Request) {
	uid, convID, member, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	var req mlsCommitRequest
	if !httpx.DecodeLimited(w, r, &req, 2*maxCiphertextBytes) {
		return
	}
	if req.GroupID == "" {
		httpx.Error(w, http.StatusBadRequest, "groupId is required")
		return
	}
	if !h.mayRemove(r, convID, member, req.Removes) {
		httpx.Error(w, http.StatusForbidden, "only a group admin can remove members")
		return
	}
	if len(req.Commit) == 0 || len(req.Commit) > maxCiphertextBytes {
		httpx.Error(w, http.StatusBadRequest, "a commit is required")
		return
	}
	if len(req.Welcome) > maxCiphertextBytes {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "welcome too large")
		return
	}
	if req.BaseEpoch < 0 {
		httpx.Error(w, http.StatusBadRequest, "baseEpoch must not be negative")
		return
	}

	// Opportunistically verify the commit's own epoch against the declared one.
	//
	// Handshake messages are PublicMessage now (see the pheme-mls crate), so the
	// epoch a Commit is built on is in the clear. When we CAN read it, it must
	// match req.BaseEpoch — otherwise the compare-and-set below would serialise
	// the group on a number the commit does not actually support, which is the
	// exact lie the old PrivateMessage framing could not catch.
	//
	// When we cannot read it — a PrivateMessage from a client that has not
	// adopted the new framing (MIXED allows both during the rollout), or anything
	// this minimal parser does not understand — the commit proceeds on its
	// declared value, exactly as before F4. That is not a regression: the CAS
	// still guards ordering, and a client that forged an epoch it cannot back up
	// produces a commit the other members reject on apply. The new guarantee is
	// strictly additional: a real, parseable commit can no longer lie about its
	// epoch.
	if parsed, err := mlswire.ParseHandshake(req.Commit); err == nil {
		if parsed.ContentType != mlswire.ContentTypeCommit {
			httpx.Error(w, http.StatusBadRequest, "handshake is not a commit")
			return
		}
		if int64(parsed.Epoch) != req.BaseEpoch {
			httpx.Error(w, http.StatusBadRequest, "commit epoch does not match baseEpoch")
			return
		}
	}

	now := time.Now().UTC()
	// Stamped with the epoch this Commit produces, so a member who has fallen behind can
	// ask for exactly the Commits it is missing rather than hunting through the log.
	epoch := req.BaseEpoch + 1
	// The Welcome goes FIRST. A device that is being added reads the log in order, and a
	// Commit it is not yet a member of is noise to it; the Welcome is what lets it in.
	msgs := make([]domain.ChatMessage, 0, 2)
	if len(req.Welcome) > 0 {
		msgs = append(msgs, domain.ChatMessage{
			ConversationID: convID,
			SenderID:       uid,
			Ciphertext:     req.Welcome,
			ContentType:    contentTypeMLSWelcome,
			MLSEpoch:       epoch,
			// Which group this belongs to. An epoch is unique only within a group, and a
			// re-established conversation restarts the count — see domain.ChatMessage.MLSGroupID.
			MLSGroupID: req.GroupID,
			CreatedAt:  now,
		})
	}
	msgs = append(msgs, domain.ChatMessage{
		ConversationID: convID,
		SenderID:       uid,
		Ciphertext:     req.Commit,
		ContentType:    contentTypeMLSCommit,
		MLSEpoch:       epoch,
		MLSGroupID:     req.GroupID,
		CreatedAt:      now,
	})

	state, stored, err := h.Store.CommitMLSGroup(r.Context(), convID, req.GroupID, req.BaseEpoch, msgs)
	if errors.Is(err, store.ErrEpochConflict) {
		// Somebody else got there first. Hand back where the group actually is, so the
		// caller can catch up instead of guessing.
		httpx.JSON(w, http.StatusConflict, state)
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not commit")
		return
	}

	// Only now — with the Commit accepted as the group's real next epoch — does anyone
	// hear about it.
	for i := range stored {
		h.Live.Publish(live.Event{ConversationID: convID, ChatMessage: &stored[i]})
	}

	// The tripwire. A group that is committing this fast is at war with itself — clients
	// feeding each other's reconcile loops — and it looks like perfect health from every
	// endpoint: every Commit here was individually well-formed and accepted.
	if n, storming := h.storms().Observe(convID, now); storming {
		h.logger().Error("mls commit storm",
			"conversationId", convID,
			"commits", n,
			"window", stormAlarmWindow.String(),
			"epoch", epoch,
		)
	}

	httpx.JSON(w, http.StatusOK, state)
}
