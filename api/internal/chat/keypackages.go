package chat

import (
	"context"
	"net/http"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
)

// Bytes per KeyPackage, and how many a device may publish at once. A KeyPackage
// is a few hundred bytes; these bounds keep the directory from being abused.
const (
	maxKeyPackageBytes = 16 * 1024
	maxKeyPackageBatch = 100
)

type publishKeyPackagesRequest struct {
	DeviceID string `json:"deviceId"`
	// Each entry is base64 of an opaque public KeyPackage.
	KeyPackages [][]byte `json:"keyPackages"`
	// The device's reusable last-resort KeyPackage, if it is publishing one.
	//
	// Which KeyPackage is last-resort is decided ON THE CLIENT, not here: it is a
	// property of the bytes (an RFC 9420 extension the client sets when building it),
	// and it is what makes the client keep the private key instead of deleting it
	// after first use. A flag invented server-side would be pure bookkeeping — the
	// package would still be single-use, and the user could still be drained.
	LastResortKeyPackage []byte `json:"lastResortKeyPackage,omitempty"`
}

// publishKeyPackages stores a batch of the caller's public KeyPackages so others
// can add them to encrypted groups. Public bytes only; no private material.
func (h *Handler) publishKeyPackages(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	var req publishKeyPackagesRequest
	// A full batch is maxKeyPackageBatch × maxKeyPackageBytes, base64-encoded; the
	// ceiling leaves room for that plus the JSON around it.
	if !httpx.DecodeLimited(w, r, &req, 2*maxKeyPackageBatch*maxKeyPackageBytes) {
		return
	}
	if req.DeviceID == "" {
		httpx.Error(w, http.StatusBadRequest, "deviceId is required")
		return
	}
	if len(req.KeyPackages) > maxKeyPackageBatch {
		httpx.Error(w, http.StatusBadRequest, "too many key packages")
		return
	}
	if len(req.KeyPackages) == 0 && len(req.LastResortKeyPackage) == 0 {
		httpx.Error(w, http.StatusBadRequest, "nothing to publish")
		return
	}

	now := time.Now().UTC()
	packages := make([]domain.MLSKeyPackage, 0, len(req.KeyPackages)+1)

	if len(req.LastResortKeyPackage) > 0 {
		if len(req.LastResortKeyPackage) > maxKeyPackageBytes {
			httpx.Error(w, http.StatusBadRequest, "invalid key package size")
			return
		}
		// One per device. A second would be stored but never handed out, so refuse it
		// rather than let the directory accumulate packages nobody can reach.
		has, err := h.Store.HasLastResortKeyPackage(r.Context(), uid, req.DeviceID)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "could not store key packages")
			return
		}
		if !has {
			packages = append(packages, domain.MLSKeyPackage{
				UserID:     uid,
				DeviceID:   req.DeviceID,
				KeyPackage: req.LastResortKeyPackage,
				LastResort: true,
				CreatedAt:  now,
			})
		}
	}

	for _, kp := range req.KeyPackages {
		if len(kp) == 0 || len(kp) > maxKeyPackageBytes {
			httpx.Error(w, http.StatusBadRequest, "invalid key package size")
			return
		}
		packages = append(packages, domain.MLSKeyPackage{
			UserID:     uid,
			DeviceID:   req.DeviceID,
			KeyPackage: kp,
			CreatedAt:  now,
		})
	}

	if err := h.Store.AddKeyPackages(r.Context(), packages); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not store key packages")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type claimKeyPackagesRequest struct {
	// The exact devices to claim for. A claim is per DEVICE, never per user: every
	// device of a member is its own MLS leaf, and one that is not in the group cannot
	// decrypt a single message sent to it.
	Devices []deviceRef `json:"devices"`
}

type deviceRef struct {
	UserID   string `json:"userId"`
	DeviceID string `json:"deviceId"`
}

// How many devices one claim may cover. A conversation's members between them have a
// handful of devices; this only stops the endpoint being used to drain the directory.
const maxClaimDevices = 64

// claimKeyPackages hands out one KeyPackage per named DEVICE, so the caller can add
// each of them to the group as its own leaf. Single-use packages are consumed; a
// device that has run out falls back to its reusable last-resort package.
//
// Scoped to a conversation the caller is IN, and to devices belonging to that
// conversation's members. Both halves matter. Unscoped, any signed-in stranger could
// stand in a loop draining a victim's published KeyPackages — never making them
// unreachable, because the last-resort package is never consumed, but permanently
// pinning them to it, so every join reuses one init key and quietly gives up the forward
// secrecy that the single-use packages exist to provide. There is no reason to let
// anyone claim keys for someone they are not talking to.
//
// Devices that have published nothing are simply absent from the response rather than
// failing the whole call: one member who has never opened Pheme must not stop a group
// from being formed with everyone else.
func (h *Handler) claimKeyPackages(w http.ResponseWriter, r *http.Request) {
	_, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	var req claimKeyPackagesRequest
	if !httpx.DecodeLimited(w, r, &req, maxSmallBodyBytes) {
		return
	}
	if len(req.Devices) == 0 || len(req.Devices) > maxClaimDevices {
		httpx.Error(w, http.StatusBadRequest, "between 1 and 64 devices are required")
		return
	}
	members, err := h.memberIDs(r.Context(), convID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load members")
		return
	}

	type claimed struct {
		UserID     string `json:"userId"`
		DeviceID   string `json:"deviceId"`
		KeyPackage []byte `json:"keyPackage"`
	}
	out := make([]claimed, 0, len(req.Devices))
	for _, d := range req.Devices {
		if d.UserID == "" || d.DeviceID == "" {
			httpx.Error(w, http.StatusBadRequest, "each device needs a userId and a deviceId")
			return
		}
		if !members[d.UserID] {
			httpx.Error(w, http.StatusForbidden, "that user is not in this conversation")
			return
		}
		kp, err := h.Store.ClaimKeyPackage(r.Context(), d.UserID, d.DeviceID)
		if err != nil {
			continue // that device has published nothing; the others still stand
		}
		out = append(out, claimed{UserID: d.UserID, DeviceID: d.DeviceID, KeyPackage: kp.KeyPackage})
	}
	if len(out) == 0 {
		// Nobody we asked for is reachable. The caller cannot start an encrypted chat
		// with them until they open Pheme on a device that publishes keys.
		httpx.Error(w, http.StatusNotFound, "no key packages available for those devices")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"keyPackages": out})
}

// listDevices reports which devices each member of a conversation has published
// KeyPackages for, WITHOUT consuming any.
//
// This is what makes device reconciliation possible: a member holding the group needs to
// know which devices ought to be in it before it can add the ones that are not, and it
// cannot find that out by claiming — claiming destroys what it hands back.
//
// Scoped to the conversation's own members, so it is not a directory anyone can walk to
// enumerate a stranger's devices.
func (h *Handler) listDevices(w http.ResponseWriter, r *http.Request) {
	_, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	members, err := h.memberIDs(r.Context(), convID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load members")
		return
	}
	userIDs := make([]string, 0, len(members))
	for uid := range members {
		userIDs = append(userIDs, uid)
	}
	devices, err := h.Store.DevicesWithKeyPackages(r.Context(), userIDs)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not list devices")
		return
	}
	// Always answer for every member, so the caller can tell "no devices" from "not in
	// the response".
	for _, uid := range userIDs {
		if devices[uid] == nil {
			devices[uid] = []string{}
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"devices": devices})
}

// memberIDs is the conversation's membership as a set, for authorization checks.
func (h *Handler) memberIDs(ctx context.Context, convID string) (map[string]bool, error) {
	members, err := h.Store.ConversationMembers(ctx, convID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(members))
	for _, m := range members {
		out[m.UserID] = true
	}
	return out, nil
}

// The sealed backup blob is the whole exported client state; a few groups' ratchet
// state is small, but cap it so the store cannot be filled with junk.
const maxKeyBackupBytes = 512 * 1024

type putKeyBackupRequest struct {
	DeviceID string `json:"deviceId"`
	// All base64 of opaque bytes: the AES-GCM salt, nonce and sealed ciphertext.
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// putKeyBackup stores the caller's encrypted MLS state. The ciphertext is sealed
// client-side under a passphrase-derived key; the server keeps only opaque bytes.
func (h *Handler) putKeyBackup(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	var req putKeyBackupRequest
	if !httpx.DecodeLimited(w, r, &req, 2*maxKeyBackupBytes) {
		return
	}
	if len(req.Salt) == 0 || len(req.Nonce) == 0 || len(req.Ciphertext) == 0 {
		httpx.Error(w, http.StatusBadRequest, "salt, nonce and ciphertext are required")
		return
	}
	if len(req.Ciphertext) > maxKeyBackupBytes {
		httpx.Error(w, http.StatusBadRequest, "backup too large")
		return
	}
	backup := domain.MLSKeyBackup{
		UserID:     uid,
		DeviceID:   req.DeviceID,
		Salt:       req.Salt,
		Nonce:      req.Nonce,
		Ciphertext: req.Ciphertext,
		UpdatedAt:  time.Now().UTC(),
	}
	if err := h.Store.PutKeyBackup(r.Context(), backup); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not store backup")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getKeyBackup returns the caller's sealed backup for client-side recovery. 404
// when none exists (the user never set up a recovery passphrase).
func (h *Handler) getKeyBackup(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	backup, err := h.Store.GetKeyBackup(r.Context(), uid)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "no backup found")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"salt":       backup.Salt,
		"nonce":      backup.Nonce,
		"ciphertext": backup.Ciphertext,
		"updatedAt":  backup.UpdatedAt,
	})
}

// deleteKeyPackages purges everything this device has published. A device that has
// lost its private keys (a wipe, a fresh identity) calls this so its stale public
// packages stop being handed out and stranding whoever claims them.
func (h *Handler) deleteKeyPackages(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	deviceID := r.URL.Query().Get("deviceId")
	if deviceID == "" {
		httpx.Error(w, http.StatusBadRequest, "deviceId is required")
		return
	}
	if err := h.Store.DeleteKeyPackages(r.Context(), uid, deviceID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not delete key packages")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// keyPackageCount lets a device know when to replenish its published packages.
// `count` covers only the single-use ones; `hasLastResort` tells the device whether
// it still needs to publish its one reusable package.
func (h *Handler) keyPackageCount(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	deviceID := r.URL.Query().Get("deviceId")
	if deviceID == "" {
		httpx.Error(w, http.StatusBadRequest, "deviceId is required")
		return
	}
	n, err := h.Store.CountKeyPackages(r.Context(), uid, deviceID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "count failed")
		return
	}
	hasLastResort, err := h.Store.HasLastResortKeyPackage(r.Context(), uid, deviceID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "count failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"count": n, "hasLastResort": hasLastResort})
}
