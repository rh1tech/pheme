package store

import (
	"context"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

func TestAppendAssignsMonotonicSeqPerConversation(t *testing.T) {
	m := NewMemory(nil)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, id := range []string{"c1", "c2"} {
		if _, err := m.CreateConversation(ctx, domain.Conversation{ID: id, Kind: domain.ConversationGroup, CreatedAt: now}, nil); err != nil {
			t.Fatal(err)
		}
	}

	// Three messages in c1 get 1,2,3; c2 has its own counter starting at 1.
	var seqs []int64
	for i := 0; i < 3; i++ {
		out, _ := m.AppendChatMessage(ctx, domain.ChatMessage{ConversationID: "c1", SenderID: "a", Ciphertext: []byte("x"), CreatedAt: now})
		seqs = append(seqs, out.Seq)
	}
	if seqs[0] != 1 || seqs[1] != 2 || seqs[2] != 3 {
		t.Fatalf("c1 seqs = %v, want [1 2 3]", seqs)
	}
	out, _ := m.AppendChatMessage(ctx, domain.ChatMessage{ConversationID: "c2", SenderID: "a", Ciphertext: []byte("x"), CreatedAt: now})
	if out.Seq != 1 {
		t.Errorf("c2 first seq = %d, want 1 (per-conversation counter)", out.Seq)
	}
}

// A message that already carries a sequence — one relayed from the hub — keeps it;
// the store must not reassign and fork the order.
func TestAppendPreservesAnAlreadyAssignedSeq(t *testing.T) {
	m := NewMemory(nil)
	ctx := context.Background()
	now := time.Now().UTC()
	_, _ = m.CreateConversation(ctx, domain.Conversation{ID: "c1", Kind: domain.ConversationGroup, CreatedAt: now}, nil)

	out, _ := m.AppendChatMessage(ctx, domain.ChatMessage{ConversationID: "c1", SenderID: "hub", Seq: 42, Ciphertext: []byte("relayed"), CreatedAt: now})
	if out.Seq != 42 {
		t.Fatalf("preset seq = %d, want 42 (must not be reassigned)", out.Seq)
	}
}

// Two messages sharing a timestamp come back in a deterministic order — by seq,
// newest first — rather than however the map happened to iterate.
func TestTranscriptTieBreaksBySeq(t *testing.T) {
	m := NewMemory(nil)
	ctx := context.Background()
	ts := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) // identical for both
	_, _ = m.CreateConversation(ctx, domain.Conversation{ID: "c1", Kind: domain.ConversationGroup, CreatedAt: ts}, nil)

	_, _ = m.AppendChatMessage(ctx, domain.ChatMessage{ConversationID: "c1", SenderID: "a", Ciphertext: []byte("first"), CreatedAt: ts})
	_, _ = m.AppendChatMessage(ctx, domain.ChatMessage{ConversationID: "c1", SenderID: "a", Ciphertext: []byte("second"), CreatedAt: ts})

	got, err := m.ChatMessagesByConversation(ctx, "c1", "", 10, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Seq <= got[1].Seq {
		t.Fatalf("tie not broken by seq (newest first): got %d,%d", got[0].Seq, got[1].Seq)
	}
}
