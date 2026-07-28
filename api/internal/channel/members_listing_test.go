package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/store"
)

// Listing a channel's members, and what that costs.
//
// The list carries each member's email, which the channel's administrator needs to tell one
// subscriber from another. Scoping it correctly matters twice over: the emails must belong to the
// members being listed, and only those members should have to be read to produce them.
//
// The second part was wrong. Decorating a page of at most two hundred members read EVERY user on
// the server, so the cost of an administrator refreshing one channel's member list grew with the
// size of the whole user base.

// countingUserStore records how many users each lookup asked for.
type countingUserStore struct {
	store.Store
	mu           sync.Mutex
	listUsersAll int   // calls to the unbounded ListUsers
	byIDsSizes   []int // how many ids each scoped lookup asked for
}

func (c *countingUserStore) ListUsers(ctx context.Context) ([]domain.User, error) {
	c.mu.Lock()
	c.listUsersAll++
	c.mu.Unlock()
	return c.Store.ListUsers(ctx)
}

func (c *countingUserStore) UsersByIDs(ctx context.Context, ids []string) (map[string]domain.User, error) {
	c.mu.Lock()
	c.byIDsSizes = append(c.byIDsSizes, len(ids))
	c.mu.Unlock()
	return c.Store.UsersByIDs(ctx, ids)
}

func (c *countingUserStore) report() (int, []int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listUsersAll, append([]int(nil), c.byIDsSizes...)
}

// memberListing decodes the handler's response.
type memberListing struct {
	Members []struct {
		UserID   string `json:"userId"`
		Username string `json:"username"`
		Role     string `json:"role"`
		Status   string `json:"status"`
	} `json:"members"`
	Total int `json:"total"`
}

func joinChannel(t *testing.T, f *appFixture, channelID, userID string, role domain.Role) {
	t.Helper()
	if _, err := f.store.UpsertMember(context.Background(), domain.ChannelMember{
		ChannelID: channelID, UserID: userID, Role: role,
		Status: domain.MemberActive, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add member: %v", err)
	}
}

// THE ONE THAT MATTERS FOR COST. Listing members reads the members, not the world.
func TestListingMembersReadsOnlyThoseMembers(t *testing.T) {
	f := newAppFixture(t)
	ownerToken, owner := f.tokenFor(t, "members-owner@pheme.test")
	ch := channelFor(t, f, owner.ID, "members")

	// Three members in the channel, and a crowd of people who are not.
	for i := 0; i < 3; i++ {
		u := seedUser(t, f.store, fmt.Sprintf("member-%d@pheme.test", i), domain.RoleUser)
		joinChannel(t, f, ch.ID, u.ID, domain.RoleUser)
	}
	for i := 0; i < 40; i++ {
		seedUser(t, f.store, fmt.Sprintf("bystander-%d@pheme.test", i), domain.RoleUser)
	}

	counting := &countingUserStore{Store: f.store}
	f.h.Store = counting

	rec := f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/members", ownerToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list members = %d: %s", rec.Code, rec.Body)
	}

	all, sizes := counting.report()
	if all > 0 {
		t.Errorf("listing members called ListUsers %d times; that reads every user on the server "+
			"to decorate one page, so this request gets slower as the product grows", all)
	}
	if len(sizes) == 0 {
		t.Fatal("no scoped user lookup was made; where did the profiles come from?")
	}
	for _, n := range sizes {
		if n > 3 {
			t.Errorf("a lookup asked for %d users to decorate 3 members", n)
		}
	}
}

// The profiles belong to the right people, and no email appears at all.
//
// Two separate promises. A lookup keyed wrongly would label a member with somebody else's name,
// which is worse than showing none. And the address a subscriber signed up with is not part of what
// a channel's owner is shown — an owner is an ordinary user, and nobody agreed to hand over their
// email by pressing Subscribe.
func TestMemberProfilesBelongToTheirMembersAndCarryNoEmail(t *testing.T) {
	f := newAppFixture(t)
	ownerToken, owner := f.tokenFor(t, "email-owner@pheme.test")
	ch := channelFor(t, f, owner.ID, "emails")

	want := map[string]string{}
	emails := []string{}
	for i := 0; i < 4; i++ {
		email := fmt.Sprintf("real-member-%d@pheme.test", i)
		u := seedUser(t, f.store, email, domain.RoleUser)
		username := fmt.Sprintf("member%d", i)
		if _, err := f.store.UpdateUserProfile(context.Background(), u.ID, domain.UserProfileUpdate{Username: &username}); err != nil {
			t.Fatalf("seed username: %v", err)
		}
		joinChannel(t, f, ch.ID, u.ID, domain.RoleUser)
		want[u.ID] = username
		emails = append(emails, email)
	}
	// Somebody who is not in the channel, whose address must not appear.
	outsider := seedUser(t, f.store, "not-a-member@pheme.test", domain.RoleUser)

	rec := f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/members", ownerToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list members = %d", rec.Code)
	}
	var got memberListing
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, m := range got.Members {
		if m.UserID == owner.ID {
			continue // the owner's own row
		}
		if want[m.UserID] == "" {
			t.Errorf("a listed member (%s) is not one of the channel's members", m.UserID)
			continue
		}
		if m.Username != want[m.UserID] {
			t.Errorf("member %s is listed as %q, want %q", m.UserID, m.Username, want[m.UserID])
		}
	}
	body := rec.Body.String()
	if strings.Contains(body, "not-a-member@pheme.test") {
		t.Errorf("a non-member's email address appeared in the listing: %s", body)
	}
	// Nor any MEMBER's address. This is the promise that regressed the whole point of the change.
	for _, email := range emails {
		if strings.Contains(body, email) {
			t.Errorf("a member's email address appeared in the listing: %s", body)
		}
	}
	_ = outsider
}

// Only someone who administers the channel may see the list — it carries email addresses.
func TestListingMembersIsForAdministratorsOnly(t *testing.T) {
	f := newAppFixture(t)
	_, owner := f.tokenFor(t, "guard-owner@pheme.test")
	ch := channelFor(t, f, owner.ID, "guarded-members")

	member := seedUser(t, f.store, "plain-member@pheme.test", domain.RoleUser)
	joinChannel(t, f, ch.ID, member.ID, domain.RoleUser)
	memberToken, _, _, err := f.tokens.Issue(member.ID, string(domain.RoleUser))
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	rec := f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/members", memberToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("an ordinary member listed the channel's members = %d, want 403; the listing "+
			"carries every member's email address", rec.Code)
	}

	// A channel admin may.
	admin := seedUser(t, f.store, "channel-admin@pheme.test", domain.RoleUser)
	joinChannel(t, f, ch.ID, admin.ID, domain.RoleAdmin)
	adminToken, _, _, err := f.tokens.Issue(admin.ID, string(domain.RoleUser))
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	if rec := f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/members", adminToken, nil); rec.Code != http.StatusOK {
		t.Errorf("a channel admin listing members = %d, want 200", rec.Code)
	}
}

// A member list that cannot load emails is still a member list. Losing a decoration must not cost
// the administrator the page.
func TestMemberListingSurvivesAProfileLookupFailure(t *testing.T) {
	f := newAppFixture(t)
	ownerToken, owner := f.tokenFor(t, "degrade-owner@pheme.test")
	ch := channelFor(t, f, owner.ID, "degrade")
	u := seedUser(t, f.store, "degraded-member@pheme.test", domain.RoleUser)
	joinChannel(t, f, ch.ID, u.ID, domain.RoleUser)

	f.h.Store = &failingUsersStore{Store: f.store}

	rec := f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/members", ownerToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list members with a failing user lookup = %d, want the list anyway", rec.Code)
	}
	var got memberListing
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Members) == 0 {
		t.Error("the member list came back empty because the emails could not be loaded")
	}
}

type failingUsersStore struct {
	store.Store
}

func (f *failingUsersStore) UsersByIDs(context.Context, []string) (map[string]domain.User, error) {
	return nil, fmt.Errorf("user store unavailable")
}

// The admin channel list has the same shape of problem: it attaches each channel's owner email.
// That read every user on the server too, and — unlike the member list — failed the whole request
// if the read failed, so a hiccup looking up users cost an administrator the channel list itself.
func TestAdminChannelListReadsOnlyTheOwnersOnThePage(t *testing.T) {
	db := store.NewMemory(nil)
	admin := seedUser(t, db, "chan-list-admin@pheme.test", domain.RoleAdmin)

	owner := seedUser(t, db, "chan-owner@pheme.test", domain.RoleUser)
	if _, err := db.CreateChannel(context.Background(), domain.Channel{
		PublicID: "pub-listed", OwnerID: owner.ID, Name: "listed",
		Status: domain.ChannelActive, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	for i := 0; i < 40; i++ {
		seedUser(t, db, fmt.Sprintf("chan-bystander-%d@pheme.test", i), domain.RoleUser)
	}

	counting := &countingUserStore{Store: db}
	mux := http.NewServeMux()
	(&AdminHandler{Store: counting}).Register(mux)

	rec := adminReq(mux, http.MethodGet, "/v1/admin/channels", admin.ID, "admin", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list channels = %d: %s", rec.Code, rec.Body)
	}

	all, sizes := counting.report()
	if all > 0 {
		t.Errorf("the admin channel list called ListUsers %d times; that reads every user on the "+
			"server to attach one owner address per channel", all)
	}
	for _, n := range sizes {
		if n > 1 {
			t.Errorf("a lookup asked for %d users for a page holding 1 channel", n)
		}
	}

	// The owner's address is still attached.
	if !strings.Contains(rec.Body.String(), "chan-owner@pheme.test") {
		t.Errorf("the owner's email is missing from the listing: %s", rec.Body)
	}
	// And nobody else's.
	if strings.Contains(rec.Body.String(), "chan-bystander-0@pheme.test") {
		t.Errorf("an unrelated user's email appeared in the channel listing: %s", rec.Body)
	}
}

// A failed owner lookup costs the decoration, not the page.
func TestAdminChannelListSurvivesAnOwnerLookupFailure(t *testing.T) {
	db := store.NewMemory(nil)
	admin := seedUser(t, db, "chan-degraded-admin@pheme.test", domain.RoleAdmin)
	owner := seedUser(t, db, "chan-degraded-owner@pheme.test", domain.RoleUser)
	if _, err := db.CreateChannel(context.Background(), domain.Channel{
		PublicID: "pub-degraded", OwnerID: owner.ID, Name: "degraded",
		Status: domain.ChannelActive, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	mux := http.NewServeMux()
	(&AdminHandler{Store: &failingUsersStore{Store: db}}).Register(mux)

	rec := adminReq(mux, http.MethodGet, "/v1/admin/channels", admin.ID, "admin", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list channels with a failing user lookup = %d, want the list anyway: %s",
			rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "degraded") {
		t.Errorf("the channel is missing from the listing: %s", rec.Body)
	}
}
