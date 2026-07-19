package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Putting the epoch back when a Commit could not be relayed.
//
// Advancing a group is two writes that are not one transaction: the conversation's epoch moves by
// compare-and-set, then the Commit is appended to the message log. If the first succeeds and the
// second does not, the group is left at an epoch nobody can reach — every other member sits one
// behind forever, with no Commit in the log to catch up on, and the conversation simply stops
// working. Not slowly, not for one person: permanently, for everyone in it.
//
// That is a bad enough outcome to be worth proving rather than assuming, and it had no test.
//
// Mongo only, deliberately. The in-memory store cannot fail between the two writes — there is no
// second failure mode to roll back from — so a conformance test would assert nothing there. This is
// exactly the kind of divergence a shared test suite hides: the half that can go wrong is the half
// that was never exercised.

// hugeCiphertext is larger than the 16MB ceiling Mongo puts on a single document, so appending it
// fails while the epoch update that preceded it has already committed. That is the real sequence,
// reached the way it would be reached in production rather than by injecting an error.
func hugeCiphertext() []byte {
	return []byte(strings.Repeat("x", 17*1024*1024))
}

func TestMongo_AFailedCommitPutsTheEpochBack(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		if s.name != "mongo" {
			t.Skip("the in-memory store cannot fail between the epoch write and the message append")
		}
		ctx := context.Background()
		conv := mustConversation(t, s.store, "owner")

		// Establish the group properly first, so the failure under test is an ADVANCE — the case
		// where there is a real epoch to put back.
		if _, _, err := s.store.CommitMLSGroup(ctx, conv.ID, "group-1", 0, []domain.ChatMessage{{
			ConversationID: conv.ID, SenderID: "owner", Ciphertext: []byte("first commit"),
			ContentType: "application/x-mls-commit", MLSEpoch: 1, MLSGroupID: "group-1",
			CreatedAt: time.Now().UTC(),
		}}); err != nil {
			t.Fatalf("establish: %v", err)
		}

		before, err := s.store.MLSGroupState(ctx, conv.ID)
		if err != nil {
			t.Fatalf("read state: %v", err)
		}
		if before.Epoch != 1 || before.GroupID != "group-1" {
			t.Fatalf("setup: state is %+v, want group-1 at epoch 1", before)
		}

		// Now a Commit that cannot be stored.
		_, _, err = s.store.CommitMLSGroup(ctx, conv.ID, "group-1", 1, []domain.ChatMessage{{
			ConversationID: conv.ID, SenderID: "owner", Ciphertext: hugeCiphertext(),
			ContentType: "application/x-mls-commit", MLSEpoch: 2, MLSGroupID: "group-1",
			CreatedAt: time.Now().UTC(),
		}})
		if err == nil {
			t.Fatal("a Commit too large to store was reported as committed")
		}

		after, err := s.store.MLSGroupState(ctx, conv.ID)
		if err != nil {
			t.Fatalf("read state: %v", err)
		}
		if after.Epoch != before.Epoch || after.GroupID != before.GroupID {
			t.Errorf("after a Commit that could not be relayed the group is at %+v, want it back at "+
				"%+v. Every other member now sits an epoch behind with no Commit in the log to catch "+
				"up on, and the conversation stops working for all of them.", after, before)
		}

		// And the group still works: a later Commit from the epoch the rollback restored is
		// accepted, which is the whole point of putting it back.
		if _, _, err := s.store.CommitMLSGroup(ctx, conv.ID, "group-1", after.Epoch, []domain.ChatMessage{{
			ConversationID: conv.ID, SenderID: "owner", Ciphertext: []byte("a commit that fits"),
			ContentType: "application/x-mls-commit", MLSEpoch: after.Epoch + 1, MLSGroupID: "group-1",
			CreatedAt: time.Now().UTC(),
		}}); err != nil {
			t.Errorf("the group could not be advanced after the rollback: %v", err)
		}
	})
}

// The same failure while ESTABLISHING a group takes a different path: there was no group before, so
// there must be none after. Leaving a group id behind with no Commit in the log would make the
// conversation permanently unestablishable — every later attempt would be told a group already
// exists, and no member could ever join it.
func TestMongo_AFailedEstablishLeavesNoGroupBehind(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		if s.name != "mongo" {
			t.Skip("the in-memory store cannot fail between the epoch write and the message append")
		}
		ctx := context.Background()
		conv := mustConversation(t, s.store, "owner")

		_, _, err := s.store.CommitMLSGroup(ctx, conv.ID, "doomed-group", 0, []domain.ChatMessage{{
			ConversationID: conv.ID, SenderID: "owner", Ciphertext: hugeCiphertext(),
			ContentType: "application/x-mls-commit", MLSEpoch: 1, MLSGroupID: "doomed-group",
			CreatedAt: time.Now().UTC(),
		}})
		if err == nil {
			t.Fatal("an establishing Commit too large to store was reported as committed")
		}

		state, err := s.store.MLSGroupState(ctx, conv.ID)
		if err != nil {
			t.Fatalf("read state: %v", err)
		}
		if state.GroupID != "" || state.Epoch != 0 {
			t.Errorf("after a failed establish the conversation claims group %q at epoch %d; every "+
				"later attempt is told a group already exists, and nobody can ever join it",
				state.GroupID, state.Epoch)
		}

		// Proof that it is genuinely unestablished: a fresh attempt succeeds.
		if _, _, err := s.store.CommitMLSGroup(ctx, conv.ID, "real-group", 0, []domain.ChatMessage{{
			ConversationID: conv.ID, SenderID: "owner", Ciphertext: []byte("a commit that fits"),
			ContentType: "application/x-mls-commit", MLSEpoch: 1, MLSGroupID: "real-group",
			CreatedAt: time.Now().UTC(),
		}}); err != nil {
			t.Errorf("the conversation could not establish a group after a failed attempt: %v", err)
		}
	})
}
