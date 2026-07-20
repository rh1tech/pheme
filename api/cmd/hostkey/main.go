// Command hostkey prints a new Ed25519 signing key for a Pheme instance.
//
// Usage:
//
//	pheme-hostkey                 # human-readable
//	pheme-hostkey -env            # as PHEME_HOST_KEY=…
//
// The key is the instance's identity. It signs the tokens the instance issues,
// and in a federated network its public half is the instance's nodelist entry —
// what every other host uses to tell this host's word from a forgery. Generate
// it once per instance and keep the private half secret; publishing the public
// half is the point.
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"os"

	"github.com/rh1tech/pheme/api/internal/auth"
)

func main() {
	asEnv := flag.Bool("env", false, "print as a PHEME_HOST_KEY environment line")
	flag.Parse()

	key, seed, err := auth.NewHostKey()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hostkey:", err)
		os.Exit(1)
	}
	pub := key.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)

	if *asEnv {
		fmt.Printf("PHEME_HOST_KEY=%s\n", seed)
		return
	}
	fmt.Printf("private (PHEME_HOST_KEY):  %s\n", seed)
	fmt.Printf("public  (nodelist entry):  %s\n", base64.RawURLEncoding.EncodeToString(pub))
	fmt.Printf("key id:                    %s\n", base64.RawURLEncoding.EncodeToString(sum[:12]))
}
