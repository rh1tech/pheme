package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/store"
)

// What the server says when its database is unwell.
//
// This is not a hypothetical. A load test pointed a thousand streams at the server, the store began
// timing out, and users were told "conversation not found" — because requireMember turned every
// error, including a timeout, into a 404. A client cannot distinguish that from a conversation
// someone deleted: the sensible thing for it to do is forget the conversation, which is precisely
// the wrong thing.
//
// A 404 must mean absence. Anything else must be a 5xx, so clients retry and operators see it.

// flakyStore fails ConversationMembership with a chosen error, and otherwise behaves normally.
type flakyStore struct {
	store.Store
	err error
}

func (f *flakyStore) ConversationMembership(ctx context.Context, convID, userID string) (domain.ConversationMember, error) {
	if f.err != nil {
		return domain.ConversationMember{}, f.err
	}
	return f.Store.ConversationMembership(ctx, convID, userID)
}

func TestAStoreFailureIsNotReportedAsAMissingConversation(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "member@pheme.test")
	f.handler.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	// Every way the store can be unwell rather than empty.
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"a timeout", context.DeadlineExceeded},
		{"a dead connection", errors.New("server selection error: server selection timeout")},
		{"an exhausted pool", errors.New("connection pool exhausted")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.handler.Store = &flakyStore{Store: f.store, err: tc.err}
			defer func() { f.handler.Store = f.store }()

			rec := f.do(http.MethodPost, "/v1/conversations/whatever/messages", token,
				map[string]any{"ciphertext": []byte("hello")})

			if rec.Code == http.StatusNotFound {
				t.Fatalf("%v was reported as 404 conversation not found.\n"+
					"The client cannot tell that from a deletion, so it drops a conversation that "+
					"is still there.", tc.err)
			}
			if rec.Code < 500 {
				t.Errorf("%v answered %d; a server-side failure must be a 5xx so clients retry",
					tc.err, rec.Code)
			}
		})
	}
}

// The other half: a genuine absence must STILL be a 404, and must not leak whether the conversation
// exists at all. Fixing the error handling must not cost the privacy posture it was protecting.
func TestAConversationThatIsNotThereIsStill404(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "outsider@pheme.test")

	rec := f.do(http.MethodPost, "/v1/conversations/000000000000000000000000/messages", token,
		map[string]any{"ciphertext": []byte("hello")})
	if rec.Code != http.StatusNotFound {
		t.Errorf("a missing conversation = %d, want 404", rec.Code)
	}

	// And a real conversation the caller is not in must answer identically — a different status
	// would confirm the conversation exists to someone with no business knowing.
	owner, ownerToken := f.user(t, "owner@pheme.test")
	_ = owner
	created := f.do(http.MethodPost, "/v1/conversations", ownerToken, map[string]any{
		"kind": "group", "name": "theirs",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create conversation = %d: %s", created.Code, created.Body)
	}
	var conv struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &conv); err != nil {
		t.Fatalf("decode: %v", err)
	}

	outsider := f.do(http.MethodPost, "/v1/conversations/"+conv.ID+"/messages", token,
		map[string]any{"ciphertext": []byte("hello")})
	if outsider.Code != http.StatusNotFound {
		t.Errorf("a conversation the caller is not in = %d, want 404 — anything else confirms it "+
			"exists", outsider.Code)
	}
}
