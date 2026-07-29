package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// okHandler stands in for the real mux; these tests only care about what the
// CORS wrapper adds around it.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("body"))
	})
}

func doWithOrigin(t *testing.T, origins []string, method, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/v1/me", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	withCORS(origins, okHandler()).ServeHTTP(rec, req)
	return rec
}

func TestCORSAllowsAConfiguredOrigin(t *testing.T) {
	rec := doWithOrigin(t, []string{"https://app.example.com"}, http.MethodGet, "https://app.example.com")

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("Allow-Origin = %q, want the request's origin echoed back", got)
	}
	// Without this a shared cache can hand one origin's allowance to another.
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
	}
}

// The important case: an unlisted origin gets no CORS headers at all, not a
// wildcard. A wildcard let any page on the internet call the API, and told an
// unauthenticated prober that this host serves a browser API for some other
// origin — which plain static hosting never does.
func TestCORSIsSilentForAnUnlistedOrigin(t *testing.T) {
	rec := doWithOrigin(t, []string{"https://app.example.com"}, http.MethodGet, "https://evil.example")

	for _, h := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Headers",
		"Access-Control-Allow-Methods",
	} {
		if got := rec.Header().Get(h); got != "" {
			t.Errorf("%s = %q, want unset for an unlisted origin", h, got)
		}
	}
	// The request itself still proceeds — CORS is enforced by the browser, and
	// silently failing non-browser callers would be a different bug.
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the request to be served normally", rec.Code)
	}
}

// A plain server-to-server request carries no Origin. It must not pick up an
// echo of the empty string.
func TestCORSIsSilentWithNoOriginHeader(t *testing.T) {
	rec := doWithOrigin(t, []string{"https://app.example.com"}, http.MethodGet, "")

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want unset when the request has no Origin", got)
	}
}

func TestCORSPreflightShortCircuits(t *testing.T) {
	rec := doWithOrigin(t, []string{"https://app.example.com"}, http.MethodOptions, "https://app.example.com")

	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("preflight body = %q, want empty", rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("preflight carried no Allow-Methods, so the browser learns nothing")
	}
}

// Origins are compared case-insensitively: config lowercases what it parses, so
// the request header must be lowercased too or a browser sending "HTTPS://..."
// would be refused.
func TestCORSMatchesOriginCaseInsensitively(t *testing.T) {
	rec := doWithOrigin(t, []string{"https://app.example.com"}, http.MethodGet, "HTTPS://APP.EXAMPLE.COM")

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "" {
		t.Error("Allow-Origin unset, want a case-insensitive match")
	}
}
