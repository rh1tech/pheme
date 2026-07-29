package config

import (
	"testing"
	"time"
)

// Configuration is the layer where a typo becomes an outage, and it had no tests at all.
//
// That is not hypothetical here. A push driver that resolved to the wrong value meant Android
// notifications had never once been delivered in production, and nothing failed loudly — the server
// came up, served every request, and quietly sent nothing. Config errors do not crash; they change
// behaviour and wait.

func TestEnvFallsBackWhenUnsetOrEmpty(t *testing.T) {
	cases := []struct {
		name  string
		set   bool
		value string
		want  string
	}{
		{"unset", false, "", "fallback"},
		{"empty", true, "", "fallback"},
		// An empty variable means "not configured", not "configured to nothing". A deploy that
		// exports PHEME_X= with no value must get the default rather than a blank setting.
		{"whitespace is a real value", true, " ", " "},
		{"set", true, "chosen", "chosen"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("PHEME_TEST_ENV", tc.value)
			}
			if got := env("PHEME_TEST_ENV", "fallback"); got != tc.want {
				t.Errorf("env = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnvIntRejectsNonsenseRatherThanZeroing(t *testing.T) {
	cases := []struct {
		name  string
		value string
		set   bool
		want  int
	}{
		{"unset", "", false, 42},
		{"empty", "", true, 42},
		{"a number", "7", true, 7},
		{"padded", "  7  ", true, 7},
		{"negative", "-1", true, -1},
		// The important one: a typo must leave the default in place, not silently become zero. A
		// zero limit, zero timeout or zero retry count is a very different server.
		{"not a number", "seven", true, 42},
		{"half a number", "7x", true, 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("PHEME_TEST_INT", tc.value)
			}
			if got := envInt("PHEME_TEST_INT", 42); got != tc.want {
				t.Errorf("envInt(%q) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}
}

func TestEnvBoolAcceptsWhatPeopleActuallyWrite(t *testing.T) {
	truthy := []string{"1", "true", "TRUE", "True", "yes", "YES", "on", "ON", " true "}
	falsy := []string{"0", "false", "FALSE", "no", "NO", "off", "OFF", " false "}

	for _, v := range truthy {
		t.Run("true/"+v, func(t *testing.T) {
			t.Setenv("PHEME_TEST_BOOL", v)
			if !envBool("PHEME_TEST_BOOL", false) {
				t.Errorf("envBool(%q) = false, want true", v)
			}
		})
	}
	for _, v := range falsy {
		t.Run("false/"+v, func(t *testing.T) {
			t.Setenv("PHEME_TEST_BOOL", v)
			if envBool("PHEME_TEST_BOOL", true) {
				t.Errorf("envBool(%q) = true, want false", v)
			}
		})
	}

	// Anything unrecognised keeps the default. Guessing at "maybe" or "enabled" would mean a
	// setting quietly flipping on a value its author thought meant the opposite.
	for _, v := range []string{"maybe", "enabled", "2", "y"} {
		t.Run("unrecognised/"+v, func(t *testing.T) {
			t.Setenv("PHEME_TEST_BOOL", v)
			if envBool("PHEME_TEST_BOOL", true) != true {
				t.Errorf("envBool(%q) did not keep the default", v)
			}
			if envBool("PHEME_TEST_BOOL", false) != false {
				t.Errorf("envBool(%q) did not keep the default", v)
			}
		})
	}
}

func TestEnvListTrimsLowercasesAndDropsBlanks(t *testing.T) {
	cases := []struct {
		name  string
		value string
		set   bool
		want  []string
	}{
		{"unset", "", false, nil},
		{"empty", "", true, nil},
		{"only whitespace", "   ", true, nil},
		{"one", "Admin@Example.com", true, []string{"admin@example.com"}},
		{"several", "A@x.com, B@x.com", true, []string{"a@x.com", "b@x.com"}},
		// Trailing commas are what a hand-edited env file looks like.
		{"trailing comma", "a@x.com,", true, []string{"a@x.com"}},
		{"empty entries", "a@x.com,,b@x.com", true, []string{"a@x.com", "b@x.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("PHEME_TEST_LIST", tc.value)
			}
			got := envList("PHEME_TEST_LIST")
			if len(got) != len(tc.want) {
				t.Fatalf("envList(%q) = %v, want %v", tc.value, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("envList(%q)[%d] = %q, want %q", tc.value, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestEnvDurationAcceptsUnitsAndBarePlainSeconds(t *testing.T) {
	cases := []struct {
		name  string
		value string
		set   bool
		want  time.Duration
	}{
		{"unset", "", false, 30 * time.Second},
		{"empty", "", true, 30 * time.Second},
		{"with units", "5m", true, 5 * time.Minute},
		{"milliseconds", "1500ms", true, 1500 * time.Millisecond},
		// A bare integer means seconds. Someone writing "60" means a minute, and rejecting it
		// silently in favour of the default would be worse than either reading.
		{"bare integer", "60", true, 60 * time.Second},
		// Nonsense keeps the default rather than becoming zero — a zero timeout is not a timeout.
		{"nonsense", "soon", true, 30 * time.Second},
		{"unit typo", "5mins", true, 30 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("PHEME_TEST_DUR", tc.value)
			}
			if got := envDuration("PHEME_TEST_DUR", 30*time.Second); got != tc.want {
				t.Errorf("envDuration(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

// Load is what the server actually runs. Its defaults are the shape of a development machine, and
// they must not be the shape of production by accident.
func TestLoadDefaults(t *testing.T) {
	cfg := Load()

	if cfg.AppAddr != ":8080" {
		t.Errorf("AppAddr = %q, want :8080", cfg.AppAddr)
	}
	// The push driver defaults to "log": a server with nothing configured writes notifications to
	// its log rather than pretending to deliver them. A default of fcm or webpush would fail
	// silently against credentials that are not there.
	if cfg.PushDriver != "log" {
		t.Errorf("PushDriver = %q, want log — an unconfigured server must not pretend to deliver", cfg.PushDriver)
	}
}

func TestLoadReadsTheEnvironment(t *testing.T) {
	t.Setenv("PHEME_APP_ADDR", ":9999")
	t.Setenv("PHEME_PUSH_DRIVER", "both")
	t.Setenv("PHEME_JWT_SECRET", "a-real-secret")

	cfg := Load()

	if cfg.AppAddr != ":9999" {
		t.Errorf("AppAddr = %q, want :9999", cfg.AppAddr)
	}
	// This one has history: the compose file hardcoded a driver, so a stack configured for "both"
	// ran as webpush and Android received nothing at all, for a day, without a single error.
	if cfg.PushDriver != "both" {
		t.Errorf("PushDriver = %q, want both", cfg.PushDriver)
	}
	if cfg.JWTSecret != "a-real-secret" {
		t.Errorf("JWTSecret was not read from the environment")
	}
}

// There must be NO default JWT secret. A fixed one is a published signing key —
// this repository is where an attacker looks it up — and because `env` treats an
// empty value as unset, an operator who dropped PHEME_JWT_SECRET (as the
// federation notes once advised) fell straight back onto it. Empty here means
// "the caller decides", and bootstrap decides: a random per-process secret for
// development, or a refusal to start on a known placeholder.
func TestLoadHasNoDefaultJWTSecret(t *testing.T) {
	t.Setenv("PHEME_JWT_SECRET", "")
	if got := Load().JWTSecret; got != "" {
		t.Errorf("JWTSecret defaults to %q; any fixed default is a forgeable signing key", got)
	}
}

// The legacy-HS256 window is a deadline, so it closes on its own. An unset or
// unparseable value must read as "no window" — the safe direction for a setting
// whose only effect is to relax a check.
func TestLegacyHS256WindowDefaultsToClosed(t *testing.T) {
	t.Setenv("PHEME_JWT_LEGACY_UNTIL", "")
	if got := Load().LegacyHS256Until; !got.IsZero() {
		t.Errorf("LegacyHS256Until = %v, want zero when unset", got)
	}
	t.Setenv("PHEME_JWT_LEGACY_UNTIL", "next tuesday")
	if got := Load().LegacyHS256Until; !got.IsZero() {
		t.Errorf("LegacyHS256Until = %v, want zero for an unparseable value", got)
	}
	t.Setenv("PHEME_JWT_LEGACY_UNTIL", "2026-09-01T00:00:00Z")
	if got := Load().LegacyHS256Until; got.IsZero() {
		t.Error("a valid RFC3339 deadline was not read")
	}
}

// An unset PHEME_CORS_ORIGINS falls back to the Vite dev server so `make dev`
// works untouched.
func TestCORSOriginsDefaultToTheDevServerWhenUnset(t *testing.T) {
	cfg := Load()
	if len(cfg.CORSOrigins) != 1 || cfg.CORSOrigins[0] != "http://localhost:5173" {
		t.Errorf("CORSOrigins = %v, want the dev server", cfg.CORSOrigins)
	}
}

// Set-but-empty must mean "no browser origin may call this API", NOT "use the
// default". This is not a hypothetical distinction: compose substitutes an
// unset variable as the empty string, so `PHEME_CORS_ORIGINS: ${PHEME_WEB_ORIGIN}`
// on a mobile-only instance arrives as "". Falling back there allowed the
// development origin on a production instance, and suppressed the startup
// warning that exists to catch a stack.env missing the variable.
func TestCORSOriginsSetButEmptyMeansNoneNotDefault(t *testing.T) {
	t.Setenv("PHEME_CORS_ORIGINS", "")
	if got := Load().CORSOrigins; len(got) != 0 {
		t.Errorf("CORSOrigins = %v, want empty when the variable is set to the empty string", got)
	}
}

func TestCORSOriginsParseAListAndLowercase(t *testing.T) {
	t.Setenv("PHEME_CORS_ORIGINS", "https://A.example , https://b.example")
	got := Load().CORSOrigins
	want := []string{"https://a.example", "https://b.example"}
	if len(got) != len(want) {
		t.Fatalf("CORSOrigins = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("CORSOrigins[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
