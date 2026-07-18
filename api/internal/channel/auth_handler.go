package channel

import (
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/domain"
	mailer "github.com/rh1tech/pheme/api/internal/email"
	"github.com/rh1tech/pheme/api/internal/httpx"
	"github.com/rh1tech/pheme/api/internal/otp"
	"github.com/rh1tech/pheme/api/internal/store"
)

// Defaults applied when the corresponding AuthHandler field is left zero.
const (
	defaultCodeTTL      = 30 * time.Minute
	defaultCodeCooldown = 2 * time.Minute
)

// AuthHandler serves registration, email verification, password reset and token
// endpoints. Registration is two-step: register stores a pending signup and
// emails a 6-digit code; verify confirms the code and creates the account. No
// user row exists until verification succeeds.
type AuthHandler struct {
	Store       store.Store
	Tokens      *auth.TokenManager
	Codes       otp.Store     // pending signups, reset codes, send cooldowns
	Mailer      mailer.Sender // delivers verification / reset codes
	AdminEmails map[string]bool
	Logger      *slog.Logger

	// Revoker rejects a refresh whose session has been terminated. The /v1/auth/* routes
	// are public — outside the JWT middleware that guards everything else — so a revoked
	// session would otherwise mint fresh tokens here forever. Optional: without one, no
	// session is revoked (the prior behaviour, and what the auth tests exercise).
	Revoker interface {
		IsRevoked(sessionID string) bool
	}

	// CodeTTL is how long a pending signup / reset code stays valid.
	CodeTTL time.Duration
	// CodeCooldown is the minimum interval between code sends to one email.
	CodeCooldown time.Duration
}

// Routes registers the auth endpoints on a mux. These are public (no JWT).
func (h *AuthHandler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/register", h.register)
	mux.HandleFunc("POST /v1/auth/verify", h.verify)
	mux.HandleFunc("POST /v1/auth/login", h.login)
	mux.HandleFunc("POST /v1/auth/refresh", h.refresh)
	mux.HandleFunc("POST /v1/auth/forgot-password", h.forgotPassword)
	mux.HandleFunc("POST /v1/auth/reset-password", h.resetPassword)
}

func (h *AuthHandler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

func (h *AuthHandler) codeTTL() time.Duration {
	if h.CodeTTL > 0 {
		return h.CodeTTL
	}
	return defaultCodeTTL
}

func (h *AuthHandler) cooldown() time.Duration {
	if h.CodeCooldown > 0 {
		return h.CodeCooldown
	}
	return defaultCodeCooldown
}

// roleForEmail returns the role an email should hold per the admin allowlist.
func (h *AuthHandler) roleForEmail(email string) domain.Role {
	if h.AdminEmails[strings.ToLower(email)] {
		return domain.RoleAdmin
	}
	return domain.RoleUser
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type tokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	UserID       string `json:"userId"`
	Role         string `json:"role"`
}

// normalizeEmail trims and lowercases an email and reports whether it is a valid
// single address.
func normalizeEmail(raw string) (string, bool) {
	e := strings.TrimSpace(strings.ToLower(raw))
	if e == "" {
		return "", false
	}
	if _, err := mail.ParseAddress(e); err != nil {
		return e, false
	}
	return e, true
}

// register validates the credentials, stores a pending signup, and emails a
// 6-digit verification code. Re-calling it for the same email acts as a resend,
// gated by the per-email cooldown. The account is not created until /verify.
func (h *AuthHandler) register(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !httpx.Decode(w, r, &req) {
		return
	}
	email, ok := normalizeEmail(req.Email)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "a valid email is required")
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := h.Store.UserByEmail(r.Context(), email); err == nil {
		httpx.Error(w, http.StatusConflict, "email already registered")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusInternalServerError, "registration failed")
		return
	}

	allowed, err := h.Codes.CooldownOK(r.Context(), "signup:"+email, h.cooldown())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "registration failed")
		return
	}
	if !allowed {
		httpx.Error(w, http.StatusTooManyRequests, "please wait a couple of minutes before requesting another code")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "registration failed")
		return
	}
	code, err := otp.GenerateCode()
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "registration failed")
		return
	}
	pending := otp.Signup{Email: email, PasswordHash: hash, CodeHash: otp.HashCode(code)}
	if err := h.Codes.PutSignup(r.Context(), pending, h.codeTTL()); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "registration failed")
		return
	}
	subject, text, html := mailer.VerificationEmail(code)
	if err := h.Mailer.Send(r.Context(), email, subject, text, html); err != nil {
		h.logger().Error("send verification email", "error", err)
		httpx.Error(w, http.StatusBadGateway, "could not send the verification email; please try again")
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{"status": "code_sent"})
}

type verifyRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

// verify confirms a pending signup's code and creates the account. Three wrong
// codes invalidate the pending signup and the user must register again.
func (h *AuthHandler) verify(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	email, _ := normalizeEmail(req.Email)
	code := strings.TrimSpace(req.Code)

	s, err := h.Codes.GetSignup(r.Context(), email)
	if errors.Is(err, otp.ErrNotFound) {
		httpx.Error(w, http.StatusBadRequest, "no pending verification; please register again")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "verification failed")
		return
	}

	if !otp.EqualCode(code, s.CodeHash) {
		n, ierr := h.Codes.IncrSignupAttempts(r.Context(), email)
		if ierr != nil {
			httpx.Error(w, http.StatusBadRequest, "no pending verification; please register again")
			return
		}
		if n >= otp.MaxAttempts {
			_ = h.Codes.DelSignup(r.Context(), email)
			httpx.Error(w, http.StatusBadRequest, "too many incorrect attempts; please request a new code")
			return
		}
		httpx.Error(w, http.StatusUnauthorized, "invalid code")
		return
	}

	// Code correct — guard against a race where the email was registered in the
	// meantime, then create the account.
	if _, err := h.Store.UserByEmail(r.Context(), email); err == nil {
		_ = h.Codes.DelSignup(r.Context(), email)
		httpx.Error(w, http.StatusConflict, "email already registered")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusInternalServerError, "verification failed")
		return
	}

	u, err := h.Store.CreateUser(r.Context(), domain.User{
		Email:        email,
		PasswordHash: s.PasswordHash,
		// Signup asks for an email and a password, so without this every account is created with
		// no name of any kind and the person on the other side of a chat sees "User 3a7119" — six
		// characters of a database id — until the user finds the profile screen. Theirs to change.
		DisplayName: domain.DefaultDisplayName(email),
		Role:        h.roleForEmail(email),
		Status:      domain.UserActive,
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "verification failed")
		return
	}
	_ = h.Codes.DelSignup(r.Context(), email)
	h.issue(w, u.ID, string(u.Role), http.StatusCreated)
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
	if u.Status == domain.UserBlocked {
		httpx.Error(w, http.StatusForbidden, "account is blocked")
		return
	}
	// Determine the effective role. Stored role is authoritative (so admins can
	// manually promote/demote users), but the PHEME_ADMIN_EMAILS allowlist always
	// guarantees its emails are admins. We never auto-demote here.
	role := u.Role
	if role == "" {
		role = domain.RoleUser
	}
	if h.roleForEmail(u.Email) == domain.RoleAdmin && role != domain.RoleAdmin {
		role = domain.RoleAdmin
		if err := h.Store.UpdateUserRole(r.Context(), u.ID, role); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "login failed")
			return
		}
	}
	h.issue(w, u.ID, string(role), http.StatusOK)
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

func (h *AuthHandler) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	claims, err := h.Tokens.ParseClaims(strings.TrimSpace(req.RefreshToken), auth.RefreshToken)
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	// A terminated device must not be able to refresh its way back in.
	if h.Revoker != nil && h.Revoker.IsRevoked(claims.SID) {
		httpx.Error(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	h.issueForSession(w, claims.Subject, claims.Role, claims.SID, http.StatusOK)
}

type forgotRequest struct {
	Email string `json:"email"`
}

// forgotPassword emails a reset code if the account exists. It always responds
// 200 (to avoid revealing which emails are registered) and is gated by the
// per-email cooldown.
func (h *AuthHandler) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	email, ok := normalizeEmail(req.Email)
	respondOK := func() { httpx.JSON(w, http.StatusOK, map[string]any{"status": "code_sent"}) }
	if !ok {
		respondOK()
		return
	}

	allowed, err := h.Codes.CooldownOK(r.Context(), "reset:"+email, h.cooldown())
	if err != nil || !allowed {
		respondOK()
		return
	}
	u, err := h.Store.UserByEmail(r.Context(), email)
	if err != nil {
		respondOK()
		return
	}
	code, err := otp.GenerateCode()
	if err != nil {
		respondOK()
		return
	}
	if err := h.Codes.PutReset(r.Context(), otp.Reset{Email: email, UserID: u.ID, CodeHash: otp.HashCode(code)}, h.codeTTL()); err != nil {
		respondOK()
		return
	}
	subject, text, html := mailer.PasswordResetEmail(code)
	if err := h.Mailer.Send(r.Context(), email, subject, text, html); err != nil {
		h.logger().Error("send reset email", "error", err)
	}
	respondOK()
}

type resetRequest struct {
	Email       string `json:"email"`
	Code        string `json:"code"`
	NewPassword string `json:"newPassword"`
}

// resetPassword verifies a reset code and sets a new password. Three wrong codes
// invalidate the pending reset. On success the user is logged in.
func (h *AuthHandler) resetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	email, _ := normalizeEmail(req.Email)
	code := strings.TrimSpace(req.Code)
	if err := auth.ValidatePassword(req.NewPassword); err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	rst, err := h.Codes.GetReset(r.Context(), email)
	if errors.Is(err, otp.ErrNotFound) {
		httpx.Error(w, http.StatusBadRequest, "no pending reset; please request a new code")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "password reset failed")
		return
	}

	if !otp.EqualCode(code, rst.CodeHash) {
		n, ierr := h.Codes.IncrResetAttempts(r.Context(), email)
		if ierr != nil {
			httpx.Error(w, http.StatusBadRequest, "no pending reset; please request a new code")
			return
		}
		if n >= otp.MaxAttempts {
			_ = h.Codes.DelReset(r.Context(), email)
			httpx.Error(w, http.StatusBadRequest, "too many incorrect attempts; please request a new code")
			return
		}
		httpx.Error(w, http.StatusUnauthorized, "invalid code")
		return
	}

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "password reset failed")
		return
	}
	if err := h.Store.UpdateUserPassword(r.Context(), rst.UserID, hash); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.Error(w, http.StatusBadRequest, "no pending reset; please request a new code")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "password reset failed")
		return
	}
	_ = h.Codes.DelReset(r.Context(), email)

	role := domain.RoleUser
	if u, err := h.Store.UserByEmail(r.Context(), email); err == nil && u.Role != "" {
		role = u.Role
	}
	if h.roleForEmail(email) == domain.RoleAdmin {
		role = domain.RoleAdmin
	}
	h.issue(w, rst.UserID, string(role), http.StatusOK)
}

// issue starts a NEW session (login, register, password reset) and writes its tokens.
func (h *AuthHandler) issue(w http.ResponseWriter, userID, role string, status int) {
	access, refresh, _, err := h.Tokens.Issue(userID, role)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not issue tokens")
		return
	}
	httpx.JSON(w, status, tokenResponse{AccessToken: access, RefreshToken: refresh, UserID: userID, Role: role})
}

// issueForSession re-issues tokens for an EXISTING session (a token refresh), keeping the
// same session id so terminating the device still revokes the right login.
func (h *AuthHandler) issueForSession(w http.ResponseWriter, userID, role, sessionID string, status int) {
	access, refresh, err := h.Tokens.IssueWithSession(userID, role, sessionID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not issue tokens")
		return
	}
	httpx.JSON(w, status, tokenResponse{AccessToken: access, RefreshToken: refresh, UserID: userID, Role: role})
}

// dummyHash is a precomputed Argon2id hash used to equalise login timing when an
// account does not exist.
const dummyHash = "$argon2id$v=19$m=65536,t=1,p=4$hz8RphtgTpv4ss8V0ETClA$Uzi1z1jG80dujeKb15tZAyoGP30p/FVWDNdKRhx/Ajo"
