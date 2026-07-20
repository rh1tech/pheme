package mlswire

import (
	"encoding/hex"
	"errors"
	"testing"
)

// A real Commit produced by the pheme-mls crate, at group epoch 0, framed as a
// PublicMessage. Regenerate with:
//
//	cargo test -p pheme-mls commits_are_public -- --nocapture
//
// and the crate test asserts the same framing, so the two ends cannot drift: if
// the crate stops emitting PublicMessage, its own test fails; if the wire layout
// shifts, this parse fails.
const commitEpoch0Hex = "00010001146772702d30313233343536373839616263646566000000000000000001000000000003411e0100010001000120c526381029654e856c0f9d43e7ecb4b0d621e770e5e301de0079aa9b5f52c54920ce452270e3fcef6007dd24fcb16a8b455c5596391dd49dd2f0933a5f369a4a772084c07ac425ab00558b38217150e882626a93ca19703def42feb216af5a011ad6000109626f623a6465762d6202000108000100020003004d000002000101000000006a5e94ee000000006acd60fe00404019ce674ace44b9ef0f6b60d2281634d892814ce402cd028116d6511e836839ff20f036e2b326a2392b1ad0c0e31d563a26a16d5c2b756afa49ede72c90f71d0300404080f48b7ca8a31f3523d0ef85ac81dc49970e86f9ca6ae19864d8ce034bb204a58312cfd4dcbebafa1da1479f686d6043f4d7001a29a6af64c1c094fb349f2d0f01207fb0dc86f0ea17ae84b63352b3d207c6b745b61e08cebcfcc1841d5f0d1f230f208e7a8cf918ba8a92ae3f8591ef3e847a569dc78880a774a2a9f3ad60e1dce9f200010b616c6963653a6465762d6102000108000100020003004d000002000103201666bafa1a6d4a7a35e2d317f66fb7dba60ea5547a37a54be02e746ebb6eff480040404b39e8cdd44c01e8495205c6d5a109c3b14a79a451a56be7aa41fd147983453043fbbd88aba38bb35cb0f3939c230f70b4be8947c6d2074243d293f5d621280722206a4cb9250bd2b88fcfea0050d9dc3781f6711bfa0eb8c2842f1ffdac8a770f4d004040407ede762d0db4bd2c5e084d20c43b6a24ea81fbd6505c2e11ab1381dbcc852ca87ebf02629029ca1ba9f560b1bec75201729f5c972c343c76449054207cc802201b116cb491c56f46dd68b967811bc414ef8273dc8fce6694d6d4086d33653fbf20deff693d73fcd7b8661d3e4cdb6c03a3b7f9023a140ac6066770582519a9b243"

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The point of the whole package: read the epoch a real commit is built on. A
// client that sent a different baseEpoch alongside these bytes would be caught,
// because the epoch is now parsed from the commit itself.
func TestParsesEpochAndContentTypeFromARealCommit(t *testing.T) {
	h, err := ParseHandshake(mustHex(t, commitEpoch0Hex))
	if err != nil {
		t.Fatalf("parsing a real commit failed: %v", err)
	}
	if h.Epoch != 0 {
		t.Errorf("epoch = %d, want 0", h.Epoch)
	}
	if h.ContentType != ContentTypeCommit {
		t.Errorf("content type = %d, want commit (%d)", h.ContentType, ContentTypeCommit)
	}
	if string(h.GroupID) != "grp-0123456789abcdef" {
		t.Errorf("group id = %q, want the fixture's", h.GroupID)
	}
}

// A PrivateMessage — the old framing, or an application message — is refused
// distinctly, so a caller can tell "encrypted, cannot inspect" from "malformed".
func TestPrivateMessageIsRefusedAsNotPublic(t *testing.T) {
	// version 0x0001, wire_format 0x0002 (private_message), then arbitrary bytes.
	priv := []byte{0x00, 0x01, 0x00, 0x02, 0xff, 0xff}
	if _, err := ParseHandshake(priv); err != ErrNotPublic {
		t.Errorf("err = %v, want ErrNotPublic", err)
	}
}

func TestTruncatedMessageIsRefused(t *testing.T) {
	full := mustHex(t, commitEpoch0Hex)
	for _, n := range []int{0, 1, 3, 4, 8, 20} {
		if _, err := ParseHandshake(full[:n]); err == nil {
			t.Errorf("a %d-byte prefix parsed without error", n)
		}
	}
}

func TestUnknownWireFormatIsMalformed(t *testing.T) {
	if _, err := ParseHandshake([]byte{0x00, 0x01, 0x09, 0x09, 0x00}); !errors.Is(err, ErrMalformed) {
		t.Errorf("err = %v, want ErrMalformed", err)
	}
}

// The varint decoder is the one piece of real wire logic; check each length
// class against known QUIC-varint encodings (RFC 9000 §16).
func TestVarint(t *testing.T) {
	cases := []struct {
		enc  []byte
		want uint64
	}{
		{[]byte{0x00}, 0},
		{[]byte{0x25}, 37},                      // 1-byte, top bits 00
		{[]byte{0x40, 0x25}, 37},                // 2-byte, top bits 01
		{[]byte{0x7b, 0xbd}, 15293},             // 2-byte
		{[]byte{0x80, 0x00, 0x40, 0x00}, 16384}, // 4-byte, top bits 10
	}
	for _, c := range cases {
		r := reader{b: c.enc}
		got, err := r.varint()
		if err != nil {
			t.Fatalf("varint(% x): %v", c.enc, err)
		}
		if got != c.want {
			t.Errorf("varint(% x) = %d, want %d", c.enc, got, c.want)
		}
	}
}
