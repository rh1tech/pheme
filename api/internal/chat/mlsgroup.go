package chat

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
	"github.com/rh1tech/pheme/api/internal/live"
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
	msgs, err := h.Store.MLSControlMessagesSince(r.Context(), convID, since)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load commits")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"messages": msgs})
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
			CreatedAt:      now,
		})
	}
	msgs = append(msgs, domain.ChatMessage{
		ConversationID: convID,
		SenderID:       uid,
		Ciphertext:     req.Commit,
		ContentType:    contentTypeMLSCommit,
		MLSEpoch:       epoch,
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
	httpx.JSON(w, http.StatusOK, state)
}
