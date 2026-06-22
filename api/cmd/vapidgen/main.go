// Command vapidgen prints a new Web Push VAPID key pair.
//
// Usage:
//
//	go run ./cmd/vapidgen            # human-readable
//	go run ./cmd/vapidgen -env       # as PHEME_VAPID_* env lines
//
// The keys authenticate the server to browser push services. Generate them once
// per environment and keep the private key secret.
package main

import (
	"flag"
	"fmt"
	"os"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func main() {
	asEnv := flag.Bool("env", false, "print as PHEME_VAPID_* environment lines")
	flag.Parse()

	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		fmt.Fprintln(os.Stderr, "vapidgen:", err)
		os.Exit(1)
	}

	if *asEnv {
		fmt.Printf("PHEME_VAPID_PUBLIC=%s\n", public)
		fmt.Printf("PHEME_VAPID_PRIVATE=%s\n", private)
		return
	}
	fmt.Printf("public:  %s\n", public)
	fmt.Printf("private: %s\n", private)
}
