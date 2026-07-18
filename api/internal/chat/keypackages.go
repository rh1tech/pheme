package chat

import (
	"context"
	"net/http"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
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
	// A human label for this device — "Chrome on macOS", "Pheme on iPhone" — set by the client,
	// so the user can recognise it in "your devices". Optional; the registry works without it.
	Label string `json:"label,omitempty"`
}

// maxDeviceLabelLen bounds the client-supplied device label so the registry cannot be stuffed.
const maxDeviceLabelLen = 100

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

	// Record this device in the user's own registry (what "your devices" reads), and refresh its
	// last-seen. Best effort — a device is reachable and usable whether or not it is listed, so a
	// registry write must never fail a key publish.
	label := req.Label
	if len(label) > maxDeviceLabelLen {
		label = label[:maxDeviceLabelLen]
	}
	// Bind this device to the session it is authenticating with, so terminating the device
	// later can revoke exactly that login. Empty for a token minted before sessions carried
	// an id — harmless, it just leaves nothing to revoke.
	sid, _ := auth.SessionIDFromContext(r.Context())
	if err := h.Store.UpsertMLSDevice(r.Context(), domain.MLSDevice{
		UserID:     uid,
		DeviceID:   req.DeviceID,
		Label:      label,
		SessionID:  sid,
		CreatedAt:  now,
		LastSeenAt: now,
	}); err != nil {
		h.logger().Error("device registry: upsert", "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// terminateDevice removes one of the caller's OWN devices: it deletes the device's published
// KeyPackages so it cannot be re-added to any group, forgets it from the registry, and revokes
// its auth session so its token stops working. The MLS leaf removal — cutting the device out of
// each conversation it is in — is orchestrated client-side by the terminating device (only a
// group member can author that commit); this endpoint handles the parts the server owns.
//
// The effect on the terminated device: its leaf goes missing from every group (it can no longer
// decrypt new messages) and its next request 401s, so it wipes and signs out. This is the
// lost-or-stolen-device answer, which is why it revokes auth rather than only signing out.
func (h *Handler) terminateDevice(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	deviceID := r.PathValue("deviceId")
	if deviceID == "" {
		httpx.Error(w, http.StatusBadRequest, "deviceId is required")
		return
	}

	// Find the device in the caller's OWN registry — both to confirm ownership (a user can
	// only terminate their own devices) and to learn which session to revoke.
	devices, err := h.Store.ListMLSDevices(r.Context(), uid)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load devices")
		return
	}
	var target *domain.MLSDevice
	for i := range devices {
		if devices[i].DeviceID == deviceID {
			target = &devices[i]
			break
		}
	}
	if target == nil {
		httpx.Error(w, http.StatusNotFound, "no such device")
		return
	}

	// Tombstone FIRST, before anything else is taken away.
	//
	// The order matters more than it looks. Deleting the KeyPackages is what makes a terminated
	// device indistinguishable from one that never published any — and "never published any" is
	// treated by every co-member as "leave its leaf alone". So if the tombstone were written last
	// and failed, the device would be invisible to BOTH signals, which is exactly the broken state
	// this is meant to end. Written first, a failure anywhere later leaves a device that is
	// tombstoned but still has claimable packages, which reconciliation recovers from.
	if err := h.Store.RevokeMLSDevice(r.Context(), uid, deviceID, time.Now().UTC()); err != nil {
		h.logger().Error("terminate device: tombstone", "device", deviceID, "error", err)
		httpx.Error(w, http.StatusInternalServerError, "could not terminate device")
		return
	}

	// Delete its KeyPackages next, so that even if a later step fails the device can no
	// longer be handed out to a group and re-added behind the user's back.
	if err := h.Store.DeleteKeyPackages(r.Context(), uid, deviceID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not terminate device")
		return
	}

	// Revoke its login. Best effort past the KeyPackage delete: if this fails the device is
	// already crypto-severed (no leaf, no keys), and its token dies on its own expiry.
	if h.Revoker != nil && target.SessionID != "" {
		ttl := h.SessionTTL
		if ttl <= 0 {
			ttl = 24 * time.Hour
		}
		if err := h.Revoker.Revoke(r.Context(), target.SessionID, time.Now().Add(ttl)); err != nil {
			h.logger().Error("terminate device: revoke session", "error", err)
		}
	}

	// Take away its push addresses too. Nothing used to, so a terminated device kept its
	// subscription and went on being pushed to — and since previews shipped, those pushes carry the
	// ciphertext of the messages it had just been told it could no longer read. The comment above
	// about being "crypto-severed" was only ever true if the client-side leaf removal succeeded,
	// which it silently does not in several ordinary cases.
	//
	// Best effort, and deliberately after the crypto steps: a failure here leaves a device that is
	// noisy rather than one that is still trusted.
	if removed, err := h.Store.DeletePushDevicesForMLSDevice(r.Context(), uid, deviceID); err != nil {
		h.logger().Error("terminate device: delete push addresses", "device", deviceID, "error", err)
	} else if removed > 0 {
		h.logger().Info("terminate device: push addresses removed", "device", deviceID, "count", removed)
	}

	// The row itself STAYS, as the tombstone written at the top. Forgetting it is what made a
	// revoked device look new, and a device that looks new keeps its leaf in every group.
	w.WriteHeader(http.StatusNoContent)
}

// listMyDevices reports the signed-in user's own devices — id, label, first/last seen — for the
// "your devices" surface. User-scoped (their own only); the device's own id lets the client flag
// which row is "this device".
func (h *Handler) listMyDevices(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	devices, err := h.Store.ListMLSDevices(r.Context(), uid)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not list devices")
		return
	}
	// Terminated devices are kept as tombstones now, so they have to be filtered out here or a
	// device the user just removed would reappear in their own list — which reads as the removal
	// having failed.
	live := make([]domain.MLSDevice, 0, len(devices))
	for _, d := range devices {
		if d.RevokedAt == nil {
			live = append(live, d)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"devices": live})
}

type registerDeviceRequest struct {
	DeviceID string `json:"deviceId"`
	Label    string `json:"label,omitempty"`
}

// registerDevice records the caller's current device in their registry and refreshes its last-seen,
// WITHOUT publishing any key packages.
//
// Registration used to be a side effect of publishing KeyPackages, which only happens when a device's
// stock runs low — so a long-lived, well-stocked device (one that established its identity before the
// registry existed, or simply has not needed to replenish since) never appeared in "your devices",
// including the very device the user is looking at. A device calls this on load instead, so it lists
// itself from the first launch and its last-seen tracks activity rather than the rare replenish.
func (h *Handler) registerDevice(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	var req registerDeviceRequest
	if !httpx.DecodeLimited(w, r, &req, maxSmallBodyBytes) {
		return
	}
	if req.DeviceID == "" {
		httpx.Error(w, http.StatusBadRequest, "deviceId is required")
		return
	}
	label := req.Label
	if len(label) > maxDeviceLabelLen {
		label = label[:maxDeviceLabelLen]
	}
	now := time.Now().UTC()
	sid, _ := auth.SessionIDFromContext(r.Context())
	if err := h.Store.UpsertMLSDevice(r.Context(), domain.MLSDevice{
		UserID:     uid,
		DeviceID:   req.DeviceID,
		Label:      label,
		SessionID:  sid,
		CreatedAt:  now,
		LastSeenAt: now,
	}); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not register device")
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
	// The terminated ones, so co-members can prune their leaves. Sent ALONGSIDE the existing map
	// rather than folded into it: an older client reads only "devices" and is unaffected.
	//
	// A device id is not a secret — it names a leaf that every member of the group can already
	// enumerate from the group state itself.
	revoked, err := h.Store.RevokedDeviceIDs(r.Context(), userIDs)
	if err != nil {
		// Not fatal. Without it a co-member prunes exactly as much as it did before.
		h.logger().Error("list devices: revoked", "error", err)
		revoked = map[string][]string{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"devices": devices, "revoked": revoked})
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

// A single upload's ceiling — a safety bound against a bogus multi-gigabyte body that would
// exhaust memory, NOT a limit on how much history a user may keep. The sealed blobs go to the
// blob store, which has no size ceiling of its own; this only bounds what one request buffers
// while decoding. A whole text history, sealed, is comfortably inside 256MB, and a client that
// somehow exceeds it can back up in a smaller window and grow from there.
const maxBackupUploadBytes = 256 * 1024 * 1024

type putKeyBackupRequest struct {
	DeviceID string `json:"deviceId"`
	// All base64 of opaque bytes: the AES-GCM salt, nonce and sealed ciphertext.
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
	// The sealed transcript cache, optional. Its own salt and nonce: one passphrase,
	// two independent seals, so either blob can be replaced without re-encrypting the
	// other.
	TranscriptSalt       []byte `json:"transcriptSalt,omitempty"`
	TranscriptNonce      []byte `json:"transcriptNonce,omitempty"`
	TranscriptCiphertext []byte `json:"transcriptCiphertext,omitempty"`
}

// putKeyBackup stores the caller's encrypted MLS state, and optionally their sealed
// transcripts. Both ciphertexts are sealed client-side under a passphrase-derived key and
// written to the blob store; this record keeps only their ids, salts and nonces, so there is
// no document-size ceiling on how much a user can back up. The server never sees the
// passphrase or any plaintext.
func (h *Handler) putKeyBackup(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if h.Blobs == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "backups are not available on this server")
		return
	}
	var req putKeyBackupRequest
	if !httpx.DecodeLimited(w, r, &req, maxBackupUploadBytes) {
		return
	}
	if len(req.Salt) == 0 || len(req.Nonce) == 0 || len(req.Ciphertext) == 0 {
		httpx.Error(w, http.StatusBadRequest, "salt, nonce and ciphertext are required")
		return
	}
	// The transcript seal travels whole or not at all: a ciphertext without its salt
	// and nonce can never be opened, and storing it would let a restore silently lose
	// the history it promises.
	hasTranscript := len(req.TranscriptCiphertext) > 0
	if hasTranscript && (len(req.TranscriptSalt) == 0 || len(req.TranscriptNonce) == 0) {
		httpx.Error(w, http.StatusBadRequest, "transcript salt and nonce are required")
		return
	}

	// What is already stored, so its blobs can be deleted once the new ones are safely in.
	// A GetKeyBackup miss is fine — this is the first backup.
	prev, prevErr := h.Store.GetKeyBackup(r.Context(), uid)

	ciphertextBlobID, err := h.Blobs.Put(r.Context(), req.Ciphertext, "application/octet-stream")
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not store backup")
		return
	}
	backup := domain.MLSKeyBackup{
		UserID:           uid,
		DeviceID:         req.DeviceID,
		Salt:             req.Salt,
		Nonce:            req.Nonce,
		CiphertextBlobID: ciphertextBlobID,
		UpdatedAt:        time.Now().UTC(),
	}
	if hasTranscript {
		transcriptBlobID, tErr := h.Blobs.Put(r.Context(), req.TranscriptCiphertext, "application/octet-stream")
		if tErr != nil {
			// The state blob we just wrote is now an orphan; drop it rather than leave it.
			_ = h.Blobs.Delete(r.Context(), ciphertextBlobID)
			httpx.Error(w, http.StatusInternalServerError, "could not store backup")
			return
		}
		backup.TranscriptSalt = req.TranscriptSalt
		backup.TranscriptNonce = req.TranscriptNonce
		backup.TranscriptBlobID = transcriptBlobID
	}

	if err := h.Store.PutKeyBackup(r.Context(), backup); err != nil {
		// Point the record nowhere and the blobs are orphans — clean them up.
		_ = h.Blobs.Delete(r.Context(), backup.CiphertextBlobID)
		if backup.TranscriptBlobID != "" {
			_ = h.Blobs.Delete(r.Context(), backup.TranscriptBlobID)
		}
		httpx.Error(w, http.StatusInternalServerError, "could not store backup")
		return
	}

	// The record now points at the new blobs; the old ones are unreferenced. Delete them —
	// best effort, and only after the swap, so a failure here leaks a blob but never loses a
	// backup. Nothing points at them any more regardless.
	if prevErr == nil {
		if prev.CiphertextBlobID != "" && prev.CiphertextBlobID != backup.CiphertextBlobID {
			_ = h.Blobs.Delete(r.Context(), prev.CiphertextBlobID)
		}
		if prev.TranscriptBlobID != "" && prev.TranscriptBlobID != backup.TranscriptBlobID {
			_ = h.Blobs.Delete(r.Context(), prev.TranscriptBlobID)
		}
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
	if h.Blobs == nil {
		httpx.Error(w, http.StatusNotFound, "no backup found")
		return
	}
	backup, err := h.Store.GetKeyBackup(r.Context(), uid)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "no backup found")
		return
	}
	ciphertext, _, err := h.Blobs.Get(r.Context(), backup.CiphertextBlobID)
	if err != nil {
		// The record survived but its blob did not — treat as no recoverable backup rather
		// than hand back a record whose ciphertext cannot be fetched.
		httpx.Error(w, http.StatusNotFound, "no backup found")
		return
	}
	var transcriptCiphertext []byte
	if backup.TranscriptBlobID != "" {
		if tc, _, tErr := h.Blobs.Get(r.Context(), backup.TranscriptBlobID); tErr == nil {
			transcriptCiphertext = tc
		}
		// A missing transcript blob is not fatal: the keys still restore, the history just
		// does not. Fall through with an empty transcript rather than fail the whole restore.
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"salt":                 backup.Salt,
		"nonce":                backup.Nonce,
		"ciphertext":           ciphertext,
		"transcriptSalt":       backup.TranscriptSalt,
		"transcriptNonce":      backup.TranscriptNonce,
		"transcriptCiphertext": transcriptCiphertext,
		"updatedAt":            backup.UpdatedAt,
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
