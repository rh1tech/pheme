// Package chat serves the private conversation API — direct (1-to-1) and group
// chats. It is the member-to-member counterpart of package channel's broadcast
// surface.
//
// The server is an MLS Delivery Service here: it stores conversation membership
// and relays message content it never reads. A ChatMessage's Ciphertext is
// opaque bytes (JSON-encoded as base64) — plaintext in the interim, MLS
// ciphertext once E2EE is switched on. No handler in this package interprets it.
package chat

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/push"
	"github.com/rh1tech/pheme/api/internal/store"
)

// Handler serves the authenticated conversation API.
type Handler struct {
	Store store.Store
	Live  live.Bus
	// Push may be nil (tests, or a deployment with no push configured). Chat
	// notifications carry only the sender's name — never message content, which the
	// server cannot read.
	Push   push.Sender
	Logger *slog.Logger
}

func (h *Handler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// Register attaches the conversation routes to an already-authenticated mux (the
// same protected mux the channel API uses, so JWT middleware already applies).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/conversations", h.createConversation)
	mux.HandleFunc("GET /v1/conversations", h.listConversations)
	mux.HandleFunc("GET /v1/conversations/{id}", h.getConversation)
	mux.HandleFunc("DELETE /v1/conversations/{id}", h.deleteConversation)
	mux.HandleFunc("GET /v1/conversations/{id}/messages", h.listMessages)
	mux.HandleFunc("POST /v1/conversations/{id}/messages", h.postMessage)
	mux.HandleFunc("GET /v1/conversations/{id}/members", h.listMembers)
	mux.HandleFunc("POST /v1/conversations/{id}/members", h.addMember)
	mux.HandleFunc("PATCH /v1/conversations/{id}/members/{userId}", h.setMemberRole)
	mux.HandleFunc("DELETE /v1/conversations/{id}/members/{userId}", h.removeMember)
	// User search for the "start a chat with…" picker. Returns public profiles
	// only — never email — and requires a minimum query length to limit
	// enumeration. Any authenticated user may search; membership is not involved.
	mux.HandleFunc("GET /v1/users/search", h.searchUsers)
	// MLS key directory (public KeyPackages) — the E2EE handshake material.
	mux.HandleFunc("POST /v1/mls/key-packages", h.publishKeyPackages)
	mux.HandleFunc("GET /v1/mls/key-packages/count", h.keyPackageCount)
	mux.HandleFunc("DELETE /v1/mls/key-packages", h.deleteKeyPackages)
	// Both device-scoped: an MLS leaf is a device, so a group is built from a KeyPackage
	// per DEVICE of each member, never one per user. `devices` answers which devices
	// exist without consuming anything; `claim` hands out a package for named ones.
	//
	// Both hang off a CONVERSATION, so membership in it is the authorization: you can
	// only see, or claim keys for, the devices of people you are actually talking to. A
	// global key directory would let any signed-in stranger enumerate a victim's devices
	// and drain their single-use KeyPackages on a loop.
	mux.HandleFunc("GET /v1/conversations/{id}/mls/devices", h.listDevices)
	mux.HandleFunc("POST /v1/conversations/{id}/mls/key-packages/claim", h.claimKeyPackages)
	// The conversation's MLS group, and the compare-and-set that serialises Commits.
	mux.HandleFunc("GET /v1/conversations/{id}/mls", h.getMLSGroup)
	mux.HandleFunc("GET /v1/conversations/{id}/mls/commits", h.listMLSCommits)
	mux.HandleFunc("POST /v1/conversations/{id}/mls/commit", h.postMLSCommit)
	mux.HandleFunc("PUT /v1/mls/key-backup", h.putKeyBackup)
	mux.HandleFunc("GET /v1/mls/key-backup", h.getKeyBackup)
}

const minUserSearchLen = 2

func (h *Handler) searchUsers(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	q := r.URL.Query().Get("q")
	if len([]rune(q)) < minUserSearchLen {
		httpx.JSON(w, http.StatusOK, map[string]any{"users": []domain.PublicUser{}})
		return
	}
	users, err := h.Store.SearchUsers(r.Context(), q, 20)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "search failed")
		return
	}
	out := make([]domain.PublicUser, 0, len(users))
	for _, u := range users {
		if u.ID == uid {
			continue // no point starting a chat with yourself
		}
		out = append(out, u.Public())
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"users": out})
}

func (h *Handler) requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return "", false
	}
	return uid, true
}

// requireMember resolves the caller and asserts they belong to the conversation,
// returning 404 (not 403) for a non-member so a conversation's existence is never
// leaked to outsiders — the same posture package channel takes.
func (h *Handler) requireMember(w http.ResponseWriter, r *http.Request) (uid, convID string, member domain.ConversationMember, ok bool) {
	uid, ok = h.requireUser(w, r)
	if !ok {
		return
	}
	convID = r.PathValue("id")
	member, err := h.Store.ConversationMembership(r.Context(), convID, uid)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "conversation not found")
		return uid, convID, member, false
	}
	return uid, convID, member, true
}

const maxGroupMembers = 200

// A ceiling for request bodies that carry only ids and short strings (creating a
// conversation, adding a member). Generous next to what they need, tiny next to
// what an attacker would like to make the server buffer.
const maxSmallBodyBytes = 64 * 1024

type createConversationRequest struct {
	Kind domain.ConversationKind `json:"kind"`
	// The other participant (direct) or the initial members besides the creator
	// (group). The creator is always added and need not be listed.
	MemberIDs []string `json:"memberIds"`
	Title     string   `json:"title,omitempty"`
}

func (h *Handler) createConversation(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	var req createConversationRequest
	if !httpx.DecodeLimited(w, r, &req, maxSmallBodyBytes) {
		return
	}

	switch req.Kind {
	case domain.ConversationDirect:
		h.createDirect(w, r, uid, req)
	case domain.ConversationGroup:
		h.createGroup(w, r, uid, req)
	default:
		httpx.Error(w, http.StatusBadRequest, "kind must be 'direct' or 'group'")
	}
}

func (h *Handler) createDirect(w http.ResponseWriter, r *http.Request, uid string, req createConversationRequest) {
	if len(req.MemberIDs) != 1 {
		httpx.Error(w, http.StatusBadRequest, "a direct chat needs exactly one other member")
		return
	}
	other := req.MemberIDs[0]
	if other == uid {
		httpx.Error(w, http.StatusBadRequest, "cannot start a direct chat with yourself")
		return
	}
	if _, err := h.Store.UserByID(r.Context(), other); err != nil {
		httpx.Error(w, http.StatusNotFound, "user not found")
		return
	}

	// Dedupe: at most one direct chat per unordered pair.
	key := domain.DirectKey(uid, other)
	if existing, err := h.Store.ConversationByDirectKey(r.Context(), key); err == nil {
		httpx.JSON(w, http.StatusOK, existing)
		return
	}

	now := time.Now().UTC()
	conv := domain.Conversation{
		Kind:      domain.ConversationDirect,
		CreatedBy: uid,
		DirectKey: key,
		CreatedAt: now,
	}
	members := []domain.ConversationMember{
		{UserID: uid, Role: domain.RoleUser, JoinedAt: now},
		{UserID: other, Role: domain.RoleUser, JoinedAt: now},
	}
	created, err := h.Store.CreateConversation(r.Context(), conv, members)
	if err != nil {
		// Lost a create race on the unique directKey — return the winner.
		if existing, e := h.Store.ConversationByDirectKey(r.Context(), key); e == nil {
			httpx.JSON(w, http.StatusOK, existing)
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "could not create conversation")
		return
	}
	httpx.JSON(w, http.StatusCreated, created)
}

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request, uid string, req createConversationRequest) {
	// Unique member set, excluding the creator (always added as admin).
	seen := map[string]struct{}{uid: {}}
	members := []domain.ConversationMember{{UserID: uid, Role: domain.RoleAdmin, JoinedAt: time.Now().UTC()}}
	for _, id := range req.MemberIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		if _, err := h.Store.UserByID(r.Context(), id); err != nil {
			httpx.Error(w, http.StatusBadRequest, "member not found: "+id)
			return
		}
		seen[id] = struct{}{}
		members = append(members, domain.ConversationMember{UserID: id, Role: domain.RoleUser, JoinedAt: time.Now().UTC()})
	}
	if len(members) > maxGroupMembers {
		httpx.Error(w, http.StatusBadRequest, "too many members")
		return
	}

	conv := domain.Conversation{
		Kind:      domain.ConversationGroup,
		Title:     req.Title,
		CreatedBy: uid,
		CreatedAt: time.Now().UTC(),
	}
	created, err := h.Store.CreateConversation(r.Context(), conv, members)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not create conversation")
		return
	}
	httpx.JSON(w, http.StatusCreated, created)
}

func (h *Handler) getConversation(w http.ResponseWriter, r *http.Request) {
	_, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	conv, err := h.Store.ConversationByID(r.Context(), convID)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "conversation not found")
		return
	}
	// Hydrated the same way the list is, so the header can label a direct chat.
	view := conversationView{Conversation: conv, Members: h.membersView(r.Context(), convID)}
	if last, err := h.Store.LastChatMessagesByConversations(r.Context(), []string{convID}); err == nil {
		if msg, present := last[convID]; present {
			view.LastMessage = newLastChatMessageView(msg)
		}
	}
	httpx.JSON(w, http.StatusOK, view)
}

func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) {
	_, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"members": h.membersView(r.Context(), convID)})
}
