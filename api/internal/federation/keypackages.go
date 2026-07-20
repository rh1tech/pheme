package federation

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/rh1tech/pheme/api/internal/httpx"
)

// KeyPackageService is what the federation handler needs to serve a peer's
// request for one of THIS host's users' MLS key packages — the material a
// remote hub needs to add that user to a group. Kept minimal, like the other
// federation services.
type KeyPackageService interface {
	// DevicesWithKeyPackages lists the device ids of a local user that have
	// published claimable key packages.
	DevicesWithKeyPackages(ctx context.Context, userID string) ([]string, error)
	// ClaimKeyPackage removes and returns one key package for a device, falling
	// back to the reusable last-resort package when the single-use stock is
	// spent. Returns nil bytes if the device has published nothing at all.
	ClaimKeyPackage(ctx context.Context, userID, deviceID string) ([]byte, error)
}

// ClaimedKeyPackage is one device's key package, on the wire.
type ClaimedKeyPackage struct {
	DeviceID   string `json:"deviceId"`
	KeyPackage []byte `json:"keyPackage"`
}

func (h *Handler) registerKeyPackages(mux *http.ServeMux) {
	mux.Handle("POST /federation/v1/claim-key-packages", h.verified(http.HandlerFunc(h.claimKeyPackages)))
}

// claimKeyPackages runs on the home host of the user being added. A peer hub
// asks for key packages to add one of our users to a group it hosts; we hand
// back one package per device.
//
// The caller is a nodelist-proven peer, which is the trust boundary: key
// packages are single-use public keys meant to be claimed, and every device
// keeps a reusable last-resort package as a floor, so a peer draining the
// single-use stock cannot lock the user out — it only forces the last-resort
// path, exactly as an over-eager local claimer would.
func (h *Handler) claimKeyPackages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(verifiedBody(r), &req); err != nil || req.UserID == "" {
		httpx.Error(w, http.StatusBadRequest, "userId required")
		return
	}

	devices, err := h.KeyPackages.DevicesWithKeyPackages(r.Context(), req.UserID)
	if err != nil || len(devices) == 0 {
		// Either the user does not exist here or has opened no device that
		// publishes keys. A peer learns only that there is nothing to claim.
		httpx.Error(w, http.StatusNotFound, "no key packages for that user")
		return
	}

	out := make([]ClaimedKeyPackage, 0, len(devices))
	for _, deviceID := range devices {
		kp, err := h.KeyPackages.ClaimKeyPackage(r.Context(), req.UserID, deviceID)
		if err != nil || len(kp) == 0 {
			continue // that device is unreachable; the others still stand
		}
		out = append(out, ClaimedKeyPackage{DeviceID: deviceID, KeyPackage: kp})
	}
	if len(out) == 0 {
		httpx.Error(w, http.StatusNotFound, "no key packages available")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"keyPackages": out})
}

// ClaimRemoteKeyPackages asks a peer host for key packages to add one of its
// users to a group. The returned packages are for that user's devices, each to
// become a leaf.
func (c *Client) ClaimRemoteKeyPackages(ctx context.Context, homeDomain, userID string) ([]ClaimedKeyPackage, error) {
	var out struct {
		KeyPackages []ClaimedKeyPackage `json:"keyPackages"`
	}
	err := c.PostJSON(ctx, c.PeerURL(homeDomain)+"/federation/v1/claim-key-packages",
		map[string]string{"userId": userID}, &out)
	return out.KeyPackages, err
}
