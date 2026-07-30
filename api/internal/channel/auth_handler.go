package channel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"github.com/rh1tech/pheme/api/internal/invite"
	"github.com/rh1tech/pheme/api/internal/otp"
	"github.com/rh1tech/pheme/api/internal/ratelimit"
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

	// InviteOnly refuses registration that does not carry a valid, unspent invite code.
	// See config.Config.InviteOnly — it defaults to on there, and the zero value here is
	// off so a handler built by hand in a test stays open unless it says otherwise.
	InviteOnly bool

	// Revoker protects the public refresh and password-reset routes, which sit
	// outside the JWT middleware. It is required in every serving configuration.
	Revoker interface {
		userSessionRevoker
		IsRevoked(sessionID string) bool
		// IsUserRevoked covers the device IsRevoked cannot reach: one whose session id
		// was never recorded has none to match, so only the per-user cutoff can end it.
		// Refresh MUST consult this for the same reason the middleware does — a token
		// re-issued from an unrevoked refresh token carries a fresh IssuedAt, which is
		// after the cutoff, so skipping the check here undoes the revocation entirely
		// rather than merely narrowing it.
		IsUserRevoked(userID string, issuedAt time.Time) bool
	}

	// Limiter throttles the credential endpoints. These are the only unauthenticated,
	// state-changing routes on the App API, and password verification is deliberately
	// expensive (Argon2id) — so without a limit an attacker gets unlimited guesses AND
	// a cheap way to saturate the hash-slot pool that keeps honest logins served.
	//
	// Optional: nil disables throttling, which is the right default for tests but not
	// for a deployment.
	Limiter ratelimit.Limiter
	// TrustProxyHeaders decides whether X-Forwarded-For is believed when identifying a
	// caller. See httpx.ClientIP — wrong in either direction weakens the per-IP limit.
	TrustProxyHeaders bool

	// CodeTTL is how long a pending signup / reset code stays valid.
	CodeTTL time.Duration
	// CodeCooldown is the minimum interval between code sends to one email.
	CodeCooldown time.Duration
}

// Routes registers the auth endpoints on a mux. These are public (no JWT).
func (h *AuthHandler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/auth/registration", h.registration)
	mux.HandleFunc("GET /v1/auth/invite", h.checkInvite)
	mux.HandleFunc("POST /v1/auth/register", h.register)
	mux.HandleFunc("POST /v1/auth/verify", h.verify)
	mux.HandleFunc("POST /v1/auth/login", h.login)
	mux.HandleFunc("POST /v1/auth/refresh", h.refresh)
	mux.HandleFunc("POST /v1/auth/forgot-password", h.forgotPassword)
	mux.HandleFunc("POST /v1/auth/reset-password", h.resetPassword)
}

// allow applies the credential-endpoint rate limit.
//
// TWO keys, and both matter. The per-account key is the one that actually bounds
// password guessing: an attacker with a botnet defeats any per-IP limit, but every
// guess against one account still has to pass through that account's bucket. The
// per-IP key bounds the other direction — one source spraying many accounts, which
// a per-account limit never sees.
//
// Ordering note: the account bucket is consumed first so that a distributed attack
// on one account is throttled even when no single IP stands out.
func (h *AuthHandler) allow(w http.ResponseWriter, r *http.Request, action, account string) bool {
	if h.Limiter == nil {
		return true
	}
	if account != "" && !h.Limiter.Allow("auth:"+action+":acct:"+strings.ToLower(account)) {
		tooMany(w)
		return false
	}
	// The address bucket is applied only when the address distinguishes callers.
	// Behind a proxy with TrustProxyHeaders off, every request carries the proxy's
	// address, and one bucket for the whole instance is not a rate limit — it is an
	// outage that eight wrong passwords can trigger for everybody. The account
	// bucket above still applies, and it is the one that bounds guessing anyway.
	ip := httpx.ClientIP(r, h.TrustProxyHeaders)
	if !httpx.ClientIPIsDistinct(ip, h.TrustProxyHeaders) {
		return true
	}
	if !h.Limiter.Allow("auth:" + action + ":ip:" + ip) {
		tooMany(w)
		return false
	}
	return true
}

// tooMany answers a throttled request. The message says nothing about which
// bucket ran out — an attacker learning "this account is being limited" learns
// that the account exists.
func tooMany(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	httpx.Error(w, http.StatusTooManyRequests, "too many attempts, try again later")
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

func (h *AuthHandler) currentRole(ctx context.Context, u domain.User) (domain.Role, error) {
	role := u.Role
	if role == "" {
		role = domain.RoleUser
	}
	if h.roleForEmail(u.Email) == domain.RoleAdmin && role != domain.RoleAdmin {
		role = domain.RoleAdmin
		if err := h.Store.UpdateUserRole(ctx, u.ID, role); err != nil {
			return "", err
		}
	}
	return role, nil
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// Invite carries the code from an invitation link. Ignored unless the server is
	// invite-only, and ignored entirely on login.
	Invite string `json:"invite,omitempty"`
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

// registration tells a client what the signup form has to ask for, before it draws one.
// Public and cacheless: it is the same answer for everybody and reveals nothing an attempt
// to register would not.
func (h *AuthHandler) registration(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{"inviteOnly": h.InviteOnly})
}

// inviteStatus is the verdict on one code: what a client needs to decide between "show the
// form" and "this link is no good".
type inviteStatus struct {
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"` // unknown | used | revoked | expired
}

// resolveInvite finds the invite behind a code and says whether it may still be redeemed.
// A code that matches nothing and a code that has been spent are told apart on purpose: the
// invitee needs to know whether to check the link or ask for a new one, and neither answer
// helps an attacker who does not already hold a code.
func (h *AuthHandler) resolveInvite(r *http.Request, code string) (domain.Invite, inviteStatus, error) {
	code = invite.Normalize(code)
	if code == "" {
		return domain.Invite{}, inviteStatus{Reason: "unknown"}, nil
	}
	inv, err := h.Store.InviteByCodeHash(r.Context(), invite.HashCode(code))
	if errors.Is(err, store.ErrNotFound) {
		return domain.Invite{}, inviteStatus{Reason: "unknown"}, nil
	} else if err != nil {
		return domain.Invite{}, inviteStatus{}, err
	}
	if status := inv.Status(time.Now().UTC()); status != domain.InvitePending {
		return inv, inviteStatus{Reason: string(status)}, nil
	}
	return inv, inviteStatus{Valid: true}, nil
}

// checkInvite reports whether an invitation link is still good, so the signup screen can say
// so before the visitor fills a form it is going to reject.
//
// Rate limited like the credential endpoints, and for the same reason: without it this is an
// oracle an attacker can grind against to find a live code.
func (h *AuthHandler) checkInvite(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "invite", "") {
		return
	}
	if !h.InviteOnly {
		// Nothing to check — anybody may register — and answering "invalid" here would make
		// an open server look broken to a client that asked.
		httpx.JSON(w, http.StatusOK, inviteStatus{Valid: true})
		return
	}
	_, status, err := h.resolveInvite(r, r.URL.Query().Get("code"))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not check the invite")
		return
	}
	httpx.JSON(w, http.StatusOK, status)
}

// inviteMessage is what the person holding a bad link is told.
func inviteMessage(reason string) string {
	switch reason {
	case "used":
		return "this invitation has already been used"
	case "revoked":
		return "this invitation has been withdrawn"
	case "expired":
		return "this invitation has expired"
	default:
		return "a valid invitation is required to register on this server"
	}
}

// register validates the credentials, stores a pending signup, and emails a
// 6-digit verification code. Re-calling it for the same email acts as a resend,
// gated by the per-email cooldown. The account is not created until /verify.
//
// On an invite-only server the invitation is CHECKED here but not spent: it is recorded on
// the pending signup and consumed at /verify, once the email has actually been proven. The
// other order lets a stranger burn somebody else's invitation by typing their address in.
func (h *AuthHandler) register(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !httpx.Decode(w, r, &req) {
		return
	}
	email, ok := normalizeEmail(req.Email)
	if !h.allow(w, r, "register", email) {
		return
	}
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

	var inviteID string
	if h.InviteOnly {
		inv, status, err := h.resolveInvite(r, req.Invite)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "registration failed")
			return
		}
		if !status.Valid {
			httpx.Error(w, http.StatusForbidden, inviteMessage(status.Reason))
			return
		}
		inviteID = inv.ID
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
	pending := otp.Signup{Email: email, PasswordHash: hash, CodeHash: otp.HashCode(code), InviteID: inviteID}
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
	if !h.allow(w, r, "verify", email) {
		return
	}
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

	// Spend the invitation BEFORE creating the account, and only then. ConsumeInvite is a
	// compare-and-set, so of two people finishing verification against the same link in the
	// same instant exactly one gets past this line — which is the entire meaning of "once".
	// Checking redeemability and then creating the user would let both through.
	//
	// The account's id is minted here rather than by the store so the invite can record who
	// spent it in that same atomic step; without it the row would have to be written twice
	// and a crash between the two would leave an invitation nobody can account for.
	userID, err := newUserID()
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "verification failed")
		return
	}
	inviteID := ""
	if h.InviteOnly && s.InviteID != "" {
		switch err := h.Store.ConsumeInvite(r.Context(), s.InviteID, userID, time.Now().UTC()); {
		case err == nil:
			inviteID = s.InviteID
		case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrInviteSpent):
			_ = h.Codes.DelSignup(r.Context(), email)
			httpx.Error(w, http.StatusForbidden, inviteMessage("used"))
			return
		default:
			httpx.Error(w, http.StatusInternalServerError, "verification failed")
			return
		}
	}

	u, err := h.Store.CreateUser(r.Context(), domain.User{
		ID:           userID,
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
		// The invitation was spent a moment ago for an account that does not exist. Hand it
		// back, or a transient database fault costs the invitee their one chance to sign up.
		if inviteID != "" {
			if rerr := h.Store.ReleaseInvite(r.Context(), inviteID); rerr != nil {
				h.logger().Error("release invite after failed signup", "inviteId", inviteID, "error", rerr)
			}
		}
		httpx.Error(w, http.StatusInternalServerError, "verification failed")
		return
	}
	_ = h.Codes.DelSignup(r.Context(), email)
	h.issue(w, u.ID, string(u.Role), http.StatusCreated)
}

// newUserID mints an account id in the same shape the stores use, so an id chosen here is
// indistinguishable from one they would have generated.
func newUserID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (h *AuthHandler) login(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !httpx.Decode(w, r, &req) {
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if !h.allow(w, r, "login", req.Email) {
		return
	}

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
	role, err := h.currentRole(r.Context(), u)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "login failed")
		return
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
	// A terminated device must not be able to refresh its way back in — by EITHER
	// route. Checking only the session id let a device with no session id (the
	// exact case the per-user cutoff exists for) trade its still-valid refresh
	// token for a freshly-stamped access token that outran the cutoff.
	if h.Revoker != nil {
		if h.Revoker.IsRevoked(claims.SID) {
			httpx.Error(w, http.StatusUnauthorized, "invalid refresh token")
			return
		}
		if claims.IssuedAt != nil && h.Revoker.IsUserRevoked(claims.Subject, claims.IssuedAt.Time) {
			httpx.Error(w, http.StatusUnauthorized, "invalid refresh token")
			return
		}
	}
	u, err := h.Store.UserByID(r.Context(), claims.Subject)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusUnauthorized, "invalid refresh token")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "refresh failed")
		return
	}
	if u.Status == domain.UserBlocked {
		httpx.Error(w, http.StatusForbidden, "account is blocked")
		return
	}
	role, err := h.currentRole(r.Context(), u)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "refresh failed")
		return
	}
	h.issueForSession(w, u.ID, string(role), claims.SID, http.StatusOK)
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
	if !h.allow(w, r, "forgot-password", email) {
		return
	}
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
		h.logger().Warn("forgot-password: store reset code failed", "error", err)
		respondOK()
		return
	}
	subject, text, html := mailer.PasswordResetEmail(code)
	if err := h.Mailer.Send(r.Context(), email, subject, text, html); err != nil {
		h.logger().Warn("forgot-password: send reset email failed", "error", err)
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
	if !h.allow(w, r, "reset-password", email) {
		return
	}
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

	u, err := h.Store.UserByID(r.Context(), rst.UserID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusBadRequest, "no pending reset; please request a new code")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "password reset failed")
		return
	}
	if u.Status == domain.UserBlocked {
		httpx.Error(w, http.StatusForbidden, "account is blocked")
		return
	}

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "password reset failed")
		return
	}
	if err := h.Codes.DelReset(r.Context(), email); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "password reset failed")
		return
	}
	// Bracket the password write with cutoffs. The first closes the race with a
	// concurrent refresh; the second catches any token minted while the write ran.
	if _, err := revokeUserSessions(
		r.Context(), h.Revoker, u.ID, h.Tokens.RefreshTTL(),
	); err != nil {
		h.logger().Error("password reset: revoke existing sessions", "user", u.ID, "error", err)
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
	// Re-read after the password write. This avoids returning a stale role or a
	// session for an account an administrator blocked while hashing ran.
	u, err = h.Store.UserByID(r.Context(), rst.UserID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusUnauthorized, "account is no longer available")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "password reset failed")
		return
	}
	if u.Status == domain.UserBlocked {
		httpx.Error(w, http.StatusForbidden, "account is blocked")
		return
	}
	role, err := h.currentRole(r.Context(), u)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "password reset failed")
		return
	}
	cutoff, err := revokeUserSessions(
		r.Context(), h.Revoker, u.ID, h.Tokens.RefreshTTL(),
	)
	if err != nil {
		h.logger().Error("password reset: finalize session revocation", "user", u.ID, "error", err)
		httpx.Error(w, http.StatusInternalServerError, "password reset failed")
		return
	}
	h.issueAt(w, rst.UserID, string(role), cutoff, http.StatusOK)
}

// issue starts a new login or registration session and writes its tokens.
func (h *AuthHandler) issue(w http.ResponseWriter, userID, role string, status int) {
	access, refresh, _, err := h.Tokens.Issue(userID, role)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not issue tokens")
		return
	}
	httpx.JSON(w, status, tokenResponse{AccessToken: access, RefreshToken: refresh, UserID: userID, Role: role})
}

func (h *AuthHandler) issueAt(
	w http.ResponseWriter,
	userID, role string,
	issuedAt time.Time,
	status int,
) {
	access, refresh, _, err := h.Tokens.IssueAt(userID, role, issuedAt)
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
