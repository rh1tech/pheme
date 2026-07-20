package channel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
)

// RemoteChannels is what the app handler needs to subscribe a local user to a
// channel on another host. Kept as a small interface so this package does not
// depend on the federation client concretely, and so the whole feature is nil
// (and the endpoint 404s) on a host that does not federate.
type RemoteChannels interface {
	// IsPeer reports whether a domain is a trusted host in the network.
	IsPeer(domain string) bool
	// SubscribeToRemoteChannel tells the origin host that this host now has a
	// subscriber for one of its channels, and returns the channel's display name.
	SubscribeToRemoteChannel(ctx context.Context, originDomain, channelPublicID string) (string, error)
}

// joinRemoteChannel subscribes the caller to a channel that lives on another
// host. The reference is `channelPublicId@host` — the origin's public id and its
// domain — because a phetag is only unique within a host, but a public id plus a
// domain is unique across the network.
//
// It creates (or reuses) a local MIRROR of the remote channel and joins the user
// to it, so from here on everything — listing, notifications, the live stream —
// runs against the mirror exactly as for a native channel. New messages arrive
// by fan-out from the origin rather than a local publish.
func (h *AppHandler) joinRemoteChannel(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	// Federation not configured, or this build has no remote support: the
	// endpoint simply does not exist as far as a caller can tell.
	if h.Remote == nil {
		httpx.Error(w, http.StatusNotFound, "not found")
		return
	}

	var req struct {
		Ref      string `json:"ref"`      // "ch_abc123@peer.example"
		DeviceID string `json:"deviceId"` // optional, to subscribe this device
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	publicID, host, found := strings.Cut(strings.TrimSpace(req.Ref), "@")
	if !found || publicID == "" || host == "" {
		httpx.Error(w, http.StatusBadRequest, "ref must be channelPublicId@host")
		return
	}
	host = strings.ToLower(host)

	if host == h.HostDomain {
		// A local channel; this is the wrong door. Point the caller at the
		// ordinary join rather than mirroring our own channel to ourselves.
		httpx.Error(w, http.StatusBadRequest, "that channel is local — use join")
		return
	}
	if !h.Remote.IsPeer(host) {
		httpx.Error(w, http.StatusNotFound, "unknown host")
		return
	}

	// Tell the origin we have a subscriber. This both authorises the mirror (the
	// origin refuses if the channel is missing or not federatable) and gets the
	// channel's real name for the mirror.
	name, err := h.Remote.SubscribeToRemoteChannel(r.Context(), host, publicID)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "channel not available on that host")
		return
	}

	// Reuse an existing mirror rather than making a second one for the same
	// remote channel.
	mirror, err := h.Store.ChannelByOriginPublicID(r.Context(), host, publicID)
	if err != nil {
		mirror, err = h.Store.CreateChannel(r.Context(), domain.Channel{
			PublicID:         newMirrorPublicID(),
			Name:             name,
			OwnerID:          uid, // the subscriber "owns" their local mirror
			SubscriptionMode: domain.ModeOpen,
			Status:           domain.ChannelActive,
			OriginDomain:     host,
			OriginPublicID:   publicID,
			CreatedAt:        time.Now().UTC(),
		})
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "could not create channel")
			return
		}
	}

	mem, err := h.join(r.Context(), uid, mirror, req.DeviceID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not join channel")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"channel": mirror, "membership": mem})
}

// newMirrorPublicID mints a local public id for a mirror channel, distinct from
// the origin's so it never collides under the global unique index.
func newMirrorPublicID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "ch_" + hex.EncodeToString(b)
}
