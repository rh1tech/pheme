package federation

import (
	"context"
	"errors"
	"strings"
)

// ErrRemoteUserNotFound is returned by ResolveRemoteUser when the peer has no
// user with that username. Callers distinguish it from a transport failure so a
// mistyped handle reads as "no such user" rather than "the other host is down".
var ErrRemoteUserNotFound = errors.New("federation: remote user not found")

// RemoteUser is a peer's answer to "who is username@yourhost": the id this host
// must use to add them to a conversation, plus a display name for confirmation.
type RemoteUser struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	Username    string `json:"username"`
}

// ResolveRemoteUser asks homeDomain to map one of its usernames to a local id,
// so a user typed as `username@homeDomain` can be added to a conversation. The
// call is signed and only a nodelist peer will answer it.
func (c *Client) ResolveRemoteUser(ctx context.Context, homeDomain, username string) (RemoteUser, error) {
	var out RemoteUser
	err := c.PostJSON(ctx, homeDomain, "/federation/v1/resolve-user",
		map[string]string{"username": username}, &out)
	if err != nil {
		// Do maps every non-2xx to an error string carrying the status; a 404 is
		// the peer saying "no such user", which the caller must not treat as an
		// outage.
		if strings.Contains(err.Error(), "returned 404") {
			return RemoteUser{}, ErrRemoteUserNotFound
		}
		return RemoteUser{}, err
	}
	if out.UserID == "" {
		return RemoteUser{}, ErrRemoteUserNotFound
	}
	return out, nil
}
