package bootstrap

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/rh1tech/pheme/api/internal/config"
)

// Driver selection: which implementation the server actually runs.
//
// This is the layer that decides whether messages go to a real push service or into a log, whether
// data lands in Mongo or in a map that vanishes on restart. Getting it wrong does not crash
// anything — it silently runs the wrong thing, which is exactly how Android notifications were
// never delivered for a day while every request returned 200.
//
// Two properties matter here and nothing else does: the DEFAULT must be the safe, dependency-free
// one, and an unrecognised driver must be REFUSED rather than quietly falling back. A typo in a
// deployment that starts anyway is an outage nobody is looking for.

func quietBuilder(cfg config.Config) *Builder {
	return New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestBlobDriverSelection(t *testing.T) {
	ctx := context.Background()

	t.Run("defaults to memory", func(t *testing.T) {
		b := quietBuilder(config.Config{})
		s, err := b.Blob(ctx)
		if err != nil || s == nil {
			t.Fatalf("Blob() = %v, %v", s, err)
		}
	})

	t.Run("memory is explicit too", func(t *testing.T) {
		b := quietBuilder(config.Config{BlobDriver: "memory"})
		if _, err := b.Blob(ctx); err != nil {
			t.Errorf("Blob(memory) = %v", err)
		}
	})

	// A typo must not start the server on a store that forgets everything at restart.
	t.Run("an unknown driver is refused", func(t *testing.T) {
		b := quietBuilder(config.Config{BlobDriver: "gridfsss"})
		_, err := b.Blob(ctx)
		if err == nil {
			t.Fatal("an unknown blob driver was accepted")
		}
		if !strings.Contains(err.Error(), "gridfsss") {
			t.Errorf("error %q does not name the offending value", err)
		}
	})

	// Built once and reused: two blob stores would mean an image written through one and read
	// through the other, which in the memory driver is simply a missing image.
	t.Run("is built once and cached", func(t *testing.T) {
		b := quietBuilder(config.Config{})
		first, err := b.Blob(ctx)
		if err != nil {
			t.Fatalf("first: %v", err)
		}
		second, err := b.Blob(ctx)
		if err != nil {
			t.Fatalf("second: %v", err)
		}
		if first != second {
			t.Error("Blob() built a second, separate store; an image written to one would be missing from the other")
		}
	})
}

func TestStoreDriverSelection(t *testing.T) {
	ctx := context.Background()

	t.Run("defaults to memory", func(t *testing.T) {
		s, err := quietBuilder(config.Config{}).Store(ctx)
		if err != nil || s == nil {
			t.Fatalf("Store() = %v, %v", s, err)
		}
	})

	t.Run("an unknown driver is refused", func(t *testing.T) {
		_, err := quietBuilder(config.Config{StoreDriver: "mongoo"}).Store(ctx)
		if err == nil {
			t.Fatal("an unknown store driver was accepted")
		}
		if !strings.Contains(err.Error(), "mongoo") {
			t.Errorf("error %q does not name the offending value", err)
		}
	})

	// A bad blob driver must fail the store too, rather than leaving cascade deletes with nothing
	// to delete images through.
	t.Run("propagates a blob failure", func(t *testing.T) {
		_, err := quietBuilder(config.Config{BlobDriver: "nonsense"}).Store(ctx)
		if err == nil {
			t.Error("a store was built on top of an invalid blob driver")
		}
	})
}

func TestBrokerDriverSelection(t *testing.T) {
	t.Run("publisher defaults to memory", func(t *testing.T) {
		p, err := quietBuilder(config.Config{}).Publisher()
		if err != nil || p == nil {
			t.Fatalf("Publisher() = %v, %v", p, err)
		}
	})

	t.Run("consumer defaults to memory", func(t *testing.T) {
		c, err := quietBuilder(config.Config{}).Consumer()
		if err != nil || c == nil {
			t.Fatalf("Consumer() = %v, %v", c, err)
		}
	})

	for _, name := range []string{"Publisher", "Consumer"} {
		t.Run(name+" refuses an unknown driver", func(t *testing.T) {
			b := quietBuilder(config.Config{BrokerDriver: "rabbitt"})
			var err error
			if name == "Publisher" {
				_, err = b.Publisher()
			} else {
				_, err = b.Consumer()
			}
			if err == nil {
				t.Fatalf("%s accepted an unknown broker driver", name)
			}
			if !strings.Contains(err.Error(), "rabbitt") {
				t.Errorf("error %q does not name the offending value", err)
			}
		})
	}
}

// THE ONE WITH HISTORY. A push driver that resolves to the wrong value delivers nothing and
// reports nothing: the server comes up, serves every request, and the notifications simply never
// arrive. That happened here for a day.
func TestPushDriverSelection(t *testing.T) {
	ctx := context.Background()

	t.Run("defaults to the log sender", func(t *testing.T) {
		s, err := quietBuilder(config.Config{}).Push(ctx)
		if err != nil || s == nil {
			t.Fatalf("Push() = %v, %v", s, err)
		}
		// An unconfigured server must not pretend to deliver — it writes to the log, where a
		// developer can read the code out of it.
		if !strings.Contains(strings.ToLower(typeName(s)), "log") {
			t.Errorf("the default push sender is %s, want the log sender", typeName(s))
		}
	})

	t.Run("webpush needs no credentials file", func(t *testing.T) {
		s, err := quietBuilder(config.Config{PushDriver: "webpush"}).Push(ctx)
		if err != nil || s == nil {
			t.Fatalf("Push(webpush) = %v, %v", s, err)
		}
	})

	// fcm and both need a credentials file, and must say so rather than starting and silently
	// dropping every Android notification.
	for _, driver := range []string{"fcm", "both"} {
		t.Run(driver+" fails loudly without credentials", func(t *testing.T) {
			_, err := quietBuilder(config.Config{PushDriver: driver}).Push(ctx)
			if err == nil {
				t.Errorf("Push(%s) started with no credentials file; every Android notification "+
					"would be dropped in silence", driver)
			}
		})
	}

	t.Run("an unknown driver is refused", func(t *testing.T) {
		_, err := quietBuilder(config.Config{PushDriver: "fcmm"}).Push(ctx)
		if err == nil {
			t.Fatal("an unknown push driver was accepted")
		}
		if !strings.Contains(err.Error(), "fcmm") {
			t.Errorf("error %q does not name the offending value", err)
		}
	})
}

// Missing APNs is a supported deployment, not a failure: without it an iPhone still gets an
// ordinary alert for a call. A server that refused to boot over it would be worse.
func TestVoIPIsOptional(t *testing.T) {
	var logs strings.Builder
	b := New(config.Config{}, slog.New(slog.NewTextHandler(&logs, nil)))

	if s := b.voip(); s != nil {
		t.Errorf("voip() = %v with no key file, want nil", s)
	}
	// Logged loudly, because it IS a degraded experience even though it is allowed.
	if !strings.Contains(logs.String(), "APNs not configured") {
		t.Errorf("the absence of APNs was not logged: %s", logs.String())
	}
}

// A BAD key is different from a missing one — but still must not stop the server. A process that
// will not boot is worse than one that cannot ring an iPhone.
func TestVoIPWithABadKeyIsLoggedAndDisabled(t *testing.T) {
	var logs strings.Builder
	b := New(config.Config{
		APNsKeyFile:  "/definitely/not/a/key.p8",
		APNsKeyID:    "K",
		APNsTeamID:   "T",
		APNsBundleID: "tech.rh1.pheme",
	}, slog.New(slog.NewTextHandler(&logs, nil)))

	if s := b.voip(); s != nil {
		t.Errorf("voip() = %v with an unreadable key, want nil", s)
	}
	if !strings.Contains(logs.String(), "APNs VoIP disabled") {
		t.Errorf("a bad APNs key was not reported: %s", logs.String())
	}
}

func TestCodesAndMailerDefaults(t *testing.T) {
	b := quietBuilder(config.Config{})

	if c := b.Codes(); c == nil {
		t.Error("Codes() returned nil; signup would panic rather than fail")
	}
	m, err := b.Mailer()
	if err != nil || m == nil {
		t.Fatalf("Mailer() = %v, %v", m, err)
	}
	// The default writes to the log so a development machine with no mail server can still
	// complete a signup — the six-digit code is readable there.
	if !strings.Contains(strings.ToLower(typeName(m)), "log") {
		t.Errorf("the default mailer is %s, want the log sender", typeName(m))
	}
}

func TestTokensAreBuiltOnce(t *testing.T) {
	b := quietBuilder(config.Config{JWTSecret: "s"})
	first, second := b.Tokens(), b.Tokens()
	if first == nil {
		t.Fatal("Tokens() returned nil")
	}
	// Two managers would mean tokens signed by one and verified by another — which works only by
	// accident, until one of them is given a different secret.
	if first != second {
		t.Error("Tokens() built a second manager")
	}
}

// Close must be safe on a builder that never opened anything: shutdown runs it unconditionally.
func TestCloseOnAnUnusedBuilderIsSafe(t *testing.T) {
	if err := quietBuilder(config.Config{}).Close(); err != nil {
		t.Errorf("Close() on an unused builder = %v", err)
	}
}

func typeName(v any) string {
	if v == nil {
		return "<nil>"
	}
	return strings.TrimPrefix(fmt.Sprintf("%T", v), "*")
}
