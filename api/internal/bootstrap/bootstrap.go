// Package bootstrap constructs service dependencies (store, broker, push, live
// bus, rate limiter, tokens) from configuration, selecting in-memory or real
// adapters per the configured drivers. It keeps the three service mains small
// and consistent.
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/redis/go-redis/v9"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/broker"
	"github.com/rh1tech/pheme/api/internal/config"
	mailer "github.com/rh1tech/pheme/api/internal/email"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/otp"
	"github.com/rh1tech/pheme/api/internal/push"
	"github.com/rh1tech/pheme/api/internal/ratelimit"
	"github.com/rh1tech/pheme/api/internal/store"
)

// Builder constructs dependencies on demand, caching shared clients (e.g. Redis)
// so multiple components reuse a single connection.
type Builder struct {
	cfg    config.Config
	logger *slog.Logger
	redis  *redis.Client
}

// New returns a Builder for the given configuration.
func New(cfg config.Config, logger *slog.Logger) *Builder {
	if logger == nil {
		logger = slog.Default()
	}
	return &Builder{cfg: cfg, logger: logger}
}

func (b *Builder) redisClient() *redis.Client {
	if b.redis == nil {
		b.redis = redis.NewClient(&redis.Options{Addr: b.cfg.RedisAddr, Password: b.cfg.RedisPass})
	}
	return b.redis
}

// Store builds the configured persistence backend.
func (b *Builder) Store(ctx context.Context) (store.Store, error) {
	switch b.cfg.StoreDriver {
	case "mongo":
		b.logger.Info("store: mongodb", "db", b.cfg.MongoDB)
		return store.NewMongo(ctx, b.cfg.MongoURI, b.cfg.MongoDB)
	case "memory", "":
		b.logger.Info("store: in-memory")
		return store.NewMemory(), nil
	default:
		return nil, fmt.Errorf("unknown store driver %q", b.cfg.StoreDriver)
	}
}

// Publisher builds the configured broker publisher.
func (b *Builder) Publisher() (broker.Publisher, error) {
	switch b.cfg.BrokerDriver {
	case "rabbit":
		b.logger.Info("broker: rabbitmq", "queue", b.cfg.Queue)
		return broker.NewRabbit(b.cfg.RabbitURI, b.cfg.Queue)
	case "memory", "":
		b.logger.Info("broker: in-memory (publisher)")
		return broker.NewMemory(0), nil
	default:
		return nil, fmt.Errorf("unknown broker driver %q", b.cfg.BrokerDriver)
	}
}

// Consumer builds the configured broker consumer.
func (b *Builder) Consumer() (broker.Consumer, error) {
	switch b.cfg.BrokerDriver {
	case "rabbit":
		b.logger.Info("broker: rabbitmq", "queue", b.cfg.Queue)
		return broker.NewRabbit(b.cfg.RabbitURI, b.cfg.Queue)
	case "memory", "":
		b.logger.Info("broker: in-memory (consumer)")
		return broker.NewMemory(0), nil
	default:
		return nil, fmt.Errorf("unknown broker driver %q", b.cfg.BrokerDriver)
	}
}

// Live builds the configured live event bus.
func (b *Builder) Live() (live.Bus, error) {
	switch b.cfg.LiveDriver {
	case "redis":
		b.logger.Info("live: redis pub/sub", "channel", b.cfg.Exchange)
		return live.NewRedisBus(b.redisClient(), b.cfg.Exchange, b.logger), nil
	case "memory", "":
		b.logger.Info("live: in-memory")
		return live.NewMemoryBus(), nil
	default:
		return nil, fmt.Errorf("unknown live driver %q", b.cfg.LiveDriver)
	}
}

// Limiter builds the configured rate limiter (20 req/s, burst 40, per channel).
func (b *Builder) Limiter() ratelimit.Limiter {
	const rate, burst = 20.0, 40.0
	switch b.cfg.RateLimitDriver {
	case "redis":
		b.logger.Info("ratelimit: redis")
		return ratelimit.NewRedisLimiter(b.redisClient(), rate, burst, "pheme:rl")
	default:
		b.logger.Info("ratelimit: in-memory")
		return ratelimit.NewTokenBucket(rate, burst)
	}
}

// Push builds the configured push sender.
func (b *Builder) Push(ctx context.Context) (push.Sender, error) {
	switch b.cfg.PushDriver {
	case "fcm":
		return push.NewFCMSender(ctx, b.cfg.FCMCredentialsFile)
	case "webpush":
		return push.NewWebPushSender(b.cfg.VAPIDPublicKey, b.cfg.VAPIDPrivateKey, b.cfg.VAPIDSubject), nil
	case "both":
		fcm, err := push.NewFCMSender(ctx, b.cfg.FCMCredentialsFile)
		if err != nil {
			return nil, err
		}
		web := push.NewWebPushSender(b.cfg.VAPIDPublicKey, b.cfg.VAPIDPrivateKey, b.cfg.VAPIDSubject)
		return push.NewMultiSender(fcm, web), nil
	case "log", "":
		b.logger.Info("push: log (no-op)")
		return push.NewLogSender(), nil
	default:
		return nil, fmt.Errorf("unknown push driver %q", b.cfg.PushDriver)
	}
}

// Codes builds the verification-code store (pending signups, reset codes,
// cooldowns).
func (b *Builder) Codes() otp.Store {
	switch b.cfg.OTPDriver {
	case "redis":
		b.logger.Info("otp: redis")
		return otp.NewRedis(b.redisClient(), "pheme:otp")
	default:
		b.logger.Info("otp: in-memory")
		return otp.NewMemory()
	}
}

// Mailer builds the configured transactional mail sender.
func (b *Builder) Mailer() (mailer.Sender, error) {
	switch b.cfg.MailDriver {
	case "smtp":
		b.logger.Info("mail: smtp", "host", b.cfg.SMTPHost, "port", b.cfg.SMTPPort)
		return mailer.NewSMTPSender(b.cfg.SMTPHost, b.cfg.SMTPPort, b.cfg.SMTPFrom, b.cfg.SMTPUser, b.cfg.SMTPPass, b.cfg.SMTPInsecureTLS)
	case "log", "":
		b.logger.Info("mail: log (not sent)")
		return mailer.NewLogSender(b.logger), nil
	default:
		return nil, fmt.Errorf("unknown mail driver %q", b.cfg.MailDriver)
	}
}

// Tokens builds the JWT token manager.
func (b *Builder) Tokens() *auth.TokenManager {
	return auth.NewTokenManager(b.cfg.JWTSecret, b.cfg.AccessTokenTTL, b.cfg.RefreshTokenTTL)
}

// Close releases shared resources (currently the cached Redis client).
func (b *Builder) Close() error {
	if b.redis != nil {
		return b.redis.Close()
	}
	return nil
}
