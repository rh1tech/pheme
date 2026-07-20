package chat

import (
	"encoding/binary"
	"net/http"
	"testing"
)

// publicCommit builds the smallest byte string mlswire will accept as a
// PublicMessage Commit built on the given epoch: the two message headers, a
// group id, the epoch, a member sender, empty authenticated data, and the commit
// content type. It carries no real proposals — the parser reads only as far as
// the content type, which is exactly the point of F4's minimal decoder.
func publicCommit(epoch uint64) []byte {
	var b []byte
	b = append(b, 0x00, 0x01) // protocol version: MLS 1.0
	b = append(b, 0x00, 0x01) // wire format: public_message
	// group_id<V>: a 4-byte id (varint length 0x04)
	b = append(b, 0x04, 'g', 'r', 'p', '0')
	// epoch: uint64
	var e [8]byte
	binary.BigEndian.PutUint64(e[:], epoch)
	b = append(b, e[:]...)
	// sender: member(1) + leaf_index uint32
	b = append(b, 0x01, 0x00, 0x00, 0x00, 0x00)
	// authenticated_data<V>: empty
	b = append(b, 0x00)
	// content_type: commit(3)
	b = append(b, 0x03)
	return b
}

// A real, parseable commit can no longer lie about the epoch it was built on:
// the server reads the epoch from the commit and refuses one whose declared
// baseEpoch disagrees. This is the hole the old PrivateMessage framing left open.
func TestCommitEpochMustMatchTheParsedEpoch(t *testing.T) {
	f := newFixture(t)
	_, aliceToken := f.user(t, "alice-ev@pheme.test")
	bobID, _ := f.user(t, "bob-ev@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	// A commit whose bytes say epoch 0, but which claims baseEpoch 7.
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/commit", aliceToken, map[string]any{
		"groupId":   "grp-ev",
		"baseEpoch": 7,
		"commit":    publicCommit(0),
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a commit whose epoch does not match baseEpoch: got %d, want 400\n%s", rec.Code, rec.Body)
	}
}

// The same commit, honestly declared, establishes the group. This proves the
// check passes the truthful path rather than blocking everything parseable.
func TestParseableCommitWithMatchingEpochIsAccepted(t *testing.T) {
	f := newFixture(t)
	_, aliceToken := f.user(t, "alice-ev2@pheme.test")
	bobID, _ := f.user(t, "bob-ev2@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/commit", aliceToken, map[string]any{
		"groupId":   "grp-ev2",
		"baseEpoch": 0,
		"commit":    publicCommit(0),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("an honest parseable commit: got %d, want 200\n%s", rec.Code, rec.Body)
	}
}

// A parseable message that is a proposal, not a commit, posted to the commit
// endpoint is refused — the endpoint accepts commits, and now it can tell.
func TestProposalPostedAsACommitIsRefused(t *testing.T) {
	f := newFixture(t)
	_, aliceToken := f.user(t, "alice-ev3@pheme.test")
	bobID, _ := f.user(t, "bob-ev3@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	msg := publicCommit(0)
	msg[len(msg)-1] = 0x02 // content_type: proposal
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/commit", aliceToken, map[string]any{
		"groupId":   "grp-ev3",
		"baseEpoch": 0,
		"commit":    msg,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a proposal posted as a commit: got %d, want 400\n%s", rec.Code, rec.Body)
	}
}

// The rollout tolerance: an opaque commit the parser does not understand (a
// PrivateMessage from a client that has not adopted the new framing) still goes
// through on its declared baseEpoch, exactly as before F4. The CAS and
// reconciliation remain the backstop for those.
func TestOpaqueCommitStillProceedsOnDeclaredEpoch(t *testing.T) {
	f := newFixture(t)
	_, aliceToken := f.user(t, "alice-ev4@pheme.test")
	bobID, _ := f.user(t, "bob-ev4@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/commit", aliceToken, map[string]any{
		"groupId":   "grp-ev4",
		"baseEpoch": 0,
		"commit":    []byte("an-opaque-private-message-commit"),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("an opaque commit at a valid baseEpoch: got %d, want 200\n%s", rec.Code, rec.Body)
	}
}
