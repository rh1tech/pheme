// Command nodelist is the network coordinator's tool: it holds the roster of
// admitted hosts and signs it into the document every host mirrors.
//
//	pheme-nodelist init                         # create a coordinator key
//	pheme-nodelist add   <domain> <pubkey> [alias]  # admit a host (alias optional)
//	pheme-nodelist remove <domain>              # remove a host (revocation)
//	pheme-nodelist sign  [--days 30]            # emit the signed list to stdout
//	pheme-nodelist pubkey                       # the key every host must trust
//
// The roster is a plain JSON file (`roster.json` by default), human-readable and
// diffable — admission is a reviewed change to it. The coordinator key
// (`coordinator.key`) signs the list and is the one secret here; its public half
// is what every host is configured to trust, so publishing that half is the
// point and losing the private half means the whole network must be re-keyed.
//
// This is deliberately a file-and-CLI tool, not a service. A nodelist changes
// rarely and admission is a human decision; a running service would be more
// attack surface for no benefit.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rh1tech/pheme/api/internal/nodelist"
)

const (
	keyFile    = "coordinator.key"
	rosterFile = "roster.json"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "init":
		cmdInit()
	case "pubkey":
		cmdPubkey()
	case "add":
		cmdAdd(os.Args[2:])
	case "remove":
		cmdRemove(os.Args[2:])
	case "sign":
		cmdSign(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `pheme-nodelist — federated network roster tool

  init                     create a coordinator key (refuses to clobber one)
  pubkey                   print the public key every host must be given
  add <domain> <pubkey> [alias]
                           admit a host (pubkey from pheme-hostkey); the optional
                           alias is a short network-wide name, e.g. pheme1
  remove <domain>          remove a host — this is how revocation happens
  sign [--days N]          sign the roster and print the list to stdout

Files, in the working directory: coordinator.key (secret), roster.json.
`)
	os.Exit(2)
}

// roster is the coordinator's editable source. Serial lives here so it survives
// between signings and only ever increases.
type roster struct {
	Serial uint64          `json:"serial"`
	Nodes  []nodelist.Node `json:"nodes"`
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "nodelist: "+format+"\n", a...)
	os.Exit(1)
}

func cmdInit() {
	if _, err := os.Stat(keyFile); err == nil {
		die("%s already exists — refusing to overwrite it. A new key re-keys the whole network.", keyFile)
	}
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		die("generating key: %v", err)
	}
	seed := priv.Seed()
	if err := os.WriteFile(keyFile, []byte(base64.RawURLEncoding.EncodeToString(seed)+"\n"), 0o600); err != nil {
		die("writing %s: %v", keyFile, err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	fmt.Printf("wrote %s (keep it secret)\n", keyFile)
	fmt.Printf("coordinator public key (give this to every host):\n  %s\n", base64.RawURLEncoding.EncodeToString(pub))
}

func cmdPubkey() {
	pub := loadKey().Public().(ed25519.PublicKey)
	fmt.Println(base64.RawURLEncoding.EncodeToString(pub))
}

func cmdAdd(args []string) {
	if len(args) != 2 && len(args) != 3 {
		die("usage: add <domain> <pubkey> [alias]")
	}
	domain := strings.ToLower(strings.TrimSpace(args[0]))
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(args[1]), "="))
	if err != nil || len(key) != ed25519.PublicKeySize {
		die("%q is not a valid host public key (expected base64url of %d bytes)", args[1], ed25519.PublicKeySize)
	}
	alias := ""
	if len(args) == 3 {
		alias = strings.ToLower(strings.TrimSpace(args[2]))
	}
	r := loadRoster()
	for i, n := range r.Nodes {
		if strings.EqualFold(n.Domain, domain) {
			// Re-adding is how a host's key is ROTATED — replace in place rather
			// than refuse, or a rotation would need a remove-then-add that leaves
			// the host absent from any list signed in between. An alias given here
			// updates it; omitting it keeps whatever the host already had.
			r.Nodes[i].PublicKey = key
			if len(args) == 3 {
				r.Nodes[i].Alias = alias
			}
			saveRoster(r)
			fmt.Printf("updated %s (key rotated)\n", domain)
			return
		}
	}
	r.Nodes = append(r.Nodes, nodelist.Node{Domain: domain, PublicKey: key, Alias: alias})
	saveRoster(r)
	if alias != "" {
		fmt.Printf("added %s (alias %s)\n", domain, alias)
	} else {
		fmt.Printf("added %s\n", domain)
	}
}

func cmdRemove(args []string) {
	if len(args) != 1 {
		die("usage: remove <domain>")
	}
	domain := strings.ToLower(strings.TrimSpace(args[0]))
	r := loadRoster()
	kept := r.Nodes[:0]
	found := false
	for _, n := range r.Nodes {
		if strings.EqualFold(n.Domain, domain) {
			found = true
			continue
		}
		kept = append(kept, n)
	}
	if !found {
		die("%s is not in the roster", domain)
	}
	r.Nodes = kept
	saveRoster(r)
	fmt.Printf("removed %s — it drops out of the network on the next signed list\n", domain)
}

func cmdSign(args []string) {
	days := 30
	for i := 0; i < len(args); i++ {
		if args[i] == "--days" && i+1 < len(args) {
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				die("--days must be a positive number")
			}
			days = n
			i++
		}
	}
	key := loadKey()
	r := loadRoster()
	// Serial advances on every signing, so a host can reject an older list
	// even if it is otherwise valid — the rollback defence. The tool is the
	// only writer, so a counter is enough; no coordination is needed.
	r.Serial++
	saveRoster(r)

	now := time.Now().UTC()
	list := nodelist.List{
		Serial:  r.Serial,
		Issued:  now,
		Expires: now.AddDate(0, 0, days),
		Nodes:   r.Nodes,
	}
	signed, err := nodelist.Sign(list, key)
	if err != nil {
		die("signing: %v", err)
	}
	fmt.Println(signed)
	fmt.Fprintf(os.Stderr, "signed serial %d, %d node(s), valid %d days\n", r.Serial, len(r.Nodes), days)
}

func loadKey() ed25519.PrivateKey {
	b, err := os.ReadFile(keyFile)
	if err != nil {
		die("reading %s: %v (run `init` first)", keyFile, err)
	}
	seed, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(seed) != ed25519.SeedSize {
		die("%s is corrupt", keyFile)
	}
	return ed25519.NewKeyFromSeed(seed)
}

func loadRoster() roster {
	b, err := os.ReadFile(rosterFile)
	if os.IsNotExist(err) {
		return roster{}
	}
	if err != nil {
		die("reading %s: %v", rosterFile, err)
	}
	var r roster
	if err := json.Unmarshal(b, &r); err != nil {
		die("%s is not valid JSON: %v", rosterFile, err)
	}
	return r
}

func saveRoster(r roster) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		die("encoding roster: %v", err)
	}
	if err := os.WriteFile(rosterFile, append(b, '\n'), 0o644); err != nil {
		die("writing %s: %v", rosterFile, err)
	}
}
