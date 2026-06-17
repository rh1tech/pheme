package channel

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
	"github.com/rh1tech/pheme/api/internal/store"
)

// AuthHandler serves registration and token endpoints.
type AuthHandler struct {
	Store  store.Store
	Tokens *auth.TokenManager
}

// Routes registers the auth endpoints on a mux. These are public (no JWT).
func (h *AuthHandler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/register", h.register)
	mux.HandleFunc("POST /v1/auth/login", h.login)
	mux.HandleFunc("POST /v1/auth/refresh", h.refresh)
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type tokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	UserID       string `json:"userId"`
}

func (h *AuthHandler) register(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !httpx.Decode(w, r, &req) {
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || len(req.Password) < 8 {
		httpx.Error(w, http.StatusBadRequest, "email and password (min 8 chars) are required")
		return
	}
	if _, err := h.Store.UserByEmail(r.Context(), req.Email); err == nil {
		httpx.Error(w, http.StatusConflict, "email already registered")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusInternalServerError, "registration failed")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "registration failed")
		return
	}
	u, err := h.Store.CreateUser(r.Context(), domain.User{
		Email:        req.Email,
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC(),
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "registration failed")
		return
	}
	h.issue(w, u.ID, http.StatusCreated)
}

func (h *AuthHandler) login(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !httpx.Decode(w, r, &req) {
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	u, err := h.Store.UserByEmail(r.Context(), req.Email)
	if err != nil {
		// Do the hash work anyway to reduce timing signal, then fail uniformly.
		_, _ = auth.VerifyPassword(req.Password, dummyHash)
		httpx.Error(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	ok, err := auth.VerifyPassword(req.Password, u.PasswordHash)
	if err != nil || !ok {
		httpx.Error(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	h.issue(w, u.ID, http.StatusOK)
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

func (h *AuthHandler) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	userID, err := h.Tokens.Parse(strings.TrimSpace(req.RefreshToken), auth.RefreshToken)
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	h.issue(w, userID, http.StatusOK)
}

func (h *AuthHandler) issue(w http.ResponseWriter, userID string, status int) {
	access, refresh, err := h.Tokens.Issue(userID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not issue tokens")
		return
	}
	httpx.JSON(w, status, tokenResponse{AccessToken: access, RefreshToken: refresh, UserID: userID})
}

// dummyHash is a precomputed Argon2id hash used to equalise login timing when an
// account does not exist.
const dummyHash = "$argon2id$v=19$m=65536,t=1,p=4$hz8RphtgTpv4ss8V0ETClA$Uzi1z1jG80dujeKb15tZAyoGP30p/FVWDNdKRhx/Ajo"
