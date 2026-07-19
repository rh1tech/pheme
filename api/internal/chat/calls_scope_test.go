package chat

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Which conversation a call belongs to.
//
// The call id is minted by the client and arrives in the URL path, so on its own it is a claim
// rather than a fact. Every call handler proved the caller belonged to the conversation in the path
// and then used the call id verbatim — authorising the caller against A conversation, never against
// THIS call.
//
// The damaging one is the answer lock. It is first-to-answer and atomic, and it exists precisely
// because a losing device must be told for certain rather than over a bus that may drop the
// message. A stranger who claimed it first would make the real callee's device believe it lost:
// it stops ringing and puts the microphone away, the call cannot be answered, and nothing anywhere
// reports a fault. The caller hears it ring out.

// twoConversations sets up a victim's call and an unrelated conversation the attacker is in.
type callScopeWorld struct {
	victimToken, attackerToken string
	victimConv, attackerConv   string
	callID                     string
}

func newCallScopeWorld(t *testing.T, f *fixture, tag string) callScopeWorld {
	t.Helper()
	f.enableCalls()

	_, victimToken := f.user(t, "callee-"+tag+"@pheme.test")
	peer, _ := f.user(t, "caller-"+tag+"@pheme.test")
	victimConv := f.createDirect(t, victimToken, peer)

	_, attackerToken := f.user(t, "attacker-"+tag+"@pheme.test")
	attackerPeer, _ := f.user(t, "attacker-peer-"+tag+"@pheme.test")
	attackerConv := f.createDirect(t, attackerToken, attackerPeer)

	return callScopeWorld{
		victimToken: victimToken, attackerToken: attackerToken,
		victimConv: victimConv, attackerConv: attackerConv,
		callID: "call-" + tag,
	}
}

func (w callScopeWorld) signal(t *testing.T, f *fixture, token, conv, body string) *httpRec {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/calls/"+w.callID+"/signal", token,
		map[string]any{"ciphertext": []byte(body)})
	return &httpRec{rec.Code, rec.Body.String()}
}

type httpRec struct {
	code int
	body string
}

// THE ONE THAT MATTERS. Somebody in another conversation must not be able to answer this call.
func TestAnotherConversationCannotClaimThisCallsAnswerLock(t *testing.T) {
	f := newFixture(t)
	w := newCallScopeWorld(t, f, "claim")

	// The caller places the call.
	if rec := w.signal(t, f, w.victimToken, w.victimConv, "the invite"); rec.code != http.StatusOK {
		t.Fatalf("place call = %d: %s", rec.code, rec.body)
	}

	// A stranger, legitimately inside their OWN conversation, quotes this call id.
	rec := f.do(http.MethodPost, "/v1/conversations/"+w.attackerConv+"/calls/"+w.callID+"/accept",
		w.attackerToken, map[string]any{"deviceId": "attacker-device"})
	if rec.Code != http.StatusOK && rec.Code != http.StatusConflict {
		t.Fatalf("unexpected status from the stranger's accept: %d (%s)", rec.Code, rec.Body)
	}

	// The callee's own device now answers. It must WIN: if the stranger's claim had reached this
	// call, this device would be told it lost, stop ringing and close the microphone.
	rec = f.do(http.MethodPost, "/v1/conversations/"+w.victimConv+"/calls/"+w.callID+"/accept",
		w.victimToken, map[string]any{"deviceId": "callees-phone"})
	if rec.Code != http.StatusOK {
		t.Fatalf("the callee's device lost the race to a stranger in another conversation "+
			"(%d: %s); the call rings out and nothing reports a fault", rec.Code, rec.Body)
	}
	var out struct {
		Winner string `json:"winner"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Winner != "callees-phone" {
		t.Errorf("winner = %q, want the callee's own device", out.Winner)
	}
}

// A stranger must not read another call's signals. They are sealed, so this is a metadata leak
// rather than an eavesdrop — that a call is happening, how far along it is — but it is not theirs.
func TestAnotherConversationCannotReadThisCallsSignals(t *testing.T) {
	f := newFixture(t)
	w := newCallScopeWorld(t, f, "read")

	if rec := w.signal(t, f, w.victimToken, w.victimConv, "sealed invite"); rec.code != http.StatusOK {
		t.Fatalf("place call = %d: %s", rec.code, rec.body)
	}

	rec := f.do(http.MethodGet,
		"/v1/conversations/"+w.attackerConv+"/calls/"+w.callID+"/signals", w.attackerToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("signals = %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Signals []struct {
			Seq        int    `json:"seq"`
			Ciphertext []byte `json:"ciphertext"`
		} `json:"signals"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Signals) != 0 {
		t.Errorf("a stranger in another conversation read %d signals from this call: %+v",
			len(out.Signals), out.Signals)
	}
}

// Nor inject into it. The signal would not decrypt — it is sealed under the other conversation's
// group key — but a client made to process a foreign blob is a client being handed garbage.
func TestAnotherConversationCannotInjectIntoThisCall(t *testing.T) {
	f := newFixture(t)
	w := newCallScopeWorld(t, f, "inject")

	if rec := w.signal(t, f, w.victimToken, w.victimConv, "real invite"); rec.code != http.StatusOK {
		t.Fatalf("place call = %d", rec.code)
	}
	if rec := w.signal(t, f, w.attackerToken, w.attackerConv, "forged"); rec.code != http.StatusOK {
		t.Fatalf("the stranger's own signal = %d", rec.code)
	}

	// The real participants see only their own.
	rec := f.do(http.MethodGet,
		"/v1/conversations/"+w.victimConv+"/calls/"+w.callID+"/signals", w.victimToken, nil)
	var out struct {
		Signals []struct {
			Ciphertext []byte `json:"ciphertext"`
		} `json:"signals"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, s := range out.Signals {
		if string(s.Ciphertext) == "forged" {
			t.Fatal("a signal from another conversation landed in this call")
		}
	}
	if len(out.Signals) != 1 {
		t.Errorf("the call holds %d signals, want only its own", len(out.Signals))
	}
}

// The ordinary path still works end to end: place, fetch, answer, and a second device loses.
func TestACallStillWorksWithinItsOwnConversation(t *testing.T) {
	f := newFixture(t)
	w := newCallScopeWorld(t, f, "happy")

	if rec := w.signal(t, f, w.victimToken, w.victimConv, "invite"); rec.code != http.StatusOK {
		t.Fatalf("place = %d: %s", rec.code, rec.body)
	}
	if rec := w.signal(t, f, w.victimToken, w.victimConv, "answer"); rec.code != http.StatusOK {
		t.Fatalf("answer = %d", rec.code)
	}

	rec := f.do(http.MethodGet,
		"/v1/conversations/"+w.victimConv+"/calls/"+w.callID+"/signals", w.victimToken, nil)
	var out struct {
		Signals []struct {
			Seq        int    `json:"seq"`
			Ciphertext []byte `json:"ciphertext"`
		} `json:"signals"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Signals) != 2 || out.Signals[0].Seq != 1 || out.Signals[1].Seq != 2 {
		t.Fatalf("signals came back as %+v, want two in order", out.Signals)
	}

	first := f.do(http.MethodPost, "/v1/conversations/"+w.victimConv+"/calls/"+w.callID+"/accept",
		w.victimToken, map[string]any{"deviceId": "phone"})
	if first.Code != http.StatusOK {
		t.Fatalf("first accept = %d", first.Code)
	}
	// A second device of the same person must be told it lost, for certain.
	second := f.do(http.MethodPost, "/v1/conversations/"+w.victimConv+"/calls/"+w.callID+"/accept",
		w.victimToken, map[string]any{"deviceId": "laptop"})
	if second.Code != http.StatusConflict {
		t.Errorf("the second device = %d, want 409 — a loser told nothing keeps ringing with a "+
			"live microphone", second.Code)
	}
	// And the same device retrying is not losing a race against itself.
	retry := f.do(http.MethodPost, "/v1/conversations/"+w.victimConv+"/calls/"+w.callID+"/accept",
		w.victimToken, map[string]any{"deviceId": "phone"})
	if retry.Code != http.StatusOK {
		t.Errorf("the winning device retrying = %d, want 200", retry.Code)
	}
}

// The same call id used in two different conversations is two different calls, which is what makes
// a client-minted id safe to trust here at all.
func TestTheSameCallIDInTwoConversationsIsTwoCalls(t *testing.T) {
	f := newFixture(t)
	w := newCallScopeWorld(t, f, "distinct")

	if rec := w.signal(t, f, w.victimToken, w.victimConv, "theirs"); rec.code != http.StatusOK {
		t.Fatalf("first = %d", rec.code)
	}
	if rec := w.signal(t, f, w.attackerToken, w.attackerConv, "mine"); rec.code != http.StatusOK {
		t.Fatalf("second = %d", rec.code)
	}

	for _, tc := range []struct{ conv, token, want string }{
		{w.victimConv, w.victimToken, "theirs"},
		{w.attackerConv, w.attackerToken, "mine"},
	} {
		rec := f.do(http.MethodGet, "/v1/conversations/"+tc.conv+"/calls/"+w.callID+"/signals", tc.token, nil)
		var out struct {
			Signals []struct {
				Ciphertext []byte `json:"ciphertext"`
			} `json:"signals"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(out.Signals) != 1 || string(out.Signals[0].Ciphertext) != tc.want {
			t.Errorf("conversation %s sees %+v, want exactly its own %q", tc.conv, out.Signals, tc.want)
		}
	}
}
