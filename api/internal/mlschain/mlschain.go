// Package mlschain is the signed ordering chain for a federated MLS group.
//
// A federated conversation has one hub that orders every commit (see
// docs/federation.md and docs/adr-federation-hub-migration.md). A follower has
// to take the hub's word for that order — unless the order is made tamper-
// evident. This package is what makes it so: each accepted commit gets a link
//
//	hash = H( prevHash ‖ seq ‖ groupID ‖ commit )
//
// binding its POSITION (seq, prevHash) to its CONTENT (the commit ciphertext)
// and to the GROUP. Because the hash is a pure function of those inputs, the hub
// and every follower compute the identical value independently — a follower that
// derives a different hash from its own prevHash has caught the hub reordering,
// dropping, or forking the log. The hub also signs the hash with its host key, so
// a relay or a malicious follower cannot fabricate a link the hub never made.
//
// seq is the MLS epoch the commit produces (they advance 1:1), so nothing new is
// counted; the chain rides on the epoch the CAS already serialises.
package mlschain

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
)

// Link computes the chain hash for a commit at position seq, following prevHash.
//
// prevHash is the previous link's hash — nil/empty for the first commit in a
// group. groupID is the MLS group id; commit is the commit control message's
// ciphertext exactly as stored and relayed. The inputs are length-prefixed so
// that no two distinct (prevHash, seq, groupID, commit) tuples can collide by
// running their bytes together.
func Link(prevHash []byte, seq int64, groupID string, commit []byte) []byte {
	h := sha256.New()
	writeField(h, prevHash)
	var s [8]byte
	binary.BigEndian.PutUint64(s[:], uint64(seq))
	h.Write(s[:]) // fixed width, no length prefix needed
	writeField(h, []byte(groupID))
	writeField(h, commit)
	return h.Sum(nil)
}

// writeField writes a length-prefixed byte field, so concatenation is
// unambiguous: ("ab","c") and ("a","bc") hash differently.
func writeField(h interface{ Write([]byte) (int, error) }, b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	h.Write(n[:])
	h.Write(b)
}

// Sign signs a chain hash with the hub's host key. The signature travels with the
// relayed commit so a follower can confirm the hub — not some intermediary —
// authorised this position.
func Sign(key ed25519.PrivateKey, hash []byte) []byte {
	return ed25519.Sign(key, hash)
}

// Verify reports whether sig is the hub's signature over hash. A nil/short key or
// signature is not valid — a missing signature must never read as a valid one.
func Verify(hubKey ed25519.PublicKey, hash, sig []byte) bool {
	if len(hubKey) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(hubKey, hash, sig)
}
