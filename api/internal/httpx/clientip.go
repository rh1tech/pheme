package httpx

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP identifies the caller for rate-limiting purposes.
//
// trustProxy decides whether X-Forwarded-For is believed. This is not a
// preference, it is a security setting: the header is client-supplied, so a host
// that honours it WITHOUT a proxy in front lets any caller rotate its apparent
// address freely and defeat every per-IP limit. A host that ignores it BEHIND a
// proxy sees only the proxy and rate-limits all its users as one.
//
// When trusted, the left-most entry is taken — that is the originating client as
// a well-behaved proxy chain records it. This is only sound because the setting
// asserts a proxy that overwrites the header rather than appending to a
// client-supplied one, which is what the nginx configs in deploy/ do.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first, _, found := strings.Cut(xff, ","); found {
				if ip := strings.TrimSpace(first); ip != "" {
					return ip
				}
			} else if ip := strings.TrimSpace(xff); ip != "" {
				return ip
			}
		}
		if real := strings.TrimSpace(r.Header.Get("X-Real-Ip")); real != "" {
			return real
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// No port (some test servers, some transports): the whole value is the host.
		return r.RemoteAddr
	}
	return host
}

// ClientIPIsDistinct reports whether ClientIP returned something that actually
// tells one caller from another.
//
// It does not when a reverse proxy is in front and trustProxy is off: every
// request then carries the proxy's own address, and anything keyed on it is one
// shared bucket for the entire instance rather than a per-caller limit. A
// per-address rate limit in that state does not protect the service, it takes it
// down — the first eight wrong passwords lock out everybody.
//
// Loopback and private ranges are the signal. A caller reaching a public API
// genuinely from 127.0.0.1 or a Docker bridge address is a proxy hop in all but
// the development case, where limiting by address matters least.
func ClientIPIsDistinct(ip string, trustProxy bool) bool {
	if trustProxy {
		return true // the header names the real client
	}
	addr := net.ParseIP(ip)
	if addr == nil {
		return false // unparseable is not something to key a bucket on
	}
	return !addr.IsLoopback() && !addr.IsPrivate() && !addr.IsLinkLocalUnicast()
}
