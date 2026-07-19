package main

import (
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"time"
)

// An opt-in profiling endpoint.
//
// It exists because a question came up that could not be answered any other way: the server was
// burning three cores while holding a thousand idle SSE connections, and no log line, metric or
// amount of reading was going to say which function was doing it. Guessing at a profile is how an
// afternoon gets spent fixing the wrong thing.
//
// It is off unless PHEME_DEBUG_ADDR is set, and it must never be bound to a public interface —
// net/http/pprof exposes command line, memory contents and a CPU profiler that anyone can trigger.
// Bind it to localhost and reach it through an SSH tunnel:
//
//	PHEME_DEBUG_ADDR=127.0.0.1:6060
//	go tool pprof http://127.0.0.1:6060/debug/pprof/profile?seconds=20
func startDebugServer(logger *slog.Logger) {
	addr := os.Getenv("PHEME_DEBUG_ADDR")
	if addr == "" {
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
		// No write timeout: a CPU profile is a long, slow response by design, and a timeout here
		// would truncate every profile worth taking.
		ReadHeaderTimeout: 5 * time.Second,
	}
	logger.Warn("pprof enabled — do not expose this port", "addr", addr)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("pprof server", "error", err)
		}
	}()
}
