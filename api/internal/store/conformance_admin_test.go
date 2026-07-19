package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// The admin listings, run against BOTH stores.
//
// These look like plumbing and are the one place an operator sees the whole system, so the failure
// mode is not an error — it is a wrong picture. A total that disagrees with the rows returned means
// paging that never ends or ends early; a search that matches nothing means an operator concludes
// an account does not exist when it does; and a listing that quietly drops the last page means the
// oldest records become unreachable through the only UI that can reach them.
//
// The paging arithmetic in particular is worth pinning across both implementations, because it is
// written twice — a slice expression in one and a skip/limit in the other — and off-by-one is the
// most likely way for them to disagree.

func seedUsers(t *testing.T, s Store, n int, prefix string) []domain.User {
	t.Helper()
	out := make([]domain.User, 0, n)
	for i := 0; i < n; i++ {
		u, err := s.CreateUser(context.Background(), domain.User{
			Email: fmt.Sprintf("%s-%02d@pheme.test", prefix, i),
			Role:  domain.RoleUser, Status: domain.UserActive, CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("create user %d: %v", i, err)
		}
		out = append(out, u)
		time.Sleep(time.Millisecond) // distinct timestamps, so ordering is not a coin toss
	}
	return out
}

func TestConformance_AdminUserListingPagesWithoutLosingRows(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		const created = 7
		seedUsers(t, s.store, created, "pager")

		first, total, err := s.store.AdminListUsers(ctx, "pager", 0, 3)
		if err != nil {
			t.Fatalf("page 1: %v", err)
		}
		if total != created {
			t.Errorf("total = %d, want %d; the UI's page count is computed from this, so paging "+
				"either stops early or offers pages that are empty", total, created)
		}
		if len(first) != 3 {
			t.Fatalf("page 1 returned %d rows, want 3", len(first))
		}

		// Walk every page and check nothing is lost or repeated. Off-by-one in either direction is
		// invisible on one page and obvious across all of them.
		seen := map[string]bool{}
		for offset := 0; offset < created; offset += 3 {
			page, _, err := s.store.AdminListUsers(ctx, "pager", offset, 3)
			if err != nil {
				t.Fatalf("page at offset %d: %v", offset, err)
			}
			for _, u := range page {
				if seen[u.ID] {
					t.Errorf("user %s appeared on two pages; an operator sees duplicates and the "+
						"last page is unreachable", u.Email)
				}
				seen[u.ID] = true
			}
		}
		if len(seen) != created {
			t.Errorf("walking every page saw %d of %d users; some are unreachable through the only "+
				"UI that can reach them", len(seen), created)
		}

		// Past the end is empty, not an error and not a wrapped first page.
		beyond, _, err := s.store.AdminListUsers(ctx, "pager", created+10, 3)
		if err != nil {
			t.Fatalf("past the end: %v", err)
		}
		if len(beyond) != 0 {
			t.Errorf("an offset past the end returned %d rows", len(beyond))
		}
	})
}

// The search is what an operator actually uses; a support request starts with an email address.
func TestConformance_AdminUserSearch(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		seedUsers(t, s.store, 3, "findme")
		seedUsers(t, s.store, 3, "other")

		found, total, err := s.store.AdminListUsers(ctx, "findme", 0, 50)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if total != 3 || len(found) != 3 {
			t.Errorf("searching found %d rows (total %d), want 3", len(found), total)
		}
		for _, u := range found {
			if !strings.Contains(u.Email, "findme") {
				t.Errorf("search returned an unrelated account: %s", u.Email)
			}
		}

		// Case-insensitive: nobody types an address back exactly as it was stored.
		upper, _, err := s.store.AdminListUsers(ctx, "FINDME", 0, 50)
		if err != nil {
			t.Fatalf("uppercase search: %v", err)
		}
		if len(upper) != 3 {
			t.Errorf("an uppercase search found %d of 3; an operator concludes the account does "+
				"not exist", len(upper))
		}

		// A search matching nothing is empty, not everything — the dangerous direction, because it
		// looks like a working list.
		none, total, err := s.store.AdminListUsers(ctx, "no-such-account-anywhere", 0, 50)
		if err != nil {
			t.Fatalf("empty search: %v", err)
		}
		if len(none) != 0 || total != 0 {
			t.Errorf("a search matching nothing returned %d rows (total %d); a filter that silently "+
				"does nothing shows an operator the whole table and lets them act on the wrong row",
				len(none), total)
		}
	})
}

func TestConformance_AdminChannelListing(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		for i := 0; i < 5; i++ {
			if _, err := s.store.CreateChannel(ctx, domain.Channel{
				PublicID: fmt.Sprintf("pub-adm-%d", i), OwnerID: "owner",
				Name:   fmt.Sprintf("Findable Channel %d", i),
				Status: domain.ChannelActive, CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("create channel: %v", err)
			}
			time.Sleep(time.Millisecond)
		}
		if _, err := s.store.CreateChannel(ctx, domain.Channel{
			PublicID: "pub-other", OwnerID: "owner", Name: "Unrelated",
			Status: domain.ChannelActive, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("create channel: %v", err)
		}

		page, total, err := s.store.AdminListChannels(ctx, "Findable", 0, 2)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if total != 5 {
			t.Errorf("total = %d, want the 5 matching channels", total)
		}
		if len(page) != 2 {
			t.Errorf("a page of 2 returned %d rows", len(page))
		}
		for _, c := range page {
			if !strings.Contains(c.Name, "Findable") {
				t.Errorf("the filter returned an unrelated channel: %s", c.Name)
			}
		}

		// ListAllChannels is the unpaged read the dispatcher and admin tooling use; it must see
		// everything, including what the filtered listing excluded.
		all, err := s.store.ListAllChannels(ctx)
		if err != nil {
			t.Fatalf("list all: %v", err)
		}
		if len(all) < 6 {
			t.Errorf("ListAllChannels returned %d channels, want at least the 6 created", len(all))
		}
	})
}

func TestConformance_AdminCommentListing(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		ch := seedChannel(t, s.store, "admincomments")
		msg := seedMessage(t, s.store, ch.ID, "commented")

		for _, body := range []string{"first flagged remark", "ordinary remark", "second flagged remark"} {
			if _, err := s.store.CreateComment(ctx, domain.Comment{
				MessageID: msg.ID, UserID: "u1", Body: body, CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("create comment: %v", err)
			}
			time.Sleep(time.Millisecond)
		}

		all, total, err := s.store.AdminListComments(ctx, "", 0, 50)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if total != 3 || len(all) != 3 {
			t.Errorf("listing returned %d comments (total %d), want 3", len(all), total)
		}
		// Newest first, like every other listing here — an operator looking for what was just
		// posted must not have to page to the end.
		for i := 1; i < len(all); i++ {
			if all[i].CreatedAt.After(all[i-1].CreatedAt) {
				t.Errorf("comments are not newest-first at position %d", i)
				break
			}
		}

		flagged, total, err := s.store.AdminListComments(ctx, "flagged", 0, 50)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if total != 2 || len(flagged) != 2 {
			t.Errorf("searching for a substring returned %d (total %d), want the 2 matching",
				len(flagged), total)
		}
		for _, c := range flagged {
			if !strings.Contains(c.Body, "flagged") {
				t.Errorf("search returned an unrelated comment: %q", c.Body)
			}
		}
	})
}

// The dashboard numbers. Wrong ones are not an error anyone sees — they are a picture an operator
// makes decisions from.
func TestConformance_AdminStatsCountWhatExists(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()

		// topN/recentN bound the two embedded lists; the counts are unaffected by them.
		before, err := s.store.AdminStats(ctx, 5, 5)
		if err != nil {
			t.Fatalf("stats: %v", err)
		}

		seedUsers(t, s.store, 3, "counted")
		ch := seedChannel(t, s.store, "counted")
		seedMessage(t, s.store, ch.ID, "one")
		seedMessage(t, s.store, ch.ID, "two")

		after, err := s.store.AdminStats(ctx, 5, 5)
		if err != nil {
			t.Fatalf("stats: %v", err)
		}

		if got := after.Users - before.Users; got != 3 {
			t.Errorf("user count moved by %d after creating 3", got)
		}
		if got := after.Channels - before.Channels; got != 1 {
			t.Errorf("channel count moved by %d after creating 1", got)
		}
		if got := after.Messages - before.Messages; got != 2 {
			t.Errorf("message count moved by %d after creating 2", got)
		}

		// The embedded lists respect their bounds. An unbounded "recent messages" on a dashboard is
		// the whole message table rendered into a page.
		capped, err := s.store.AdminStats(ctx, 1, 1)
		if err != nil {
			t.Fatalf("stats: %v", err)
		}
		if len(capped.RecentMessages) > 1 {
			t.Errorf("asked for 1 recent message and got %d", len(capped.RecentMessages))
		}
		if len(capped.TopChannels) > 1 {
			t.Errorf("asked for 1 top channel and got %d", len(capped.TopChannels))
		}
	})
}
