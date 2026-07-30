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

// The encrypted-group surface, run against BOTH store implementations.
//
// This is the part of the store where being wrong is unrecoverable rather than inconvenient. A
// KeyPackage handed out twice costs the forward secrecy it exists to provide. A compare-and-set
// that lets two commits through forks the group, and a forked group cannot be rejoined — every
// member ends up holding a history the others cannot read.
//
// All of it was exercised through the in-memory store only, while production runs Mongo. The
// compare-and-set is the one that worries me most: it is implemented separately in each, and "two
// writers, one winner" is exactly the kind of thing that works in a map guarded by a mutex and
// needs real care in a database.

func mustConversation(t *testing.T, s Store, ownerID string) domain.Conversation {
	t.Helper()
	conv, err := s.CreateConversation(context.Background(),
		domain.Conversation{
			Kind: domain.ConversationGroup, Title: "MLS", CreatedBy: ownerID,
			CreatedAt: time.Now().UTC(),
		},
		[]domain.ConversationMember{{UserID: ownerID, Role: domain.RoleAdmin, JoinedAt: time.Now().UTC()}},
	)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	return conv
}

// A single-use KeyPackage is CONSUMED by a claim. Handing the same one out twice means two devices
// join on one init key, which is precisely the forward secrecy these are for.
func TestConformance_SingleUseKeyPackagesAreConsumed(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()

		if err := s.store.AddKeyPackages(ctx, []domain.MLSKeyPackage{
			{UserID: "u1", DeviceID: "d1", KeyPackage: []byte("kp-1")},
			{UserID: "u1", DeviceID: "d1", KeyPackage: []byte("kp-2")},
		}); err != nil {
			t.Fatalf("add: %v", err)
		}

		seen := map[string]bool{}
		for i := 0; i < 2; i++ {
			kp, err := s.store.ClaimKeyPackage(ctx, "u1", "d1")
			if err != nil {
				t.Fatalf("claim %d: %v", i, err)
			}
			if seen[string(kp.KeyPackage)] {
				t.Fatalf("the same KeyPackage %q was handed out twice", kp.KeyPackage)
			}
			seen[string(kp.KeyPackage)] = true
		}

		// Drained, with no last-resort package published: the device is unreachable and the caller
		// must be told so rather than handed a reused key.
		if _, err := s.store.ClaimKeyPackage(ctx, "u1", "d1"); !errors.Is(err, ErrNotFound) {
			t.Errorf("claiming from an exhausted device = %v, want ErrNotFound", err)
		}
	})
}

// The last-resort package is REUSED rather than consumed, which is what stops a drained device
// becoming permanently unreachable.
func TestConformance_LastResortKeyPackageSurvivesBeingClaimed(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		if err := s.store.AddKeyPackages(ctx, []domain.MLSKeyPackage{
			{UserID: "u1", DeviceID: "d1", KeyPackage: []byte("last"), LastResort: true},
		}); err != nil {
			t.Fatalf("add: %v", err)
		}

		for i := 0; i < 3; i++ {
			kp, err := s.store.ClaimKeyPackage(ctx, "u1", "d1")
			if err != nil {
				t.Fatalf("claim %d: %v", i, err)
			}
			if string(kp.KeyPackage) != "last" {
				t.Fatalf("claim %d returned %q, want the last-resort package", i, kp.KeyPackage)
			}
		}

		has, err := s.store.HasLastResortKeyPackage(ctx, "u1", "d1")
		if err != nil || !has {
			t.Errorf("HasLastResortKeyPackage = %v, %v after three claims", has, err)
		}
	})
}

// One last-resort package per device, however many times it is published.
//
// The count EXCLUDES it, deliberately: it is never consumed, so counting it would tell a client it
// has stock it does not have and it would stop replenishing. So uniqueness is asserted the way the
// contract actually exposes it — the count reflects single-use packages alone, and the last-resort
// is present.
func TestConformance_OnlyOneLastResortPerDevice(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		for i := 0; i < 3; i++ {
			if err := s.store.AddKeyPackages(ctx, []domain.MLSKeyPackage{
				{UserID: "u1", DeviceID: "d1", KeyPackage: []byte("last"), LastResort: true},
			}); err != nil {
				t.Fatalf("add %d: %v", i, err)
			}
		}

		n, err := s.store.CountKeyPackages(ctx, "u1", "d1")
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 0 {
			t.Errorf("count = %d after publishing only last-resort packages, want 0 — counting one "+
				"would tell the client it has single-use stock it does not have", n)
		}
		has, err := s.store.HasLastResortKeyPackage(ctx, "u1", "d1")
		if err != nil || !has {
			t.Fatalf("HasLastResortKeyPackage = %v, %v", has, err)
		}

		// Publishing it three times must not have stored three. Claiming repeatedly returns the
		// same bytes and never exhausts, which is what one package looks like from outside.
		for i := 0; i < 4; i++ {
			kp, err := s.store.ClaimKeyPackage(ctx, "u1", "d1")
			if err != nil {
				t.Fatalf("claim %d: %v", i, err)
			}
			if string(kp.KeyPackage) != "last" || !kp.LastResort {
				t.Fatalf("claim %d returned %q (lastResort=%v)", i, kp.KeyPackage, kp.LastResort)
			}
		}
	})
}

// Single-use stock is what the count is for: a client watches it to know when to replenish, and it
// must fall as packages are consumed.
func TestConformance_CountTracksSingleUseStock(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		if err := s.store.AddKeyPackages(ctx, []domain.MLSKeyPackage{
			{UserID: "u1", DeviceID: "d1", KeyPackage: []byte("kp-1")},
			{UserID: "u1", DeviceID: "d1", KeyPackage: []byte("kp-2")},
			{UserID: "u1", DeviceID: "d1", KeyPackage: []byte("last"), LastResort: true},
		}); err != nil {
			t.Fatalf("add: %v", err)
		}

		if n, _ := s.store.CountKeyPackages(ctx, "u1", "d1"); n != 2 {
			t.Fatalf("count = %d, want the 2 single-use packages", n)
		}
		if _, err := s.store.ClaimKeyPackage(ctx, "u1", "d1"); err != nil {
			t.Fatalf("claim: %v", err)
		}
		if n, _ := s.store.CountKeyPackages(ctx, "u1", "d1"); n != 1 {
			t.Errorf("count = %d after one claim, want 1 — a client watching this would never "+
				"replenish", n)
		}

		// Drain the last single-use one; claims then fall back to the last-resort package rather
		// than failing, which is what keeps a busy device reachable.
		if _, err := s.store.ClaimKeyPackage(ctx, "u1", "d1"); err != nil {
			t.Fatalf("claim: %v", err)
		}
		kp, err := s.store.ClaimKeyPackage(ctx, "u1", "d1")
		if err != nil {
			t.Fatalf("claim after draining: %v", err)
		}
		if !kp.LastResort {
			t.Errorf("a drained device returned %q instead of its last-resort package", kp.KeyPackage)
		}
	})
}

// A claim is scoped to ONE DEVICE. An MLS leaf is a device, so a claim that returned some other
// device of the same user would build a group that locks the intended device out.
func TestConformance_KeyPackagesAreClaimedPerDevice(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		if err := s.store.AddKeyPackages(ctx, []domain.MLSKeyPackage{
			{UserID: "u1", DeviceID: "phone", KeyPackage: []byte("phone-kp")},
			{UserID: "u1", DeviceID: "laptop", KeyPackage: []byte("laptop-kp")},
		}); err != nil {
			t.Fatalf("add: %v", err)
		}

		kp, err := s.store.ClaimKeyPackage(ctx, "u1", "laptop")
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if string(kp.KeyPackage) != "laptop-kp" {
			t.Errorf("claiming for the laptop returned %q", kp.KeyPackage)
		}
		// The phone's package is untouched.
		if n, _ := s.store.CountKeyPackages(ctx, "u1", "phone"); n != 1 {
			t.Errorf("claiming for one device consumed another's package (phone has %d)", n)
		}
	})
}

func TestConformance_DevicesWithKeyPackagesListsPublishers(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		if err := s.store.AddKeyPackages(ctx, []domain.MLSKeyPackage{
			{UserID: "u1", DeviceID: "phone", KeyPackage: []byte("a")},
			{UserID: "u1", DeviceID: "laptop", KeyPackage: []byte("b")},
			{UserID: "u2", DeviceID: "tablet", KeyPackage: []byte("c")},
		}); err != nil {
			t.Fatalf("add: %v", err)
		}

		got, err := s.store.DevicesWithKeyPackages(ctx, []string{"u1", "u2", "u3"})
		if err != nil {
			t.Fatalf("devices: %v", err)
		}
		if len(got["u1"]) != 2 {
			t.Errorf("u1 has %v, want both devices", got["u1"])
		}
		// Sorted, because callers diff these lists and map iteration order is random.
		if len(got["u1"]) == 2 && got["u1"][0] > got["u1"][1] {
			t.Errorf("device ids are not sorted: %v", got["u1"])
		}
		if _, ok := got["u3"]; ok {
			t.Error("a user who has published nothing appeared in the directory")
		}
	})
}

// THE COMPARE-AND-SET. Two members committing against the same epoch: exactly one wins, and the
// loser is told where the group actually is. If both won, the group forks and neither half can read
// the other's messages ever again.
func TestConformance_CommitMLSGroupIsACompareAndSet(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		owner := mustUser(t, s.store, "cas@pheme.test")
		conv := mustConversation(t, s.store, owner.ID)

		msg := func(epoch int64) []domain.ChatMessage {
			return []domain.ChatMessage{{
				ConversationID: conv.ID, SenderID: owner.ID, Ciphertext: []byte("c"),
				ContentType: domain.ContentTypeMLSCommit, MLSEpoch: epoch,
				MLSGroupID: "group-1", CreatedAt: time.Now().UTC(),
			}}
		}

		state, stored, err := s.store.CommitMLSGroup(ctx, conv.ID, "group-1", 0, msg(1))
		if err != nil {
			t.Fatalf("establish: %v", err)
		}
		if state.Epoch != 1 || state.GroupID != "group-1" {
			t.Fatalf("after establishing: %+v", state)
		}
		if len(stored) != 1 {
			t.Errorf("the commit relayed %d messages, want 1", len(stored))
		}

		// A second commit against the SAME base epoch must be refused.
		_, _, err = s.store.CommitMLSGroup(ctx, conv.ID, "group-1", 0, msg(1))
		if !errors.Is(err, ErrEpochConflict) {
			t.Errorf("a stale commit returned %v, want ErrEpochConflict", err)
		}

		// And a commit for a DIFFERENT group id must be refused too, or two groups share one
		// conversation and every member picks one at random.
		_, _, err = s.store.CommitMLSGroup(ctx, conv.ID, "group-2", 1, msg(2))
		if err == nil {
			t.Error("a commit for a different group id was accepted; the conversation now has two")
		}
	})
}

// Under a real race, exactly one commit wins. A compare-and-set that is merely usually right forks
// the group occasionally, which is worse than one that never works, because nobody finds it.
func TestConformance_ConcurrentCommitsProduceOneWinner(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		owner := mustUser(t, s.store, "race-cas@pheme.test")
		conv := mustConversation(t, s.store, owner.ID)

		if _, _, err := s.store.CommitMLSGroup(ctx, conv.ID, "g", 0, []domain.ChatMessage{{
			ConversationID: conv.ID, SenderID: owner.ID, Ciphertext: []byte("c"),
			ContentType: domain.ContentTypeMLSCommit, MLSEpoch: 1, MLSGroupID: "g",
			CreatedAt: time.Now().UTC(),
		}}); err != nil {
			t.Fatalf("establish: %v", err)
		}

		const racers = 6
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins, conflicts := 0, 0

		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _, err := s.store.CommitMLSGroup(ctx, conv.ID, "g", 1, []domain.ChatMessage{{
					ConversationID: conv.ID, SenderID: owner.ID, Ciphertext: []byte("c"),
					ContentType: domain.ContentTypeMLSCommit, MLSEpoch: 2, MLSGroupID: "g",
					CreatedAt: time.Now().UTC(),
				}})
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					wins++
				case errors.Is(err, ErrEpochConflict):
					conflicts++
				}
			}()
		}
		wg.Wait()

		if wins != 1 {
			t.Errorf("%d of %d commits against one epoch succeeded, want exactly 1 — the group forks "+
				"the moment two do", wins, racers)
		}
		if conflicts != racers-1 {
			t.Errorf("%d commits were told they conflicted, want %d; the rest failed some other way",
				conflicts, racers-1)
		}

		final, err := s.store.MLSGroupState(ctx, conv.ID)
		if err != nil {
			t.Fatalf("state: %v", err)
		}
		if final.Epoch != 2 {
			t.Errorf("final epoch = %d, want 2 — one commit and only one applied", final.Epoch)
		}
	})
}

// A reset retires the current group and starts a new one, keeping the old id so its messages stay
// decryptable by whoever still holds its keys.
func TestConformance_ResetRetiresTheGroupAndKeepsItsID(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		owner := mustUser(t, s.store, "reset@pheme.test")
		conv := mustConversation(t, s.store, owner.ID)

		if _, _, err := s.store.CommitMLSGroup(ctx, conv.ID, "old-group", 0, []domain.ChatMessage{{
			ConversationID: conv.ID, SenderID: owner.ID, Ciphertext: []byte("c"),
			ContentType: domain.ContentTypeMLSCommit, MLSEpoch: 1, MLSGroupID: "old-group",
			CreatedAt: time.Now().UTC(),
		}}); err != nil {
			t.Fatalf("establish: %v", err)
		}

		after, err := s.store.ResetMLSGroup(ctx, conv.ID)
		if err != nil {
			t.Fatalf("reset: %v", err)
		}
		if after.GroupID == "old-group" {
			t.Error("the retired group is still current")
		}
		var kept bool
		for _, id := range after.PriorGroupIDs {
			if id == "old-group" {
				kept = true
			}
		}
		if !kept {
			t.Errorf("priorGroupIds = %v, want the retired group remembered so its messages stay "+
				"decryptable", after.PriorGroupIDs)
		}
	})
}

// GroupInfo is only useful for the CURRENT group. Keeping a retired group's export would have a
// joining device external-join into a group nobody is in.
func TestConformance_GroupInfoIsIgnoredForARetiredGroup(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		owner := mustUser(t, s.store, "ginfo@pheme.test")
		conv := mustConversation(t, s.store, owner.ID)

		if _, _, err := s.store.CommitMLSGroup(ctx, conv.ID, "g1", 0, []domain.ChatMessage{{
			ConversationID: conv.ID, SenderID: owner.ID, Ciphertext: []byte("c"),
			ContentType: domain.ContentTypeMLSCommit, MLSEpoch: 1, MLSGroupID: "g1",
			CreatedAt: time.Now().UTC(),
		}}); err != nil {
			t.Fatalf("establish: %v", err)
		}
		if err := s.store.SetMLSGroupInfo(ctx, conv.ID, "g1", 1, []byte("info")); err != nil {
			t.Fatalf("set: %v", err)
		}
		got, err := s.store.MLSGroupInfo(ctx, conv.ID)
		if err != nil || string(got.GroupInfo) != "info" {
			t.Fatalf("MLSGroupInfo = %+v, %v", got, err)
		}

		// For a group that is not current: ignored rather than stored.
		if err := s.store.SetMLSGroupInfo(ctx, conv.ID, "not-current", 9, []byte("wrong")); err != nil {
			t.Fatalf("set for another group: %v", err)
		}
		got, err = s.store.MLSGroupInfo(ctx, conv.ID)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if string(got.GroupInfo) == "wrong" {
			t.Error("GroupInfo for a group that is not current overwrote the live one")
		}
	})
}

// The key backup is the whole account. It must round-trip byte for byte, and re-backing-up must
// replace rather than accumulate.
func TestConformance_KeyBackupRoundTripsAndReplaces(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()

		if _, err := s.store.GetKeyBackup(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetKeyBackup for a user with none = %v, want ErrNotFound", err)
		}

		// The sealed bytes live in the blob store; this record keeps their id, plus the salt and
		// nonce inline. A whole transcript can exceed Mongo's 16 MB document cap, which is why the
		// ciphertext is not stored here.
		first := domain.MLSKeyBackup{
			UserID: "u1", DeviceID: "d1",
			Salt: []byte{1, 2}, Nonce: []byte{0x00, 0xff, 0xfe},
			CiphertextBlobID: "blob-first",
		}
		if err := s.store.PutKeyBackup(ctx, first); err != nil {
			t.Fatalf("put: %v", err)
		}
		got, err := s.store.GetKeyBackup(ctx, "u1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		// The nonce is binary, including NULs and high bytes: a string round trip here would
		// corrupt the seal and the backup would never open again.
		if string(got.Nonce) != string(first.Nonce) {
			t.Errorf("nonce came back as %v, want %v", got.Nonce, first.Nonce)
		}
		if got.CiphertextBlobID != "blob-first" {
			t.Errorf("blob id = %q, want it preserved", got.CiphertextBlobID)
		}

		second := first
		second.CiphertextBlobID = "blob-second"
		if err := s.store.PutKeyBackup(ctx, second); err != nil {
			t.Fatalf("re-put: %v", err)
		}
		got, err = s.store.GetKeyBackup(ctx, "u1")
		if err != nil {
			t.Fatalf("get after re-put: %v", err)
		}
		if got.CiphertextBlobID != "blob-second" {
			t.Errorf("a re-backup left the OLD blob in place (%q); a restore would return state the "+
				"device has since moved past", got.CiphertextBlobID)
		}
	})
}

// A read receipt only moves FORWARD. A device that has been offline reporting an old position must
// not drag everyone's ticks backwards.
func TestConformance_ReceiptsRoundTrip(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		owner := mustUser(t, s.store, "receipts@pheme.test")
		conv := mustConversation(t, s.store, owner.ID)

		got, err := s.store.SetConversationReceipt(ctx, conv.ID, owner.ID, 12, 12)
		if err != nil {
			t.Fatalf("set: %v", err)
		}
		if got.UserID != owner.ID {
			t.Errorf("receipt = %+v, want it attributed to the reader", got)
		}
		if got.ReadSeq != 12 {
			t.Errorf("readSeq = %d, want 12", got.ReadSeq)
		}
		// A lower report must not drag the watermark back.
		if got, _ := s.store.SetConversationReceipt(ctx, conv.ID, owner.ID, 0, 5); got.ReadSeq != 12 {
			t.Errorf("readSeq = %d after a lower report, want it held at 12", got.ReadSeq)
		}
	})
}

// Deleting a device's KeyPackages must be scoped to its OWNER as well as its id.
//
// Device ids are minted by clients, not issued by the server, so nothing stops two unrelated
// accounts holding the same one — and on a delete keyed by device id alone, one person tidying up
// after a retired device would strip another person's live device of everything it had published.
// The victim would not notice until somebody tried to add them to a group and found nothing to
// add, which reads as "this person cannot be messaged" long after the cause.
//
// Worth running against both stores rather than one: the two implementations filter separately,
// and a missing userId in a Mongo query looks nothing like a missing condition in a Go loop.
func TestConformance_DeletingKeyPackagesIsScopedToTheOwner(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()

		const shared = "same-client-minted-id"
		if err := s.store.AddKeyPackages(ctx, []domain.MLSKeyPackage{
			{UserID: "mine", DeviceID: shared, KeyPackage: []byte("mine-1")},
			{UserID: "mine", DeviceID: shared, KeyPackage: []byte("mine-2"), LastResort: true},
			{UserID: "theirs", DeviceID: shared, KeyPackage: []byte("theirs-1")},
			{UserID: "theirs", DeviceID: shared, KeyPackage: []byte("theirs-2"), LastResort: true},
			{UserID: "mine", DeviceID: "another-device", KeyPackage: []byte("other-1")},
		}); err != nil {
			t.Fatalf("add: %v", err)
		}

		if err := s.store.DeleteKeyPackages(ctx, "mine", shared); err != nil {
			t.Fatalf("delete: %v", err)
		}

		// Mine are gone, last-resort included — a package left behind adds a device to groups it
		// can no longer decrypt.
		if n, err := s.store.CountKeyPackages(ctx, "mine", shared); err != nil || n != 0 {
			t.Errorf("my device kept %d packages (err %v), want none", n, err)
		}
		if has, err := s.store.HasLastResortKeyPackage(ctx, "mine", shared); err != nil || has {
			t.Errorf("my device kept its last-resort package (has=%v, err %v)", has, err)
		}

		// Theirs are untouched.
		if n, err := s.store.CountKeyPackages(ctx, "theirs", shared); err != nil || n != 1 {
			t.Errorf("another account's device with the same id has %d single-use packages (err %v), "+
				"want 1 — deleting mine reached across accounts", n, err)
		}
		if has, err := s.store.HasLastResortKeyPackage(ctx, "theirs", shared); err != nil || !has {
			t.Errorf("another account's device lost its last-resort package (has=%v, err %v); it "+
				"silently stops being addable to groups", has, err)
		}

		// And my other device is untouched.
		if n, err := s.store.CountKeyPackages(ctx, "mine", "another-device"); err != nil || n != 1 {
			t.Errorf("my other device has %d packages (err %v), want 1", n, err)
		}
	})
}

// Paging back through messages that all share one timestamp must return each one
// exactly once. The load-older cursor bounds (createdAt, seq), not createdAt
// alone, so a page boundary that falls inside a group of same-millisecond
// messages neither skips nor repeats them.
func TestConformance_PaginationTiesBySeq(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		owner := mustUser(t, s.store, "pager@pheme.test")
		conv := mustConversation(t, s.store, owner.ID)

		// Six messages, ALL at the same instant — only their hub-assigned seq orders them.
		ts := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
		const n = 6
		for i := 0; i < n; i++ {
			if _, err := s.store.AppendChatMessage(ctx, domain.ChatMessage{
				ConversationID: conv.ID, SenderID: owner.ID,
				Ciphertext: []byte{byte('a' + i)}, ContentType: "application/octet-stream",
				CreatedAt: ts,
			}); err != nil {
				t.Fatalf("append %d: %v", i, err)
			}
		}

		// Page back two at a time, following the cursor, and collect every id seen.
		seen := map[string]int{}
		cursor := ""
		for pages := 0; pages < n+2; pages++ {
			page, err := s.store.ChatMessagesByConversation(ctx, conv.ID, cursor, 2, time.Time{})
			if err != nil {
				t.Fatalf("page: %v", err)
			}
			if len(page) == 0 {
				break
			}
			for _, m := range page {
				seen[m.ID]++
			}
			cursor = page[len(page)-1].ID
		}

		if len(seen) != n {
			t.Errorf("saw %d distinct messages across pages, want %d — the cursor skipped or stopped short", len(seen), n)
		}
		for id, count := range seen {
			if count != 1 {
				t.Errorf("message %s returned %d times, want exactly once — the cursor duplicated a tie", id, count)
			}
		}
	})
}

// The transcript's message count must survive a round trip in BOTH stores.
//
// It is the only thing the upload handler can compare one backup against another by — the
// server cannot open the sealed blob — and it is what stops a freshly installed device from
// replacing a full transcript with an empty one. The Mongo store writes an explicit field
// list, so a field added to the struct is persisted by neither implementation until it is
// added there too; that is exactly the kind of divergence this suite exists to catch, and it
// nearly shipped.
func TestConformance_KeyBackupCarriesTheTranscriptCount(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()

		full := domain.MLSKeyBackup{
			UserID:             "u-count",
			DeviceID:           "dev-1",
			Salt:               []byte("salt"),
			Nonce:              []byte("nonce"),
			CiphertextBlobID:   "blob-state",
			TranscriptSalt:     []byte("t-salt"),
			TranscriptNonce:    []byte("t-nonce"),
			TranscriptBlobID:   "blob-transcript",
			TranscriptMessages: 137,
			UpdatedAt:          time.Now().UTC(),
		}
		if err := s.store.PutKeyBackup(ctx, full); err != nil {
			t.Fatalf("put: %v", err)
		}
		got, err := s.store.GetKeyBackup(ctx, "u-count")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.TranscriptMessages != 137 {
			t.Fatalf("transcriptMessages = %d, want 137 — the count did not survive storage", got.TranscriptMessages)
		}

		// Replacing with a transcript-less backup must clear the count, not leave the old one
		// behind. A stale count is worse than none: the guard would compare against a number no
		// stored transcript backs, and refuse honest backups forever.
		bare := domain.MLSKeyBackup{
			UserID:           "u-count",
			DeviceID:         "dev-2",
			Salt:             []byte("salt2"),
			Nonce:            []byte("nonce2"),
			CiphertextBlobID: "blob-state-2",
			UpdatedAt:        time.Now().UTC(),
		}
		if err := s.store.PutKeyBackup(ctx, bare); err != nil {
			t.Fatalf("put bare: %v", err)
		}
		got, err = s.store.GetKeyBackup(ctx, "u-count")
		if err != nil {
			t.Fatalf("get after bare: %v", err)
		}
		if got.TranscriptMessages != 0 {
			t.Fatalf("transcriptMessages = %d after a transcript-less replace, want 0", got.TranscriptMessages)
		}
	})
}

// The archive of superseded backups, in BOTH stores.
//
// Replacing a backup used to be the same act as destroying it — the previous transcript's salt and
// nonce lived on the record and went with it, so even a surviving blob was noise. What has to hold
// is that an archived version keeps its own seal parameters, that the archive is bounded, and that
// pruning hands back what it dropped so the blobs can go too.
func TestConformance_KeyBackupArchiveKeepsSupersededVersions(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()

		version := func(i int) domain.MLSKeyBackup {
			return domain.MLSKeyBackup{
				UserID:             "u-archive",
				DeviceID:           fmt.Sprintf("dev-%d", i),
				Salt:               []byte(fmt.Sprintf("salt-%d", i)),
				Nonce:              []byte(fmt.Sprintf("nonce-%d", i)),
				CiphertextBlobID:   fmt.Sprintf("state-blob-%d", i),
				TranscriptSalt:     []byte(fmt.Sprintf("t-salt-%d", i)),
				TranscriptNonce:    []byte(fmt.Sprintf("t-nonce-%d", i)),
				TranscriptBlobID:   fmt.Sprintf("transcript-blob-%d", i),
				TranscriptMessages: (i + 1) * 10,
				// Distinct and increasing, since newest-first ordering is what the fallback reads.
				UpdatedAt: time.Now().UTC().Add(time.Duration(i) * time.Minute),
			}
		}

		for i := 0; i < 3; i++ {
			if _, err := s.store.ArchiveKeyBackup(ctx, version(i), 5); err != nil {
				t.Fatalf("archive %d: %v", i, err)
			}
		}

		listed, err := s.store.ListKeyBackupVersions(ctx, "u-archive")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(listed) != 3 {
			t.Fatalf("listed %d versions, want 3", len(listed))
		}
		// Newest first — a fallback walks this in order and must try the most recent history first.
		if listed[0].DeviceID != "dev-2" || listed[2].DeviceID != "dev-0" {
			t.Fatalf("wrong order: %s … %s", listed[0].DeviceID, listed[2].DeviceID)
		}
		// Each version's OWN seal parameters survived. This is the property whose absence made a
		// replaced backup unopenable.
		if string(listed[2].TranscriptSalt) != "t-salt-0" ||
			string(listed[2].TranscriptNonce) != "t-nonce-0" {
			t.Fatal("an archived version lost its own salt/nonce")
		}
		if listed[2].TranscriptMessages != 10 {
			t.Fatalf("archived count = %d, want 10", listed[2].TranscriptMessages)
		}

		// By id, scoped to the owner.
		one, err := s.store.KeyBackupVersion(ctx, "u-archive", listed[0].ID)
		if err != nil {
			t.Fatalf("by id: %v", err)
		}
		if one.DeviceID != "dev-2" {
			t.Fatalf("fetched %s, want dev-2", one.DeviceID)
		}
		if _, err := s.store.KeyBackupVersion(ctx, "somebody-else", listed[0].ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("another user fetched this version: %v", err)
		}
		if _, err := s.store.KeyBackupVersion(ctx, "u-archive", "no-such-id"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown id: %v", err)
		}
	})
}

// Bounded, and it says what it dropped — the caller deletes those blobs, so a prune that stayed
// silent would leak a transcript per replacement forever.
func TestConformance_KeyBackupArchiveIsBoundedAndReportsPruned(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()

		var allPruned []domain.MLSKeyBackup
		for i := 0; i < 5; i++ {
			pruned, err := s.store.ArchiveKeyBackup(ctx, domain.MLSKeyBackup{
				UserID:           "u-bounded",
				DeviceID:         fmt.Sprintf("dev-%d", i),
				CiphertextBlobID: fmt.Sprintf("state-%d", i),
				TranscriptBlobID: fmt.Sprintf("transcript-%d", i),
				UpdatedAt:        time.Now().UTC().Add(time.Duration(i) * time.Minute),
			}, 2)
			if err != nil {
				t.Fatalf("archive %d: %v", i, err)
			}
			allPruned = append(allPruned, pruned...)
		}

		listed, err := s.store.ListKeyBackupVersions(ctx, "u-bounded")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(listed) != 2 {
			t.Fatalf("archive holds %d, want the 2 it was bounded to", len(listed))
		}
		// Five archived, two kept: three had to be reported so their blobs could be deleted.
		if len(allPruned) != 3 {
			t.Fatalf("pruned %d versions, want 3 — a silent prune leaks a transcript each time",
				len(allPruned))
		}
	})
}
