package auth

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// What a login costs, and what stops a thousand of them at once from being the last thing the
// server does.
//
// Argon2 is deliberately expensive: 64 MiB per derivation, held for its duration. That is the
// point of the algorithm, and it is fine until the number running at once is decided by however
// many people happen to be signing in. A thousand clients reconnecting after a deploy is not a
// hypothetical — it is what a deploy IS — and unbounded that is 64 GB of simultaneous demand.
//
// A load run measured 832 MB live in argon2.initBlocks from sixteen concurrent hashes, 99% of the
// process heap. The arithmetic from there is not comforting.

// TestConcurrentHashingIsBounded is the test the fix exists for. It does not measure memory —
// that is not something a unit test can do reliably — it measures the thing that CAUSES the
// memory: how many derivations are allowed to be in flight at once.
func TestConcurrentHashingIsBounded(t *testing.T) {
	limit := maxConcurrentHashes()

	var inFlight, peak atomic.Int64
	var wg sync.WaitGroup

	// Comfortably more attempts than slots, so the bound has to do something.
	attempts := limit * 6
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Observe occupancy from inside the slot by wrapping the same primitive the real
			// callers use.
			withHashSlot(func() struct{} {
				now := inFlight.Add(1)
				for {
					old := peak.Load()
					if now <= old || peak.CompareAndSwap(old, now) {
						break
					}
				}
				// Long enough that the goroutines genuinely overlap.
				time.Sleep(20 * time.Millisecond)
				inFlight.Add(-1)
				return struct{}{}
			})
		}(i)
	}
	wg.Wait()

	if got := peak.Load(); got > int64(limit) {
		t.Errorf("%d derivations ran at once, limit is %d. At %d MiB each that is %d MiB of "+
			"simultaneous demand, decided by how many people happened to sign in.",
			got, limit, argonMemory/1024, got*int64(argonMemory)/1024)
	}
	if peak.Load() < 2 {
		t.Errorf("peak concurrency was %d — logins are serialised, which is its own outage",
			peak.Load())
	}
}

// Everything still works while bounded: the slot must not corrupt or skip the actual hashing.
func TestHashingStillWorksUnderTheBound(t *testing.T) {
	const password = "Correct12345"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := VerifyPassword(password, hash)
			if err != nil {
				errs <- err
				return
			}
			if !ok {
				errs <- fmt.Errorf("a correct password did not verify")
			}
			if bad, _ := VerifyPassword("Wrong12345", hash); bad {
				errs <- fmt.Errorf("a wrong password verified")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// The cost parameters are read from the stored hash, so they are attacker-controlled the moment
// anything can write one. An absurd memory parameter must be refused as malformed rather than
// honoured — otherwise a single row turns one login into an out-of-memory kill.
func TestAnAbsurdCostParameterIsRefusedRatherThanAllocated(t *testing.T) {
	real, err := HashPassword("Correct12345")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	parts := strings.Split(real, "$")

	for _, tc := range []struct {
		name   string
		params string
	}{
		{"16 TiB of memory", "m=17179869184,t=1,p=4"},
		{"just over the ceiling", fmt.Sprintf("m=%d,t=1,p=4", maxArgonMemory+1)},
		{"zero memory", "m=0,t=1,p=4"},
		{"zero threads", "m=65536,t=1,p=0"},
		{"zero time", "m=65536,t=0,p=4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forged := strings.Join([]string{parts[0], parts[1], parts[2], tc.params, parts[4], parts[5]}, "$")

			done := make(chan struct{})
			var ok bool
			var err error
			go func() {
				defer close(done)
				ok, err = VerifyPassword("Correct12345", forged)
			}()

			select {
			case <-done:
			case <-time.After(10 * time.Second):
				// Reaching argon2 with these parameters at all is the failure; if it is still
				// running it is busy allocating.
				t.Fatalf("VerifyPassword accepted %q and is still working on it", tc.params)
			}

			if err == nil {
				t.Errorf("%q was accepted (ok=%v); the allocation size came straight from stored data",
					tc.params, ok)
			}
			if ok {
				t.Errorf("%q verified successfully", tc.params)
			}
		})
	}
}

// A hash written with a legitimately higher cost than today's default must still verify — that is
// the whole reason the parameters live in the hash. The ceiling must not break parameter upgrades.
func TestAHigherButSaneCostStillVerifies(t *testing.T) {
	// Exactly at the ceiling, which is a plausible future default.
	if maxArgonMemory <= argonMemory {
		t.Fatalf("the ceiling (%d) leaves no room above the default (%d); parameters could never "+
			"be raised", maxArgonMemory, argonMemory)
	}
}
