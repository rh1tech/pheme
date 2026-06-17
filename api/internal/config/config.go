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
	VAPIDSubject       string // mailto: contact for Web Push

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
		VAPIDSubject:       env("PHEME_VAPID_SUBJECT", "mailto:admin@example.com"),

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
