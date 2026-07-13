package chat

import (
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
}

// publishKeyPackages stores a batch of the caller's public KeyPackages so others
// can add them to encrypted groups. Public bytes only; no private material.
func (h *Handler) publishKeyPackages(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	var req publishKeyPackagesRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.DeviceID == "" {
		httpx.Error(w, http.StatusBadRequest, "deviceId is required")
		return
	}
	if len(req.KeyPackages) == 0 || len(req.KeyPackages) > maxKeyPackageBatch {
		httpx.Error(w, http.StatusBadRequest, "between 1 and 100 key packages")
		return
	}
	packages := make([]domain.MLSKeyPackage, 0, len(req.KeyPackages))
	for _, kp := range req.KeyPackages {
		if len(kp) == 0 || len(kp) > maxKeyPackageBytes {
			httpx.Error(w, http.StatusBadRequest, "invalid key package size")
			return
		}
		packages = append(packages, domain.MLSKeyPackage{
			UserID:     uid,
			DeviceID:   req.DeviceID,
			KeyPackage: kp,
			CreatedAt:  time.Now().UTC(),
		})
	}
	if err := h.Store.AddKeyPackages(r.Context(), packages); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not store key packages")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// claimKeyPackage hands out (and removes) one of a user's KeyPackages, so the
// caller can add them to a group. Single-use: a claimed package is never reused.
func (h *Handler) claimKeyPackage(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	target := r.PathValue("userId")
	kp, err := h.Store.ClaimKeyPackage(r.Context(), target)
	if err != nil {
		// None left: the target has not published, or has run dry. The caller
		// cannot start an encrypted chat until they do.
		httpx.Error(w, http.StatusNotFound, "no key package available for that user")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"keyPackage": kp.KeyPackage})
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
	if !httpx.Decode(w, r, &req) {
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

// keyPackageCount lets a device know when to replenish its published packages.
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
	httpx.JSON(w, http.StatusOK, map[string]any{"count": n})
}
