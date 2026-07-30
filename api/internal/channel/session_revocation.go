package channel

import (
	"context"
	"errors"
	"time"
)

var errSessionRevokerUnavailable = errors.New("session revoker is not configured")

type userSessionRevoker interface {
	RevokeUserBefore(ctx context.Context, userID string, cutoff, expiresAt time.Time) error
}

// revokeUserSessions chooses the next whole JWT second as the cutoff. JWT iat
// values have second precision, so this also catches tokens issued earlier in
// the current second.
func revokeUserSessions(
	ctx context.Context,
	revoker userSessionRevoker,
	userID string,
	ttl time.Duration,
) (time.Time, error) {
	if revoker == nil || ttl <= 0 {
		return time.Time{}, errSessionRevokerUnavailable
	}
	cutoff := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	if err := revoker.RevokeUserBefore(ctx, userID, cutoff, cutoff.Add(ttl)); err != nil {
		return time.Time{}, err
	}
	return cutoff, nil
}
