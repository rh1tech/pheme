package chat

import (
	"context"
	"net/http"
	"strconv"

	"github.com/rh1tech/pheme/api/internal/httpx"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/push"
)

// 1:1 voice call signalling.
//
// The server's entire involvement in a call is here, and it amounts to passing a handful of
// sealed envelopes between two browsers. It cannot read them: they are encrypted under a key
// derived from the conversation's MLS group (RFC 9420's exporter), which the server does not
// have and cannot derive. That is not incidental — the envelopes carry the SDP, the SDP
// carries the DTLS fingerprint, and a server able to rewrite that fingerprint could put
// itself in the middle of the call. Encrypting them is what makes the call as private as the
// chat it was placed from.
//
// Nothing is written to the database. The signals live in Redis for two minutes and then
// they are gone; a call leaves no record at all.
//
// The media never comes near us. It goes peer to peer, and for the minority of pairs who
// cannot reach each other directly, through coturn (see ice.go).

// A signal is a few kilobytes of SDP at most. The cap is generous next to that and small
// next to what someone would like to make the server buffer.
const maxSignalBytes = 16 * 1024

type callSignalRequest struct {
	// Ciphertext is the sealed signal. Opaque: the server relays it and never looks inside.
	Ciphertext []byte `json:"ciphertext"`
	// Ring asks the server to wake the other members' devices with a push. Only the first
	// signal of a call — the invite — sets it; the rest of the exchange happens over the
	// live stream, and pushing for each one would buzz a phone half a dozen times per call.
	//
	// This is the one thing about a call the server does learn: that A is calling B, at a
	// time. It already knows they are in a conversation together, and it cannot be otherwise
	// — something has to wake a sleeping device.
	Ring bool `json:"ring,omitempty"`
	// Cancel says this call has stopped ringing — the caller gave up, or hung up before it was
	// answered. It closes the notification the ring put on the other person's lock screen.
	//
	// Without it a missed call leaves a live-looking ring sitting there, and tapping it
	// deep-links into a call nobody is on any more. The push that takes a notification away is
	// as much a part of ringing as the one that puts it there.
	Cancel bool `json:"cancel,omitempty"`
}

// postCallSignal relays one sealed signalling blob to the conversation's other devices.
func (h *Handler) postCallSignal(w http.ResponseWriter, r *http.Request) {
	uid, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	if h.Mailbox == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "calling is not configured on this server")
		return
	}
	// Signalling is the one place an authenticated user can make the server do work in a
	// tight loop, and it is how you would call-bomb somebody.
	if h.Limiter != nil && !h.Limiter.Allow("call:"+uid) {
		httpx.Error(w, http.StatusTooManyRequests, "slow down")
		return
	}
	callID := r.PathValue("callId")
	if callID == "" {
		httpx.Error(w, http.StatusBadRequest, "callId is required")
		return
	}

	var req callSignalRequest
	if !httpx.DecodeLimited(w, r, &req, 2*maxSignalBytes) {
		return
	}
	if len(req.Ciphertext) == 0 || len(req.Ciphertext) > maxSignalBytes {
		httpx.Error(w, http.StatusBadRequest, "a signal is required")
		return
	}

	signal, err := h.Mailbox.Append(r.Context(), callID, req.Ciphertext)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not relay the signal")
		return
	}

	recipients, err := h.memberIDs(r.Context(), convID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load members")
		return
	}
	to := make([]string, 0, len(recipients))
	for id := range recipients {
		to = append(to, id)
	}

	// The event carries only "call X has a signal N" — a nudge, not the signal itself. The
	// bus is allowed to drop events, and a dropped SDP answer is a call that silently never
	// connects; so the client fetches the signal from the mailbox, where it is ordered and
	// cannot be lost. A dropped nudge costs a few hundred milliseconds, not the call.
	h.Live.Publish(live.Event{
		ConversationID: convID,
		Recipients:     to,
		CallSignal:     &live.CallSignal{CallID: callID, Seq: signal.Seq, FromUserID: uid},
	})

	if req.Ring {
		h.ringMembers(convID, uid, callID, push.KindCall)
	}
	if req.Cancel {
		h.ringMembers(convID, uid, callID, push.KindCallCancel)
	}
	httpx.JSON(w, http.StatusOK, signal)
}

// postCallRing re-nudges the other end while a call is still ringing.
//
// The invite is published once, and if the callee's live stream happens to be down at that
// instant — reconnecting, backgrounded, moving between cells — the ring is simply missed. The
// invite itself is not lost: it sits in the mailbox for two minutes. But nothing ever looked
// at it again, so the call rang out against a device that would have answered.
//
// This re-publishes the nudge, and the nudge is all it does: no signal is appended and no push
// is sent (the push already went out with the invite, and buzzing a phone every few seconds is
// not ringing, it is harassment). The callee refetches the whole mailbox on any nudge, so a
// repeat is idempotent — a device already ringing stays ringing exactly once.
func (h *Handler) postCallRing(w http.ResponseWriter, r *http.Request) {
	uid, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	if h.Mailbox == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "calling is not configured on this server")
		return
	}
	if h.Limiter != nil && !h.Limiter.Allow("call:"+uid) {
		httpx.Error(w, http.StatusTooManyRequests, "slow down")
		return
	}
	callID := r.PathValue("callId")
	if callID == "" {
		httpx.Error(w, http.StatusBadRequest, "callId is required")
		return
	}

	recipients, err := h.memberIDs(r.Context(), convID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load members")
		return
	}
	to := make([]string, 0, len(recipients))
	for id := range recipients {
		to = append(to, id)
	}

	h.Live.Publish(live.Event{
		ConversationID: convID,
		Recipients:     to,
		CallSignal:     &live.CallSignal{CallID: callID, FromUserID: uid},
	})
	w.WriteHeader(http.StatusNoContent)
}

// getCallSignals returns everything the caller has not seen. This is the transport of
// record; SSE is only the nudge that says to come and look.
func (h *Handler) getCallSignals(w http.ResponseWriter, r *http.Request) {
	_, _, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	if h.Mailbox == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "calling is not configured on this server")
		return
	}
	callID := r.PathValue("callId")
	var since int
	if v := r.URL.Query().Get("since"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			httpx.Error(w, http.StatusBadRequest, "since must be a non-negative sequence number")
			return
		}
		since = n
	}
	signals, err := h.Mailbox.Since(r.Context(), callID, since)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load signals")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"signals": signals})
}

type callAcceptRequest struct {
	// DeviceID identifies which of the answering user's devices is picking up.
	DeviceID string `json:"deviceId"`
}

// postCallAccept decides which device answered.
//
// Every device a person is signed in on rings, and exactly one of them must win. This is the
// only server-side lock in the whole feature, and it earns its place: the losing device has
// already opened the microphone, and "somebody else answered" cannot be delivered over a bus
// that is allowed to drop messages. A loser told nothing keeps ringing, with a live mic, until
// a timeout fires. So the answer is decided here, atomically, and both devices are told for
// certain.
func (h *Handler) postCallAccept(w http.ResponseWriter, r *http.Request) {
	_, _, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	if h.Mailbox == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "calling is not configured on this server")
		return
	}
	callID := r.PathValue("callId")

	var req callAcceptRequest
	if !httpx.DecodeLimited(w, r, &req, maxSmallBodyBytes) {
		return
	}
	if req.DeviceID == "" {
		httpx.Error(w, http.StatusBadRequest, "deviceId is required")
		return
	}

	winner, won, err := h.Mailbox.Claim(r.Context(), callID, req.DeviceID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not accept the call")
		return
	}
	if !won {
		// Not an error — it is the answer. This device should stop ringing and put the
		// microphone away.
		httpx.JSON(w, http.StatusConflict, map[string]any{"winner": winner})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"winner": winner})
}

// ringMembers wakes the other members' devices for an incoming call.
//
// Reuses the chat push path, which already fans out to every device a person has — which is
// exactly what ringing means. The notification names the caller and nothing else; the server
// has nothing else to tell, and would not be able to say what the call is about even if it
// wanted to.
func (h *Handler) ringMembers(convID, callerID, callID string, kind push.Kind) {
	if h.Push == nil {
		return
	}
	select {
	case pushSlots <- struct{}{}:
	default:
		h.logger().Warn("call ring: at capacity, notification dropped", "conversation", convID)
		return
	}
	go func() {
		defer func() { <-pushSlots }()
		ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
		defer cancel()
		log := h.logger()

		members, err := h.Store.ConversationMembers(ctx, convID)
		if err != nil {
			log.Error("call ring: load members", "conversation", convID, "error", err)
			return
		}
		recipients := make([]string, 0, len(members))
		for _, m := range members {
			if m.UserID != callerID {
				recipients = append(recipients, m.UserID)
			}
		}
		if len(recipients) == 0 {
			return
		}
		devices, err := h.Store.DevicesForUsers(ctx, recipients)
		if err != nil || len(devices) == 0 {
			return
		}
		if _, err := h.Push.SendChat(ctx, push.ChatNotification{
			ConversationID: convID,
			SenderName:     h.senderName(ctx, callerID),
			Kind:           kind,
			CallID:         callID,
		}, devices); err != nil {
			log.Error("call ring: send", "conversation", convID, "error", err)
		}
	}()
}
