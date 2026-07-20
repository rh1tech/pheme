package federation

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rh1tech/pheme/api/internal/httpx"
)

// ChannelService is what the federation handler needs from the rest of the
// system to serve cross-host channels. It is deliberately small: the origin
// records a peer's interest, the subscriber delivers an arriving message.
type ChannelService interface {
	// RecordRemoteSubscriber runs on the CHANNEL'S host. It notes that the
	// calling peer has a subscriber to the given local channel, so future
	// messages fan out to that peer. Returns the channel's display name so the
	// subscriber's mirror can show something real, or an error if the channel
	// does not exist or does not accept remote subscribers.
	RecordRemoteSubscriber(ctx context.Context, channelPublicID, peerDomain string) (name string, err error)

	// DeliverRemoteMessage runs on the SUBSCRIBER'S host. It routes a message
	// arriving from the origin peer to the local mirror channel and fans it out
	// to local subscribers exactly as a native message would be.
	DeliverRemoteMessage(ctx context.Context, originDomain string, msg RemoteMessage) error
}

// RemoteMessage is one channel broadcast, on the wire between hosts. Plaintext,
// like every channel message — the server reads channel content, unlike chat.
// Images are omitted in this first version: their bytes live in the origin's
// blob store and would need a qualified URL or a proxy, which title-and-body
// delivery does not (see docs/federation.md).
type RemoteMessage struct {
	ChannelPublicID string    `json:"channelPublicId"`
	Title           string    `json:"title"`
	Body            string    `json:"body"`
	CreatedAt       time.Time `json:"createdAt"`
}

// registerChannels adds the channel S2S routes. Called from Register when a
// ChannelService is wired in.
func (h *Handler) registerChannels(mux *http.ServeMux) {
	mux.Handle("POST /federation/v1/channel-subscribe", h.verified(http.HandlerFunc(h.channelSubscribe)))
	mux.Handle("POST /federation/v1/channel-delivery", h.verified(http.HandlerFunc(h.channelDelivery)))
}

// channelSubscribe runs on the channel's origin host. A peer tells us one of its
// users wants a local channel; we record the peer so the dispatcher fans out to
// it. We learn WHICH peer wants it — that is the routing we need — and never
// which of their users, which is the peer's business and not ours to hold.
func (h *Handler) channelSubscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChannelPublicID string `json:"channelPublicId"`
	}
	if err := json.Unmarshal(verifiedBody(r), &req); err != nil || req.ChannelPublicID == "" {
		httpx.Error(w, http.StatusBadRequest, "channelPublicId required")
		return
	}
	name, err := h.Channels.RecordRemoteSubscriber(r.Context(), req.ChannelPublicID, caller(r).Origin)
	if err != nil {
		// One opaque status: a peer learns "no" without learning whether the
		// channel is missing, private, or approval-gated.
		httpx.Error(w, http.StatusNotFound, "channel not available")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"name": name})
}

// channelDelivery runs on the subscriber's host. The origin peer hands us a new
// message for a channel we mirror; we deliver it to our local subscribers. The
// caller is proven, so the message is attributed to the right origin without
// trusting a field in the body.
func (h *Handler) channelDelivery(w http.ResponseWriter, r *http.Request) {
	var msg RemoteMessage
	if err := json.Unmarshal(verifiedBody(r), &msg); err != nil || msg.ChannelPublicID == "" {
		httpx.Error(w, http.StatusBadRequest, "malformed message")
		return
	}
	if err := h.Channels.DeliverRemoteMessage(r.Context(), caller(r).Origin, msg); err != nil {
		// We do not mirror this channel from this origin, or delivery failed.
		httpx.Error(w, http.StatusNotFound, "no such channel")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"delivered": true})
}

// PeerBaseURL builds the https base URL for a peer domain's federation endpoints.
// A peer is reached over ordinary HTTPS at its domain; the specific paths come
// from the RemoteMessage/subscribe endpoints, which are stable and need no
// per-peer discovery for these first calls.
func PeerBaseURL(domain string) string { return "https://" + domain }

// SubscribeToRemoteChannel tells the origin host that this host has a subscriber
// for one of its channels, and returns the channel's display name.
func (c *Client) SubscribeToRemoteChannel(ctx context.Context, originDomain, channelPublicID string) (string, error) {
	var out struct {
		Name string `json:"name"`
	}
	err := c.PostJSON(ctx, c.PeerURL(originDomain)+"/federation/v1/channel-subscribe",
		map[string]string{"channelPublicId": channelPublicID}, &out)
	return out.Name, err
}

// DeliverToPeer hands a new channel message to a subscriber host. Fire-and-
// forget from the dispatcher's point of view: a peer that is down must not hold
// up local delivery, so the caller logs a failure rather than retrying here.
func (c *Client) DeliverToPeer(ctx context.Context, peerDomain string, msg RemoteMessage) error {
	return c.PostJSON(ctx, c.PeerURL(peerDomain)+"/federation/v1/channel-delivery", msg, nil)
}
