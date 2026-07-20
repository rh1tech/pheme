// Package mlswire reads the few fields a delivery service needs out of an MLS
// handshake message, without an MLS implementation.
//
// The server is not an MLS member: it holds no group secrets and cannot decrypt
// application messages or fully verify a commit's authenticity (that needs the
// group's GroupContext hashes — RFC 9420 §6). But once handshake messages are
// framed as PublicMessage (see the pheme-mls crate's wire_format_policy), the
// FramedContent is in the clear, and the ONE field the server most needs — the
// epoch a Commit is built on — sits near the front of it. Reading it lets the
// server serialise commits on a number it parsed rather than one the client
// merely claimed alongside the opaque bytes.
//
// This is a deliberately minimal decoder: enough of RFC 9420 §6 framing to reach
// the epoch and the content type, and no more. The full commit — proposals,
// signatures — stays opaque here; validating those is the hub's job (F5), and
// needs group state this package does not have.
package mlswire

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// WireFormat values (RFC 9420 §6, IANA "MLS Wire Formats").
const (
	wireFormatPublicMessage  = 0x0001
	wireFormatPrivateMessage = 0x0002
)

// ContentType values (RFC 9420 §6).
const (
	ContentTypeApplication = 0x01
	ContentTypeProposal    = 0x02
	ContentTypeCommit      = 0x03
)

var (
	// ErrNotPublic is returned for a message that is not a PublicMessage — most
	// importantly a PrivateMessage, whose contents the server cannot read. A
	// caller that requires an inspectable commit treats this as "cannot verify".
	ErrNotPublic = errors.New("mlswire: not a PublicMessage (handshake is encrypted)")
	ErrTruncated = errors.New("mlswire: message is truncated")
	ErrMalformed = errors.New("mlswire: malformed message")
)

// Handshake is what the server can read from a plaintext handshake message.
type Handshake struct {
	GroupID     []byte
	Epoch       uint64
	ContentType uint8
}

// ParseHandshake reads an MLSMessage carrying a PublicMessage and returns the
// framed group id, epoch and content type.
//
// The MLSMessage layout (RFC 9420 §6):
//
//	ProtocolVersion version;   // uint16
//	WireFormat wire_format;    // uint16
//	select (wire_format) { case public_message: PublicMessage; ... };
//
// and a PublicMessage begins with its FramedContent:
//
//	opaque group_id<V>;        // varint length + bytes
//	uint64 epoch;
//	Sender sender; ...
//	ContentType content_type;
//
// so group id, epoch and content type are reachable by decoding the two fixed
// headers, one variable-length vector, the epoch, then the sender (whose length
// depends on its type) to reach the content type.
func ParseHandshake(msg []byte) (Handshake, error) {
	r := reader{b: msg}

	if _, err := r.u16(); err != nil { // protocol version — not validated here
		return Handshake{}, err
	}
	wf, err := r.u16()
	if err != nil {
		return Handshake{}, err
	}
	switch wf {
	case wireFormatPublicMessage:
		// good
	case wireFormatPrivateMessage:
		return Handshake{}, ErrNotPublic
	default:
		return Handshake{}, fmt.Errorf("%w: wire format 0x%04x", ErrMalformed, wf)
	}

	// FramedContent.group_id<V>
	groupID, err := r.vec()
	if err != nil {
		return Handshake{}, err
	}
	// FramedContent.epoch
	epoch, err := r.u64()
	if err != nil {
		return Handshake{}, err
	}
	// FramedContent.sender — Sender { SenderType sender_type; select {...} }.
	// The content type comes after it, so the sender must be skipped by type.
	if err := r.skipSender(); err != nil {
		return Handshake{}, err
	}
	// FramedContent.authenticated_data<V>
	if _, err := r.vec(); err != nil {
		return Handshake{}, err
	}
	// FramedContent.content_type
	ct, err := r.u8()
	if err != nil {
		return Handshake{}, err
	}

	return Handshake{GroupID: groupID, Epoch: epoch, ContentType: ct}, nil
}

// reader is a byte cursor over a message.
type reader struct {
	b []byte
	i int
}

func (r *reader) need(n int) error {
	if r.i+n > len(r.b) {
		return ErrTruncated
	}
	return nil
}

func (r *reader) u8() (uint8, error) {
	if err := r.need(1); err != nil {
		return 0, err
	}
	v := r.b[r.i]
	r.i++
	return v, nil
}

func (r *reader) u16() (uint16, error) {
	if err := r.need(2); err != nil {
		return 0, err
	}
	v := binary.BigEndian.Uint16(r.b[r.i:])
	r.i += 2
	return v, nil
}

func (r *reader) u32() (uint32, error) {
	if err := r.need(4); err != nil {
		return 0, err
	}
	v := binary.BigEndian.Uint32(r.b[r.i:])
	r.i += 4
	return v, nil
}

func (r *reader) u64() (uint64, error) {
	if err := r.need(8); err != nil {
		return 0, err
	}
	v := binary.BigEndian.Uint64(r.b[r.i:])
	r.i += 8
	return v, nil
}

// varint decodes an RFC 9420 variable-length integer: the top two bits of the
// first byte give the length (1/2/4/8 bytes), the rest are the value. It is the
// QUIC varint (RFC 9000 §16), which RFC 9420 §2.1.2 adopts for vector lengths.
func (r *reader) varint() (uint64, error) {
	first, err := r.u8()
	if err != nil {
		return 0, err
	}
	prefix := first >> 6
	value := uint64(first & 0x3f)
	extra := 0
	switch prefix {
	case 0:
		extra = 0
	case 1:
		extra = 1
	case 2:
		extra = 3
	case 3:
		extra = 7
	}
	for k := 0; k < extra; k++ {
		b, err := r.u8()
		if err != nil {
			return 0, err
		}
		value = value<<8 | uint64(b)
	}
	return value, nil
}

// vec reads a variable-length vector: a varint length followed by that many
// bytes. Returns a slice into the message.
func (r *reader) vec() ([]byte, error) {
	n, err := r.varint()
	if err != nil {
		return nil, err
	}
	if err := r.need(int(n)); err != nil {
		return nil, err
	}
	out := r.b[r.i : r.i+int(n)]
	r.i += int(n)
	return out, nil
}

// skipSender advances past a Sender struct (RFC 9420 §6):
//
//	SenderType sender_type;   // uint8: member(1), external(2), new_member_proposal(3), new_member_commit(4)
//	select (sender_type) {
//	  case member:               uint32 leaf_index;
//	  case external:             uint32 sender_index;
//	  case new_member_commit:    struct{};   // nothing
//	  case new_member_proposal:  struct{};   // nothing
//	};
func (r *reader) skipSender() error {
	st, err := r.u8()
	if err != nil {
		return err
	}
	switch st {
	case 1, 2: // member / external carry a uint32 index
		_, err := r.u32()
		return err
	case 3, 4: // new_member_* carry nothing
		return nil
	default:
		return fmt.Errorf("%w: sender type %d", ErrMalformed, st)
	}
}
