package chat

import (
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/ident"
)

func TestDirectKeysWithoutAHostDomainStayLegacy(t *testing.T) {
	h := &Handler{}
	a := domain.User{ID: "aaa"}
	b := domain.User{ID: "bbb"}

	primary, legacy := h.directKeys(a, b)
	if primary != domain.DirectKey("aaa", "bbb") {
		t.Errorf("primary = %q, want the legacy key when no domain is configured", primary)
	}
	// Nothing to fall back TO — the primary already is the legacy form, and a
	// second identical lookup would be pure waste.
	if legacy != "" {
		t.Errorf("legacy = %q, want empty", legacy)
	}
}

func TestDirectKeysWithAHostDomainAreQualified(t *testing.T) {
	h := &Handler{HostDomain: "a.example"}
	a := domain.User{ID: "aaa"}
	b := domain.User{ID: "bbb"}

	primary, legacy := h.directKeys(a, b)
	want := ident.PairKey(ident.User("a.example", "aaa"), ident.User("a.example", "bbb"))
	if primary != want {
		t.Errorf("primary = %q, want the qualified pair key", primary)
	}
	// The legacy key must still be offered, or a conversation created before
	// qualification becomes invisible and a duplicate gets created alongside it.
	if legacy != domain.DirectKey("aaa", "bbb") {
		t.Errorf("legacy = %q, want the old key so old rows stay findable", legacy)
	}
}

func TestDirectKeysDoNotDependOnArgumentOrder(t *testing.T) {
	h := &Handler{HostDomain: "a.example"}
	a := domain.User{ID: "aaa"}
	b := domain.User{ID: "bbb"}

	forward, _ := h.directKeys(a, b)
	backward, _ := h.directKeys(b, a)
	if forward != backward {
		t.Error("the key depends on which user is passed first, so {a,b} and {b,a} would be two chats")
	}
}

// The whole reason identifiers are qualified. Two accounts with the same local
// id on different hosts are different people, and a conversation with each is
// two conversations — under the legacy key they collapsed into one.
func TestSameLocalIdOnDifferentHostsIsADifferentConversation(t *testing.T) {
	h := &Handler{HostDomain: "a.example"}
	me := domain.User{ID: "aaa"}

	local := domain.User{ID: "bbb"}
	remote := domain.User{ID: "bbb", Domain: "b.example"}

	withLocal, _ := h.directKeys(me, local)
	withRemote, _ := h.directKeys(me, remote)
	if withLocal == withRemote {
		t.Error("a local and a remote user with the same id produced one key")
	}

	// And the legacy key CANNOT tell them apart, which is exactly why it is
	// being replaced rather than extended.
	if domain.DirectKey(me.ID, local.ID) != domain.DirectKey(me.ID, remote.ID) {
		t.Error("the legacy key unexpectedly distinguished them; this test is no longer meaningful")
	}
}

// An account with no Domain is local, so it must key identically to one that
// names this host explicitly. Otherwise backfilling the field — or a peer
// echoing our own domain back at us — would fork every conversation.
func TestAbsentDomainKeysTheSameAsTheHostDomain(t *testing.T) {
	h := &Handler{HostDomain: "a.example"}
	me := domain.User{ID: "aaa"}

	implicit, _ := h.directKeys(me, domain.User{ID: "bbb"})
	explicit, _ := h.directKeys(me, domain.User{ID: "bbb", Domain: "a.example"})
	if implicit != explicit {
		t.Error("an implicitly-local user keyed differently from an explicitly-local one")
	}
}

func TestUserIdentQualifiesWithTheLocalDomainOnlyWhenUnset(t *testing.T) {
	local := domain.User{ID: "aaa"}
	if got := local.Ident("a.example").String(); got != "mimi://a.example/u/aaa" {
		t.Errorf("local user = %q", got)
	}
	remote := domain.User{ID: "aaa", Domain: "b.example"}
	if got := remote.Ident("a.example").String(); got != "mimi://b.example/u/aaa" {
		t.Errorf("remote user = %q — the local domain overrode a real one", got)
	}
}
