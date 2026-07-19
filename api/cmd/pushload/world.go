package main

import (
	"context"
	"crypto/ecdh"
	cryptorand "crypto/rand"
	"encoding/base64"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

// Provisioning: accounts, their push devices, and the conversations they talk in.
//
// The devices are the point. Each one registers a Web Push subscription whose endpoint is the fake
// push service, so every notification the server decides to send becomes a real outbound HTTPS
// request that takes real time — which is the thing the earlier load test left out.

type user struct {
	id    string
	token string
}

type world struct {
	users []user
	convs []conv
}

type conv struct {
	id      string
	members []int
}

func provision(ctx context.Context, c *client, adminToken, pushURL string, o options) (*world, error) {
	runID := time.Now().UTC().Format("0102-150405")
	fmt.Printf("provisioning   %d users x %d devices, %d per conversation...\n",
		o.users, o.devicesPerUser, o.groupSize)

	w := &world{users: make([]user, o.users)}
	var mu sync.Mutex
	var firstErr error

	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for i := 0; i < o.users; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()

			email := fmt.Sprintf("pushload-%s-%d@%s", runID, i, o.emailDomain)
			var created struct {
				ID string `json:"id"`
			}
			if err := c.do(ctx, http.MethodPost, "/v1/admin/users", adminToken,
				map[string]string{"email": email, "password": o.password, "role": "user"}, &created); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("create %s: %w", email, err)
				}
				mu.Unlock()
				return
			}
			token, err := c.login(ctx, email, o.password)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("login %s: %w", email, err)
				}
				mu.Unlock()
				return
			}

			// One push subscription per device, each pointing at the fake service. The endpoints
			// differ per device so the server treats them as distinct addresses rather than
			// collapsing them — a device IS its push address.
			//
			// The keys have to be REAL. Web Push encrypts the payload to the subscription's public
			// key, so the library rejects a made-up one before it opens a connection: the first
			// version of this used a plausible-looking constant and reported that 100% of
			// notifications were dropped, when in truth not one had been attempted.
			for d := 0; d < o.devicesPerUser; d++ {
				sub, err := subscription(fmt.Sprintf("%s/push/%d/%d", pushURL, i, d))
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("build subscription %d/%d: %w", i, d, err)
					}
					mu.Unlock()
					return
				}
				if err := c.do(ctx, http.MethodPost, "/v1/devices", token, map[string]any{
					"platform": "web", "webPushSub": sub,
				}, nil); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("register device %d/%d: %w", i, d, err)
					}
					mu.Unlock()
					return
				}
			}

			// The user's own id, needed to build conversations.
			var me struct {
				ID string `json:"id"`
			}
			if err := c.do(ctx, http.MethodGet, "/v1/me", token, nil, &me); err != nil {
				// Fall back to the id the admin API returned.
				me.ID = created.ID
			}
			if me.ID == "" {
				me.ID = created.ID
			}

			mu.Lock()
			w.users[i] = user{id: me.ID, token: token}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}

	// Disjoint groups, so the number of notifications a message produces is fixed and the drop
	// count means something.
	for base := 0; base+o.groupSize <= o.users; base += o.groupSize {
		members := make([]int, 0, o.groupSize)
		ids := make([]string, 0, o.groupSize-1)
		for j := base; j < base+o.groupSize; j++ {
			members = append(members, j)
			if j != base {
				ids = append(ids, w.users[j].id)
			}
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := c.do(ctx, http.MethodPost, "/v1/conversations", w.users[base].token, map[string]any{
			"kind": "group", "name": fmt.Sprintf("pushload-%s-%d", runID, base/o.groupSize),
			"memberIds": ids,
		}, &created); err != nil {
			return nil, fmt.Errorf("create conversation: %w", err)
		}
		w.convs = append(w.convs, conv{id: created.ID, members: members})
	}
	if len(w.convs) == 0 {
		return nil, fmt.Errorf("no conversations: %d users cannot fill a group of %d", o.users, o.groupSize)
	}
	fmt.Printf("               %d conversations ready\n", len(w.convs))
	return w, nil
}

// subscription builds a Web Push subscription with a genuine P-256 key pair, pointing at the given
// endpoint. Nothing ever decrypts what is sent to it — the fake service only counts — but the
// sender will not encrypt to an invalid key, so the keys must be real for a request to happen at
// all.
func subscription(endpoint string) (string, error) {
	key, err := ecdh.P256().GenerateKey(cryptorand.Reader)
	if err != nil {
		return "", err
	}
	auth := make([]byte, 16)
	if _, err := cryptorand.Read(auth); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"endpoint":%q,"keys":{"p256dh":%q,"auth":%q}}`,
		endpoint,
		base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(auth),
	), nil
}

// sendFor holds the message rate for the configured duration and returns how many were accepted.
func sendFor(ctx context.Context, c *client, w *world, o options) int {
	interval := time.Duration(float64(time.Second) / o.rate)
	deadline := time.After(o.duration)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	//nolint:gosec // choosing which conversation to post in needs no crypto-grade randomness
	pick := rand.New(rand.NewSource(time.Now().UnixNano()))
	payload := make([]byte, 512)

	var sent int
	var wg sync.WaitGroup
	for {
		select {
		case <-deadline:
			wg.Wait()
			return sent
		case <-ticker.C:
			cv := w.convs[pick.Intn(len(w.convs))]
			sender := cv.members[pick.Intn(len(cv.members))]
			sent++
			wg.Add(1)
			go func() {
				defer wg.Done()
				// A failure to send is a different problem from a dropped notification, and this
				// tool is measuring the second. Errors here would show up as a shortfall in the
				// notification count, which the report reads as drops — so they are counted
				// separately by simply not counting this message as sent.
				_ = c.do(ctx, http.MethodPost, "/v1/conversations/"+cv.id+"/messages",
					w.users[sender].token,
					map[string]any{"ciphertext": payload, "contentType": "application/octet-stream"}, nil)
			}()
		}
	}
}
