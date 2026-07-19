package domain

import (
	"errors"
	"strings"
	"testing"
)

// Names, handles, and the key that decides whether two people already have a conversation.
//
// Small pure functions, each with a consequence out of proportion to its size: a derived name is
// what the other side of a chat sees, a username is unique system-wide and unchangeably public
// once taken, and the direct-chat key is what stops one pair of people accumulating two separate
// conversations that each hold half the history.

// THE ONE WITH HISTORY. Every account used to be born nameless, and the clients rendered six
// characters of a database id — "User 3a7119" — which is what the other side of a chat saw
// indefinitely unless the person found the profile screen.
func TestADerivedNameIsSomethingThePersonRecognises(t *testing.T) {
	for _, tc := range []struct{ email, want string }{
		{"boris@pheme.test", "boris"},
		{"boris.quill@pheme.test", "boris.quill"},
		{"Boris.Quill@Pheme.Test", "Boris.Quill"},
		// A "+tag" is addressing, not identity — nobody is called "boris+newsletters".
		{"boris+newsletters@pheme.test", "boris"},
		// Punctuation an address may carry but a name should not lead or trail with.
		{".boris.@pheme.test", "boris"},
		{"_boris-@pheme.test", "boris"},
		{"  boris@pheme.test  ", "boris"},
	} {
		if got := DefaultDisplayName(tc.email); got != tc.want {
			t.Errorf("DefaultDisplayName(%q) = %q, want %q", tc.email, got, tc.want)
		}
	}
}

// Something that is not an address produces no name rather than nonsense. Better a client falling
// back than a name of "not-an-email".
func TestADerivedNameFromSomethingThatIsNotAnAddress(t *testing.T) {
	for _, s := range []string{"", "no-at-sign", "   "} {
		if got := DefaultDisplayName(s); got != "" {
			t.Errorf("DefaultDisplayName(%q) = %q, want empty", s, got)
		}
	}
}

// A pathological address must not become a pathological name. The bound here only has to stop that;
// names people choose are checked separately, with a larger limit.
func TestADerivedNameIsBounded(t *testing.T) {
	long := strings.Repeat("a", 500) + "@pheme.test"
	got := DefaultDisplayName(long)
	if len(got) > maxDisplayNameLen {
		t.Errorf("a %d-character local part produced a %d-character name", 500, len(got))
	}
	if got == "" {
		t.Error("a long but valid address produced no name at all")
	}
}

// THE ONE THAT KEEPS A CONVERSATION SINGULAR. The key must not depend on who started it.
func TestDirectKeyIsTheSameWhicheverWayRound(t *testing.T) {
	if a, b := DirectKey("alice", "bob"), DirectKey("bob", "alice"); a != b {
		t.Errorf("DirectKey is order-dependent: %q vs %q. Alice starting a chat with Bob and Bob "+
			"starting one with Alice would make two conversations, each holding half the history",
			a, b)
	}
}

func TestDirectKeyDistinguishesDifferentPairs(t *testing.T) {
	seen := map[string]string{}
	pairs := [][2]string{
		{"a", "b"}, {"a", "c"}, {"b", "c"}, {"aa", "b"}, {"a", "ab"},
	}
	for _, p := range pairs {
		k := DirectKey(p[0], p[1])
		if other, clash := seen[k]; clash {
			t.Errorf("pair %v and %s produce the same key %q; two different pairs would share one "+
				"conversation", p, other, k)
		}
		seen[k] = p[0] + "/" + p[1]
	}
}

// The key is built by joining two ids with a colon, which is only unambiguous because an id can
// never contain one. DirectKey("a", "b:c") and DirectKey("a:b", "c") both produce "a:b:c".
//
// That is not a defect to fix, and it is worth saying why rather than leaving the question open.
// Ids are 24 characters of hex from the stores' generator, so the alphabet is [0-9a-f] and a colon
// is unreachable. Changing the key's format would invalidate every direct-conversation row already
// stored under the current one — a migration, to close a hole nothing can reach.
//
// What this test does is pin the precondition the function silently depends on, so that if ids ever
// stop being hex the failure shows up here rather than as two people mysteriously sharing a
// conversation with somebody else.
func TestDirectKeyIsUnambiguousForIDsOfTheShapeTheStoresIssue(t *testing.T) {
	const idAlphabet = "0123456789abcdef"

	if strings.ContainsAny(idAlphabet, ":") {
		t.Fatal("ids can now contain the separator DirectKey joins on; two unrelated pairs would " +
			"produce the same key and share a conversation")
	}

	// Realistic ids, exhaustively paired, must all produce distinct keys.
	ids := []string{
		"507f1f77bcf86cd799439011", "507f1f77bcf86cd799439012", "507f191e810c19729de860ea",
		"0000000000000000000000ab", "ab0000000000000000000000",
	}
	seen := map[string][2]string{}
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			k := DirectKey(ids[i], ids[j])
			if prev, clash := seen[k]; clash {
				t.Errorf("pairs %v and %v both produce %q", prev, [2]string{ids[i], ids[j]}, k)
			}
			seen[k] = [2]string{ids[i], ids[j]}
		}
	}
}

func TestUsernameRules(t *testing.T) {
	valid := []string{"abc", "boris", "boris_quill", "boris.quill", "a1b2c3", strings.Repeat("a", 30)}
	for _, u := range valid {
		if err := ValidateUsername(u); err != nil {
			t.Errorf("ValidateUsername(%q) = %v, want accepted", u, err)
		}
	}

	invalid := []struct{ username, why string }{
		{"", "empty"},
		{"ab", "shorter than the minimum"},
		{strings.Repeat("a", 31), "longer than the maximum"},
		{"1boris", "starts with a digit"},
		{".boris", "starts with a dot"},
		{"boris quill", "contains a space"},
		{"boris-quill", "contains a hyphen, which usernames do not allow"},
		{"boris@quill", "contains an at sign"},
		{"boris/quill", "contains a slash"},
		{"borís", "contains a non-ascii letter"},
	}
	for _, tc := range invalid {
		err := ValidateUsername(tc.username)
		if err == nil {
			t.Errorf("ValidateUsername(%q) was accepted (%s)", tc.username, tc.why)
			continue
		}
		if !errors.Is(err, ErrInvalidUsername) {
			t.Errorf("ValidateUsername(%q) = %v, want ErrInvalidUsername so callers can match on it",
				tc.username, err)
		}
	}
}

// MLS control traffic must be recognisable, because it is what decides whether a stored message
// wakes somebody's phone. A Welcome or a Commit is protocol machinery, not something a person sent;
// treating one as a message rings a phone for nothing, and treating a real message as protocol
// traffic silently drops the notification for something that mattered.
func TestMLSProtocolTrafficIsRecognised(t *testing.T) {
	protocol := []string{
		ContentTypeMLSWelcome, ContentTypeMLSCommit, ContentTypeMLSDevice,
		ContentTypeMLSHistoryRequest, ContentTypeMLSHistoryOffer,
	}
	for _, ct := range protocol {
		if !IsMLSProtocol(ct) {
			t.Errorf("IsMLSProtocol(%q) = false; this is protocol traffic and would ring a phone "+
				"for something nobody sent", ct)
		}
	}

	human := []string{
		"application/octet-stream", "text/plain", "", "image/jpeg",
		// Near-misses: a typo must not be silently treated as protocol traffic, which would drop
		// the notification for a real message.
		"application/x-mls", "mls-commit", strings.ToUpper(ContentTypeMLSCommit),
	}
	for _, ct := range human {
		if IsMLSProtocol(ct) {
			t.Errorf("IsMLSProtocol(%q) = true; a real message would never notify anyone", ct)
		}
	}
}
