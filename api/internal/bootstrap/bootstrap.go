// Package bootstrap constructs service dependencies (store, broker, push, live
// bus, rate limiter, tokens) from configuration, selecting in-memory or real
// adapters per the configured drivers. It keeps the three service mains small
// and consistent.
package bootstrap

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/broker"
	"github.com/rh1tech/pheme/api/internal/calls"
	"github.com/rh1tech/pheme/api/internal/config"
	mailer "github.com/rh1tech/pheme/api/internal/email"
	"github.com/rh1tech/pheme/api/internal/federation"
	"github.com/rh1tech/pheme/api/internal/idempotency"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/nodelist"
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
	blob   blob.Store
	tokens *auth.TokenManager
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

// Blob builds (and caches) the configured blob store for processed images.
func (b *Builder) Blob(ctx context.Context) (blob.Store, error) {
	if b.blob != nil {
		return b.blob, nil
	}
	switch b.cfg.BlobDriver {
	case "gridfs":
		b.logger.Info("blob: gridfs", "db", b.cfg.MongoDB)
		bs, err := blob.NewGridFS(ctx, b.cfg.MongoURI, b.cfg.MongoDB)
		if err != nil {
			return nil, err
		}
		b.blob = bs
	case "memory", "":
		b.logger.Info("blob: in-memory")
		b.blob = blob.NewMemory()
	default:
		return nil, fmt.Errorf("unknown blob driver %q", b.cfg.BlobDriver)
	}
	return b.blob, nil
}

// Store builds the configured persistence backend. The blob store is threaded in
// so cascade deletes can remove a deleted message's images.
func (b *Builder) Store(ctx context.Context) (store.Store, error) {
	bs, err := b.Blob(ctx)
	if err != nil {
		return nil, err
	}
	switch b.cfg.StoreDriver {
	case "mongo":
		b.logger.Info("store: mongodb", "db", b.cfg.MongoDB)
		return store.NewMongo(ctx, b.cfg.MongoURI, b.cfg.MongoDB, bs)
	case "memory", "":
		b.logger.Info("store: in-memory")
		return store.NewMemory(bs), nil
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

// CallMailbox builds the short-lived store behind a voice call: its signalling channel and
// the lock that decides which of a person's devices answered.
//
// It follows the live bus: Redis in production, in-process otherwise. The Redis one is not
// an optimisation — the browser placing the call and the browser answering it may be talking
// to two different App API instances, and they must not each believe they won the race to
// answer. Nothing here is durable; it all expires in two minutes.
func (b *Builder) CallMailbox() calls.Mailbox {
	switch b.cfg.LiveDriver {
	case "redis":
		b.logger.Info("call mailbox: redis")
		return calls.NewRedis(b.redisClient(), "pheme:call")
	default:
		b.logger.Info("call mailbox: in-memory")
		return calls.NewMemory()
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

// Dedup builds the store that makes a retried ingest request safe to send twice.
//
// It follows the rate limiter's driver setting rather than having its own: both answer the same
// question — "has this caller already done this?" — and both are only correct when every instance
// shares one view. A deployment that runs Redis for one and memory for the other would deduplicate
// only the retries that happened to land on the same instance, which is worse than not
// deduplicating at all, because it looks like it works.
func (b *Builder) Dedup() idempotency.Store {
	switch b.cfg.RateLimitDriver {
	case "redis":
		b.logger.Info("idempotency: redis")
		return idempotency.NewRedis(b.redisClient(), "pheme:idem:")
	default:
		b.logger.Info("idempotency: in-memory (retries are only deduplicated on this instance)")
		return idempotency.NewMemory()
	}
}

// Push builds the configured push sender.
func (b *Builder) Push(ctx context.Context) (push.Sender, error) {
	switch b.cfg.PushDriver {
	case "fcm":
		fcm, err := push.NewFCMSender(ctx, b.cfg.FCMCredentialsFile, b.cfg.PublicAPIURL)
		if err != nil {
			return nil, err
		}
		// Composed even with no web push, so an iPhone can still be rung over PushKit — which is a
		// thing the FCM sender is structurally incapable of doing on its own.
		return push.NewMultiSender(fcm, nil, b.voip()), nil
	case "webpush":
		web := push.NewWebPushSender(b.cfg.VAPIDPublicKey, b.cfg.VAPIDPrivateKey, b.cfg.VAPIDSubject, b.cfg.PublicAPIURL)
		return push.NewMultiSender(nil, web, b.voip()), nil
	case "both":
		fcm, err := push.NewFCMSender(ctx, b.cfg.FCMCredentialsFile, b.cfg.PublicAPIURL)
		if err != nil {
			return nil, err
		}
		web := push.NewWebPushSender(b.cfg.VAPIDPublicKey, b.cfg.VAPIDPrivateKey, b.cfg.VAPIDSubject, b.cfg.PublicAPIURL)
		return push.NewMultiSender(fcm, web, b.voip()), nil
	case "log", "":
		b.logger.Info("push: log (no-op)")
		return push.NewLogSender(), nil
	default:
		return nil, fmt.Errorf("unknown push driver %q", b.cfg.PushDriver)
	}
}

// voip builds the APNs VoIP sender, or nil when APNs is not configured.
//
// Nil is a supported deployment, not a failure: without it an iPhone still gets an ordinary alert for
// an incoming call — a banner rather than a ringing call screen. That is a degraded experience, so it
// is logged loudly, but it is not a reason to refuse to start. A bad key, on the other hand, IS logged
// as an error and then ignored, because a server that will not boot is worse than one that cannot ring
// an iPhone.
func (b *Builder) voip() push.VoIPSender {
	if b.cfg.APNsKeyFile == "" {
		b.logger.Info("push: APNs not configured — iOS calls will arrive as alerts, not ringing calls")
		return nil
	}

	sender, err := push.NewAPNsVoIPSender(push.APNsConfig{
		KeyFile:    b.cfg.APNsKeyFile,
		KeyID:      b.cfg.APNsKeyID,
		TeamID:     b.cfg.APNsTeamID,
		BundleID:   b.cfg.APNsBundleID,
		Production: b.cfg.APNsProduction,
	})
	if err != nil {
		b.logger.Error("push: APNs VoIP disabled", "error", err)
		return nil
	}

	b.logger.Info("push: APNs VoIP", "topic", b.cfg.APNsBundleID+".voip", "production", b.cfg.APNsProduction)
	return sender
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
	// CACHED, like the blob store, and for a sharper reason than saving an allocation.
	//
	// The session revoker is attached to a manager instance (UseRevoker), and it is what makes
	// "terminate this device" actually bite. A second manager would be a second verifier with NO
	// revoker on it — accepting tokens that had been revoked, and undoing a security control
	// silently. There is one caller today; this makes a second one safe rather than subtly wrong.
	if b.tokens == nil {
		b.tokens = auth.NewTokenManager(b.cfg.JWTSecret, b.cfg.AccessTokenTTL, b.cfg.RefreshTokenTTL)
		if key, err := auth.ParseHostKey(b.cfg.HostKey); err != nil {
			// Refuse to start rather than fall back to the shared secret. A host
			// that silently ignored a malformed key would keep issuing tokens
			// its operator believed were asymmetric, and the whole point of the
			// key is that nobody has to wonder which mode a host is in.
			panic(fmt.Sprintf("PHEME_HOST_KEY is set but unusable: %v", err))
		} else if key != nil {
			if b.cfg.HostDomain == "" {
				panic("PHEME_HOST_KEY is set without PHEME_HOST_DOMAIN: a signing key with no issuer")
			}
			b.tokens.UseHostKey(key, b.cfg.HostDomain)
		}
	}
	return b.tokens
}

// Nodelist builds the store of trusted peers from the configured coordinator
// key and signed list, or returns (nil, nil) when federation is not configured.
//
// Federation is off unless BOTH a coordinator key and a list path are set. A
// key with no list, or a list with no key, is a half-configured host, and the
// safe reading of that is "not federated" rather than "trust something".
//
// A missing or malformed list is fatal, not ignored: an operator who configured
// federation and whose host silently came up trusting no peers would have a
// mystery, not an error. A host that means to stand alone simply sets neither.
func (b *Builder) Nodelist() (*nodelist.Store, error) {
	if b.cfg.NodelistCoordKey == "" || b.cfg.NodelistPath == "" {
		if b.cfg.NodelistCoordKey != "" || b.cfg.NodelistPath != "" {
			b.logger.Warn("federation half-configured — need both PHEME_NODELIST_COORD_KEY and PHEME_NODELIST_PATH; staying standalone")
		}
		return nil, nil
	}
	coord, err := parseCoordKey(b.cfg.NodelistCoordKey)
	if err != nil {
		return nil, fmt.Errorf("nodelist coordinator key: %w", err)
	}
	s := nodelist.NewStore(coord)
	if err := s.LoadFile(b.cfg.NodelistPath); err != nil {
		return nil, fmt.Errorf("loading nodelist from %s: %w", b.cfg.NodelistPath, err)
	}
	b.logger.Info("nodelist loaded", "serial", s.Serial())
	return s, nil
}

// FederationClient builds the signing client this host uses to call peers, or
// (nil, nil) when the host has no signing key — in which case it cannot federate
// and callers do no cross-host work.
func (b *Builder) FederationClient() (*federation.Client, error) {
	key, err := auth.ParseHostKey(b.cfg.HostKey)
	if err != nil {
		return nil, fmt.Errorf("host key: %w", err)
	}
	if key == nil || b.cfg.HostDomain == "" {
		return nil, nil
	}
	keyID := auth.HostKeyID(key.Public().(ed25519.PublicKey))
	client := federation.NewClient(b.cfg.HostDomain, keyID, key)
	// A peer is reached at https://<domain> by default. PHEME_PEER_URLS overrides
	// that for domains reachable at a non-default URL — a private network, a test
	// harness on loopback, a peer behind a different port. Format: a comma list of
	// domain=baseURL pairs, e.g. "b.test=http://localhost:8100,c.test=https://c:9443".
	if overrides := parsePeerURLs(b.cfg.PeerURLs); len(overrides) > 0 {
		client.PeerURL = func(domain string) string {
			if u, ok := overrides[domain]; ok {
				return u
			}
			return federation.PeerBaseURL(domain)
		}
	}
	return client, nil
}

// parsePeerURLs reads the "domain=baseURL,..." override list into a map. Malformed
// entries are skipped rather than failing startup — a bad override should degrade
// to the default URL, not take the host down.
func parsePeerURLs(list string) map[string]string {
	if strings.TrimSpace(list) == "" {
		return nil
	}
	out := map[string]string{}
	for _, pair := range strings.Split(list, ",") {
		domain, url, ok := strings.Cut(strings.TrimSpace(pair), "=")
		domain, url = strings.TrimSpace(domain), strings.TrimSpace(url)
		if ok && domain != "" && url != "" {
			out[domain] = strings.TrimRight(url, "/")
		}
	}
	return out
}

// Close releases shared resources (the cached Redis client and blob store).
func (b *Builder) Close() error {
	if b.blob != nil {
		_ = b.blob.Close(context.Background())
	}
	if b.redis != nil {
		return b.redis.Close()
	}
	return nil
}

// parseCoordKey decodes a base64url-encoded Ed25519 public key.
func parseCoordKey(encoded string) (ed25519.PublicKey, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(encoded), "="))
	if err != nil {
		return nil, fmt.Errorf("not base64url: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("is %d bytes, want %d", len(b), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}
