package store

import "github.com/rh1tech/pheme/api/internal/domain"

// commitCiphertext returns the ciphertext of the Commit in a commit batch — the
// bytes the ordering chain binds. A batch is a Welcome (optional, first) followed
// by the Commit; the chain is over the Commit, since that is the event every
// member applies. Returns nil if the batch carries no commit, which keeps Link
// total rather than panicking on a malformed batch.
func commitCiphertext(msgs []domain.ChatMessage) []byte {
	for _, m := range msgs {
		if m.ContentType == domain.ContentTypeMLSCommit {
			return m.Ciphertext
		}
	}
	return nil
}

// stampChainHash returns a copy of the batch with the chain hash written onto the
// Commit message. Immutable: it does not mutate the caller's slice contents.
func stampChainHash(msgs []domain.ChatMessage, hash []byte) []domain.ChatMessage {
	out := make([]domain.ChatMessage, len(msgs))
	copy(out, msgs)
	for i := range out {
		if out[i].ContentType == domain.ContentTypeMLSCommit {
			out[i].MLSChainHash = hash
		}
	}
	return out
}
