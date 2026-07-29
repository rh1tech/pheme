// Package config loads runtime configuration from environment variables.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds settings shared across the Pheme services. Each binary reads the
// subset it needs; unused fields are harmless.
type Config struct {
	// HTTP
	AppAddr    string // App API listen address, e.g. ":8080"
	IngestAddr string // Ingest API listen address, e.g. ":8081"

	// Infrastructure connection strings
	MongoURI  string
	MongoDB   string
	RabbitURI string
	RedisAddr string
	RedisPass string

	// Messaging
	Queue    string // primary work queue name
	Exchange string // exchange for live events / DLX

	// Auth
	JWTSecret       string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	// Push
	FCMCredentialsFile string // path to Firebase service-account JSON

	// APNs, for iOS VoIP pushes only. FCM handles every other iOS notification; it cannot handle this
	// one, because a ringing call needs PushKit and FCM cannot reach it (see push_apns_voip.go).
	// Leave APNsKeyFile empty and iPhones simply fall back to an ordinary alert — a banner instead of
	// a call screen.
	APNsKeyFile    string // path to the .p8 signing key
	APNsKeyID      string
	APNsTeamID     string
	APNsBundleID   string // the VoIP topic is this + ".voip"
	APNsProduction bool   // Apple's production gateway rather than the sandbox

	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string // VAPID contact: an https: URL or mailto: address. Apple Web Push requires an https: URL (it rejects mailto: with 403 BadJwtToken).

	// PublicAPIURL is the externally reachable base URL of the App API (e.g.
	// "https://chat.example.com"), used to build absolute image URLs for push
	// notifications. Empty disables notification images (history still carries them).
	PublicAPIURL string

	// ICE servers for 1:1 voice calls. The server never carries call media — WebRTC
	// takes it peer to peer — but a pair behind symmetric NAT cannot reach each other
	// directly, and TURN is the relay of last resort for them.
	//
	// TURNURLs is a comma-separated list of ICE URLs, e.g.
	//   stun:turn.example.com:3478,turn:turn.example.com:3478?transport=udp,turns:turn.example.com:5349?transport=tcp
	// Empty disables calling entirely rather than shipping a half-working feature.
	//
	// TURNSecret is coturn's `static-auth-secret`. It NEVER leaves the server: what a
	// client receives is a short-lived username/credential pair derived from it, so a
	// leaked credential expires by itself and cannot be turned back into the secret.
	TURNURLs   string
	TURNSecret string
	// TURNTTL is how long an issued TURN credential stays valid. Long enough to place a
	// call and be re-fetched on the next one; short enough that a stolen one is worthless.
	TURNTTL time.Duration

	// Email (transactional mail: verification + password-reset codes)
	MailDriver      string // log | smtp
	SMTPHost        string
	SMTPPort        int
	SMTPFrom        string // From header, e.g. "Pheme <noreply@example.com>"
	SMTPUser        string // optional SMTP AUTH user (empty = no auth)
	SMTPPass        string
	SMTPInsecureTLS bool // skip STARTTLS cert verification (internal relay only)

	// Verification codes
	OTPDriver    string // memory | redis
	CodeTTL      time.Duration
	CodeCooldown time.Duration // minimum interval between code sends per email

	// CORSOrigins is the exact set of browser origins allowed to call this API,
	// e.g. "https://app.example.com". The web SPA is served from a different host
	// than the API, so it genuinely needs CORS.
	//
	// This used to be a blanket "Access-Control-Allow-Origin: *" on every response.
	// That header on an API is both needless exposure and a fingerprint — a censor
	// probing an unknown host learns it is running an API meant for a browser on
	// some other origin, which static hosting never says. An unlisted origin now
	// gets no CORS headers at all rather than a wildcard.
	CORSOrigins []string

	// HostDomain is this instance's own domain, e.g. "chat.example.com".
	// It becomes the issuer and audience of every token, and in a federated
	// network it is the name peers know this host by and the key the nodelist
	// entry is filed under. Empty keeps the pre-federation behaviour.
	HostDomain string

	// NodelistCoordKey is the coordinator public key this host trusts to sign the
	// network's nodelist, base64url-encoded. Empty means this host is not part of
	// a federated network — it verifies no peers and federates with no one, which
	// is the correct default for a standalone instance.
	NodelistCoordKey string

	// NodelistPath is where the signed nodelist is read from at startup and by
	// refresh. Empty disables federation even if a coordinator key is set.
	NodelistPath string

	// PeerURLs overrides where specific peer domains are reached, as a comma list
	// of "domain=baseURL" pairs. Empty means every peer is reached at
	// https://<domain>. For private networks, non-default ports, or a loopback test
	// harness where the nodelist domain is not the URL to dial.
	PeerURLs string

	// HostKey is this instance's Ed25519 signing key, base64url-encoded 32-byte
	// seed. Generate one with `pheme-hostkey`.
	//
	// Empty means tokens keep being signed HS256 with PHEME_JWT_SECRET, which is
	// what every deployment did before federation. That mode cannot survive
	// federation: a token's subject is a bare user id, so two hosts sharing a
	// secret would each accept the other's tokens and authenticate as whichever
	// local user held the same id, with a signature that verifies and nothing
	// visibly wrong.
	//
	// Turning this on signs nobody out ONLY if PHEME_JWT_LEGACY_UNTIL is set to
	// a date far enough ahead to cover the longest refresh token already issued
	// (30 days by default). Without it, tokens signed with the shared secret
	// stop verifying the moment the key is turned on.
	HostKey string

	// LegacyHS256Until is the RFC3339 date until which a host that has a key of
	// its own still honours tokens signed with PHEME_JWT_SECRET.
	//
	// It exists so the switch to asymmetric signing can be made without signing
	// every session out, and it is a date rather than a flag because the window
	// has to close on its own. While it is open the host accepts the weaker
	// algorithm, which is exactly the state the key was meant to leave behind.
	LegacyHS256Until time.Time

	// TrustProxyHeaders decides whether X-Forwarded-For identifies the caller for
	// rate limiting. Set it when a reverse proxy that OVERWRITES the header sits in
	// front (the deploy/nginx configs do); leave it off otherwise, because the header
	// is client-supplied and honouring it without a proxy lets any caller rotate its
	// apparent address and evade every per-IP limit.
	TrustProxyHeaders bool

	// Authorization
	AdminEmails []string // emails granted the admin role on register/login

	// Initial admin seeding. When both are set, the App API ensures a verified,
	// active admin with these credentials exists at startup (created if missing).
	// Opt-in: leaving either empty disables seeding. Used to bootstrap the first
	// admin without the email-verification flow, and by the E2E suite.
	SeedAdminEmail    string
	SeedAdminPassword string

	// Backend selection. Each defaults to a zero-dependency in-memory/log
	// implementation so the services run without external infrastructure.
	// Switch to the real adapters once the docker-compose stack is running.
	StoreDriver     string // memory | mongo
	BlobDriver      string // memory | gridfs
	BrokerDriver    string // memory | rabbit
	LiveDriver      string // memory | redis
	RateLimitDriver string // memory | redis
	PushDriver      string // log | fcm | webpush | both
}

// Load reads configuration from the environment, applying sensible defaults for
// local development.
func Load() Config {
	return Config{
		AppAddr:    env("PHEME_APP_ADDR", ":8080"),
		IngestAddr: env("PHEME_INGEST_ADDR", ":8081"),

		MongoURI:  env("PHEME_MONGO_URI", "mongodb://localhost:27017"),
		MongoDB:   env("PHEME_MONGO_DB", "pheme"),
		RabbitURI: env("PHEME_RABBIT_URI", "amqp://guest:guest@localhost:5672/"),
		RedisAddr: env("PHEME_REDIS_ADDR", "localhost:6379"),
		RedisPass: env("PHEME_REDIS_PASS", ""),

		Queue:    env("PHEME_QUEUE", "pheme.notifications"),
		Exchange: env("PHEME_EXCHANGE", "pheme.events"),

		// No default. A fixed default is a published signing key: this repository
		// is where an attacker would look it up, and `env` treats an empty value
		// as unset, so an operator who follows the documented advice to drop
		// PHEME_JWT_SECRET after switching to a host key would have fallen back
		// onto it. Empty means "the caller decides", and bootstrap does — either
		// an ephemeral random secret for development or a refusal to start.
		JWTSecret:       env("PHEME_JWT_SECRET", ""),
		AccessTokenTTL:  envDuration("PHEME_ACCESS_TTL", 15*time.Minute),
		RefreshTokenTTL: envDuration("PHEME_REFRESH_TTL", 720*time.Hour),

		FCMCredentialsFile: env("PHEME_FCM_CREDENTIALS", ""),

		APNsKeyFile:    env("PHEME_APNS_KEY_FILE", ""),
		APNsKeyID:      env("PHEME_APNS_KEY_ID", ""),
		APNsTeamID:     env("PHEME_APNS_TEAM_ID", ""),
		APNsBundleID:   env("PHEME_APNS_BUNDLE_ID", ""),
		APNsProduction: envBool("PHEME_APNS_PRODUCTION", false),

		VAPIDPublicKey:  env("PHEME_VAPID_PUBLIC", ""),
		VAPIDPrivateKey: env("PHEME_VAPID_PRIVATE", ""),
		VAPIDSubject:    env("PHEME_VAPID_SUBJECT", "https://example.com"),

		PublicAPIURL: env("PHEME_PUBLIC_API_URL", ""),

		TURNURLs:   env("PHEME_TURN_URLS", ""),
		TURNSecret: env("PHEME_TURN_SECRET", ""),
		TURNTTL:    time.Duration(envInt("PHEME_TURN_TTL_SECONDS", 600)) * time.Second,

		MailDriver:      env("PHEME_MAIL_DRIVER", "log"),
		SMTPHost:        env("PHEME_SMTP_HOST", "localhost"),
		SMTPPort:        envInt("PHEME_SMTP_PORT", 25),
		SMTPFrom:        env("PHEME_SMTP_FROM", "Pheme <noreply@example.com>"),
		SMTPUser:        env("PHEME_SMTP_USER", ""),
		SMTPPass:        env("PHEME_SMTP_PASS", ""),
		SMTPInsecureTLS: envBool("PHEME_SMTP_INSECURE_TLS", false),

		OTPDriver:    env("PHEME_OTP_DRIVER", "memory"),
		CodeTTL:      envDuration("PHEME_CODE_TTL", 30*time.Minute),
		CodeCooldown: envDuration("PHEME_CODE_COOLDOWN", 2*time.Minute),

		// Defaults to the Vite dev server so `make dev` works untouched. Every
		// real deployment must set this to its web origin.
		CORSOrigins: envListDefault("PHEME_CORS_ORIGINS", "http://localhost:5173"),

		HostDomain:       env("PHEME_HOST_DOMAIN", ""),
		HostKey:          env("PHEME_HOST_KEY", ""),
		LegacyHS256Until: envTime("PHEME_JWT_LEGACY_UNTIL"),

		TrustProxyHeaders: envBool("PHEME_TRUST_PROXY_HEADERS", false),

		NodelistCoordKey: env("PHEME_NODELIST_COORD_KEY", ""),
		NodelistPath:     env("PHEME_NODELIST_PATH", ""),
		PeerURLs:         env("PHEME_PEER_URLS", ""),

		AdminEmails: envList("PHEME_ADMIN_EMAILS"),

		SeedAdminEmail:    env("PHEME_SEED_ADMIN_EMAIL", ""),
		SeedAdminPassword: env("PHEME_SEED_ADMIN_PASSWORD", ""),

		StoreDriver:     env("PHEME_STORE_DRIVER", "memory"),
		BlobDriver:      env("PHEME_BLOB_DRIVER", "memory"),
		BrokerDriver:    env("PHEME_BROKER_DRIVER", "memory"),
		LiveDriver:      env("PHEME_LIVE_DRIVER", "memory"),
		RateLimitDriver: env("PHEME_RATELIMIT_DRIVER", "memory"),
		PushDriver:      env("PHEME_PUSH_DRIVER", "log"),
	}
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// envTime reads an RFC3339 instant. An unset or unparseable value yields the
// zero time, which every caller reads as "no window" — the safe direction for a
// setting whose only job is to relax a check.
func envTime(key string) time.Time {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(v))
	if err != nil {
		return time.Time{}
	}
	return t
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}

// envList parses a comma-separated env var into a trimmed, lowercased slice.
func envList(key string) []string {
	return envListDefault(key, "")
}

// envListDefault is envList with a fallback used when the variable is UNSET.
// The fallback runs through the same parsing, so it may itself be a list.
//
// Set-but-empty is honoured as an empty list rather than falling back. The two
// are different intentions, and conflating them is not theoretical: compose
// substitutes an unset variable as the empty string, so
// `PHEME_CORS_ORIGINS: ${PHEME_WEB_ORIGIN}` on an instance that serves no web
// app arrives here as "". Treating that as "unset" silently allowed the
// development origin on a production instance and suppressed the startup
// warning written for exactly that case.
func envListDefault(key, def string) []string {
	raw, ok := os.LookupEnv(key)
	if !ok {
		raw = def
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(strings.ToLower(part)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		// Allow plain seconds as an integer fallback.
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return def
}
