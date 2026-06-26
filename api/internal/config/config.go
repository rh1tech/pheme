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
	VAPIDPublicKey     string
	VAPIDPrivateKey    string
	VAPIDSubject       string // VAPID contact: an https: URL or mailto: address. Apple Web Push requires an https: URL (it rejects mailto: with 403 BadJwtToken).

	// Email (transactional mail: verification + password-reset codes)
	MailDriver      string // log | smtp
	SMTPHost        string
	SMTPPort        int
	SMTPFrom        string // From header, e.g. "Pheme <noreply@app.example.com>"
	SMTPUser        string // optional SMTP AUTH user (empty = no auth)
	SMTPPass        string
	SMTPInsecureTLS bool // skip STARTTLS cert verification (internal relay only)

	// Verification codes
	OTPDriver    string // memory | redis
	CodeTTL      time.Duration
	CodeCooldown time.Duration // minimum interval between code sends per email

	// Authorization
	AdminEmails []string // emails granted the admin role on register/login

	// Backend selection. Each defaults to a zero-dependency in-memory/log
	// implementation so the services run without external infrastructure.
	// Switch to the real adapters once the docker-compose stack is running.
	StoreDriver     string // memory | mongo
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

		JWTSecret:       env("PHEME_JWT_SECRET", "dev-insecure-change-me"),
		AccessTokenTTL:  envDuration("PHEME_ACCESS_TTL", 15*time.Minute),
		RefreshTokenTTL: envDuration("PHEME_REFRESH_TTL", 720*time.Hour),

		FCMCredentialsFile: env("PHEME_FCM_CREDENTIALS", ""),
		VAPIDPublicKey:     env("PHEME_VAPID_PUBLIC", ""),
		VAPIDPrivateKey:    env("PHEME_VAPID_PRIVATE", ""),
		VAPIDSubject:       env("PHEME_VAPID_SUBJECT", "https://app.example.com"),

		MailDriver:      env("PHEME_MAIL_DRIVER", "log"),
		SMTPHost:        env("PHEME_SMTP_HOST", "localhost"),
		SMTPPort:        envInt("PHEME_SMTP_PORT", 25),
		SMTPFrom:        env("PHEME_SMTP_FROM", "Pheme <noreply@app.example.com>"),
		SMTPUser:        env("PHEME_SMTP_USER", ""),
		SMTPPass:        env("PHEME_SMTP_PASS", ""),
		SMTPInsecureTLS: envBool("PHEME_SMTP_INSECURE_TLS", false),

		OTPDriver:    env("PHEME_OTP_DRIVER", "memory"),
		CodeTTL:      envDuration("PHEME_CODE_TTL", 30*time.Minute),
		CodeCooldown: envDuration("PHEME_CODE_COOLDOWN", 2*time.Minute),

		AdminEmails: envList("PHEME_ADMIN_EMAILS"),

		StoreDriver:     env("PHEME_STORE_DRIVER", "memory"),
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
	raw, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
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
