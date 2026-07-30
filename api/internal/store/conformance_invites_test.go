package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Invites, run against BOTH stores.
//
// The single-use guarantee lives in the store and nowhere else — the handler only asks. It is
// written twice, as a map under a mutex in one implementation and a conditional update in the
// other, and "at most one caller wins" is exactly the kind of property those two can disagree
// about while both look correct in isolation.

func seedInvite(t *testing.T, s Store, mutate func(*domain.Invite)) domain.Invite {
	t.Helper()
	inv := domain.Invite{
		CodeHash:  fmt.Sprintf("hash-%d", time.Now().UnixNano()),
		Prefix:    "abc123",
		CreatedBy: "admin",
		CreatedAt: time.Now().UTC(),
	}
	if mutate != nil {
		mutate(&inv)
	}
	created, err := s.CreateInvite(context.Background(), inv)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	return created
}

func TestConformanceInviteLookup(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		inv := seedInvite(t, s.store, nil)

		got, err := s.store.InviteByCodeHash(ctx, inv.CodeHash)
		if err != nil {
			t.Fatalf("by code hash: %v", err)
		}
		if got.ID != inv.ID {
			t.Fatalf("got %q, want %q", got.ID, inv.ID)
		}

		if _, err := s.store.InviteByCodeHash(ctx, "nothing-hashes-to-this"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown hash err = %v, want ErrNotFound", err)
		}
		// An empty hash must never match: it is what a missing code arrives as, and matching
		// any row would admit anyone who left the field blank.
		if _, err := s.store.InviteByCodeHash(ctx, ""); !errors.Is(err, ErrNotFound) {
			t.Fatalf("empty hash err = %v, want ErrNotFound", err)
		}
		if _, err := s.store.InviteByID(ctx, "no-such-id"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown id err = %v, want ErrNotFound", err)
		}
	})
}

func TestConformanceConsumeInviteIsSingleUse(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		now := time.Now().UTC()
		inv := seedInvite(t, s.store, nil)

		if err := s.store.ConsumeInvite(ctx, inv.ID, "user-1", now); err != nil {
			t.Fatalf("first consume: %v", err)
		}
		if err := s.store.ConsumeInvite(ctx, inv.ID, "user-2", now); !errors.Is(err, ErrInviteSpent) {
			t.Fatalf("second consume err = %v, want ErrInviteSpent", err)
		}

		after, err := s.store.InviteByID(ctx, inv.ID)
		if err != nil {
			t.Fatalf("by id: %v", err)
		}
		if after.UsedBy != "user-1" || after.UsedAt == nil {
			t.Fatalf("got usedBy=%q usedAt=%v, want the first caller recorded", after.UsedBy, after.UsedAt)
		}
		if after.Status(now) != domain.InviteUsed {
			t.Fatalf("status = %q, want used", after.Status(now))
		}

		if err := s.store.ConsumeInvite(ctx, "no-such-id", "user-3", now); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown id err = %v, want ErrNotFound", err)
		}
	})
}

// The race the whole design exists to lose safely: many callers redeeming one invitation at
// the same instant, exactly one of whom may win.
func TestConformanceConcurrentConsumeAdmitsOne(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		now := time.Now().UTC()
		inv := seedInvite(t, s.store, nil)

		const racers = 8
		var wg sync.WaitGroup
		var mu sync.Mutex
		won := 0
		start := make(chan struct{})
		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				if err := s.store.ConsumeInvite(ctx, inv.ID, fmt.Sprintf("user-%d", i), now); err == nil {
					mu.Lock()
					won++
					mu.Unlock()
				}
			}(i)
		}
		close(start)
		wg.Wait()

		if won != 1 {
			t.Fatalf("%d of %d callers consumed one invite, want exactly 1", won, racers)
		}
	})
}

func TestConformanceConsumeRefusesRevokedAndExpired(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		now := time.Now().UTC()

		revoked := seedInvite(t, s.store, nil)
		if err := s.store.RevokeInvite(ctx, revoked.ID, now); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		// Revoking twice is a no-op, not an error: the caller asked for a state it is in.
		if err := s.store.RevokeInvite(ctx, revoked.ID, now); err != nil {
			t.Fatalf("second revoke: %v", err)
		}
		if err := s.store.ConsumeInvite(ctx, revoked.ID, "user", now); !errors.Is(err, ErrInviteSpent) {
			t.Fatalf("consume revoked err = %v, want ErrInviteSpent", err)
		}

		past := now.Add(-time.Hour)
		expired := seedInvite(t, s.store, func(i *domain.Invite) { i.ExpiresAt = &past })
		if err := s.store.ConsumeInvite(ctx, expired.ID, "user", now); !errors.Is(err, ErrInviteSpent) {
			t.Fatalf("consume expired err = %v, want ErrInviteSpent", err)
		}

		future := now.Add(time.Hour)
		live := seedInvite(t, s.store, func(i *domain.Invite) { i.ExpiresAt = &future })
		if err := s.store.ConsumeInvite(ctx, live.ID, "user", now); err != nil {
			t.Fatalf("consume unexpired: %v", err)
		}

		if err := s.store.RevokeInvite(ctx, "no-such-id", now); !errors.Is(err, ErrNotFound) {
			t.Fatalf("revoke unknown err = %v, want ErrNotFound", err)
		}
	})
}

// Release exists for one case — the account could not be created after the invite was spent —
// and it must genuinely restore redeemability, or the invitee is locked out anyway.
func TestConformanceReleaseInviteRestoresIt(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		now := time.Now().UTC()
		inv := seedInvite(t, s.store, nil)

		if err := s.store.ConsumeInvite(ctx, inv.ID, "user-1", now); err != nil {
			t.Fatalf("consume: %v", err)
		}
		if err := s.store.ReleaseInvite(ctx, inv.ID); err != nil {
			t.Fatalf("release: %v", err)
		}

		after, err := s.store.InviteByID(ctx, inv.ID)
		if err != nil {
			t.Fatalf("by id: %v", err)
		}
		if after.UsedAt != nil || after.UsedBy != "" {
			t.Fatalf("got usedBy=%q usedAt=%v, want both cleared", after.UsedBy, after.UsedAt)
		}
		if err := s.store.ConsumeInvite(ctx, inv.ID, "user-2", now); err != nil {
			t.Fatalf("consume after release: %v", err)
		}

		if err := s.store.ReleaseInvite(ctx, "no-such-id"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("release unknown err = %v, want ErrNotFound", err)
		}
	})
}

func TestConformanceAdminListInvites(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		for i := 0; i < 5; i++ {
			seedInvite(t, s.store, func(inv *domain.Invite) {
				inv.CodeHash = fmt.Sprintf("hash-%d-%d", time.Now().UnixNano(), i)
				inv.Note = fmt.Sprintf("guest %d", i)
			})
			time.Sleep(time.Millisecond) // distinct timestamps, so newest-first is not a coin toss
		}
		seedInvite(t, s.store, func(inv *domain.Invite) {
			inv.CodeHash = "hash-needle"
			inv.Note = "Wilhelmina"
		})

		page, total, err := s.store.AdminListInvites(ctx, "", 0, 2)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if total != 6 {
			t.Fatalf("total = %d, want 6", total)
		}
		if len(page) != 2 {
			t.Fatalf("page size = %d, want 2", len(page))
		}
		if !page[0].CreatedAt.After(page[1].CreatedAt) {
			t.Fatal("listing is not newest-first")
		}

		found, total, err := s.store.AdminListInvites(ctx, "wilhelm", 0, 20)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if total != 1 || len(found) != 1 || found[0].Note != "Wilhelmina" {
			t.Fatalf("search by note returned %d rows (total %d), want the one", len(found), total)
		}
	})
}
