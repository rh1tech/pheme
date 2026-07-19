package store

import (
	"context"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// The device registry, run against BOTH stores.
//
// Terminating a device has to reach two separate places, and this project has already paid for
// getting that wrong: the MLS registry, which decides whether co-members still offer the device a
// leaf in the group, and the push rows, which decide whether it still gets woken. A termination
// that touches one and not the other leaves a device that is either still being pushed to or still
// being admitted to groups — and in both cases the person who pressed "remove this device" was told
// it worked.
//
// The tombstone is the subtle part. Removing the row entirely would be simpler and is wrong: a
// co-member cannot then tell a terminated device from one that has simply never published keys,
// and it is the difference between pruning a leaf and waiting for one.

func seedMLSDevice(t *testing.T, s Store, userID, deviceID string) {
	t.Helper()
	if err := s.UpsertMLSDevice(context.Background(), domain.MLSDevice{
		UserID: userID, DeviceID: deviceID, Label: deviceID,
		CreatedAt: time.Now().UTC(), LastSeenAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert device %s: %v", deviceID, err)
	}
}

func TestConformance_ADevicesRegistrationRoundTrips(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		seedMLSDevice(t, s.store, "alice", "phone")
		seedMLSDevice(t, s.store, "alice", "laptop")
		seedMLSDevice(t, s.store, "bob", "phone") // same client-minted id, different person

		devices, err := s.store.ListMLSDevices(ctx, "alice")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(devices) != 2 {
			t.Fatalf("alice has %d devices, want 2: %+v", len(devices), devices)
		}
		for _, d := range devices {
			if d.UserID != "alice" {
				t.Errorf("another user's device appeared in alice's list: %+v", d)
			}
		}
	})
}

// Upserting the same device again updates it rather than duplicating it. A device that re-announced
// itself on every launch would otherwise fill the list with copies of itself.
func TestConformance_ReregisteringADeviceUpdatesRatherThanDuplicates(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		seedMLSDevice(t, s.store, "alice", "phone")
		seedMLSDevice(t, s.store, "alice", "phone")
		seedMLSDevice(t, s.store, "alice", "phone")

		devices, err := s.store.ListMLSDevices(ctx, "alice")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(devices) != 1 {
			t.Errorf("re-registering one device produced %d rows; the device list fills with copies "+
				"of the same phone", len(devices))
		}
	})
}

// THE ONE THAT MATTERS. A revoked device is TOMBSTONED, not forgotten.
func TestConformance_RevokingADeviceLeavesATombstone(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		seedMLSDevice(t, s.store, "alice", "old-phone")
		seedMLSDevice(t, s.store, "alice", "current-phone")

		if err := s.store.RevokeMLSDevice(ctx, "alice", "old-phone", time.Now().UTC()); err != nil {
			t.Fatalf("revoke: %v", err)
		}

		revoked, err := s.store.RevokedDeviceIDs(ctx, []string{"alice"})
		if err != nil {
			t.Fatalf("revoked ids: %v", err)
		}
		if !contains(revoked["alice"], "old-phone") {
			t.Errorf("a revoked device is not reported as revoked (%v); co-members cannot tell it "+
				"from a device that has simply never published keys, so they wait for a leaf that "+
				"is never coming instead of pruning one", revoked["alice"])
		}
		if contains(revoked["alice"], "current-phone") {
			t.Errorf("revoking one device marked another as revoked (%v); the person's working "+
				"phone gets pruned out of their groups", revoked["alice"])
		}
	})
}

// Revocation is per user. One person terminating a device must not prune another's, even when the
// client-minted ids collide.
func TestConformance_RevocationDoesNotCrossAccounts(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		const shared = "phone" // the same id on two unrelated accounts
		seedMLSDevice(t, s.store, "alice", shared)
		seedMLSDevice(t, s.store, "bob", shared)

		if err := s.store.RevokeMLSDevice(ctx, "alice", shared, time.Now().UTC()); err != nil {
			t.Fatalf("revoke: %v", err)
		}

		revoked, err := s.store.RevokedDeviceIDs(ctx, []string{"alice", "bob"})
		if err != nil {
			t.Fatalf("revoked ids: %v", err)
		}
		if !contains(revoked["alice"], shared) {
			t.Errorf("alice's device was not revoked: %v", revoked)
		}
		if contains(revoked["bob"], shared) {
			t.Errorf("revoking alice's device revoked bob's device of the same client-minted id "+
				"(%v); bob is pruned out of his groups for something he did not do", revoked["bob"])
		}
	})
}

// Deleting a device removes it from the list, and only it.
func TestConformance_DeletingAnMLSDevice(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		seedMLSDevice(t, s.store, "alice", "phone")
		seedMLSDevice(t, s.store, "alice", "laptop")
		seedMLSDevice(t, s.store, "bob", "phone")

		if err := s.store.DeleteMLSDevice(ctx, "alice", "phone"); err != nil {
			t.Fatalf("delete: %v", err)
		}

		alices, err := s.store.ListMLSDevices(ctx, "alice")
		if err != nil {
			t.Fatalf("list alice: %v", err)
		}
		if len(alices) != 1 || alices[0].DeviceID != "laptop" {
			t.Errorf("alice's devices are %+v, want only the laptop", alices)
		}
		bobs, err := s.store.ListMLSDevices(ctx, "bob")
		if err != nil {
			t.Fatalf("list bob: %v", err)
		}
		if len(bobs) != 1 {
			t.Errorf("deleting alice's phone removed bob's device of the same id: %+v", bobs)
		}

		// Deleting one that is not there is not an error — a client tidying up must not have to
		// know whether it already succeeded.
		if err := s.store.DeleteMLSDevice(ctx, "alice", "never-existed"); err != nil {
			t.Errorf("deleting an unknown device = %v, want nil", err)
		}
	})
}

// THE OTHER HALF OF A TERMINATION. Removing the MLS device must also stop it being pushed to.
func TestConformance_TerminatingADeviceRemovesItsPushAddresses(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()

		// Two push rows for the ONE terminated device, from two different tokens.
		//
		// That is how a phone accumulates rows in production: the token rotates, the app registers
		// the new one, and the old row stays behind — which is how one account ended up with four
		// Android rows for one handset. Registering the SAME token twice would not produce two rows,
		// because CreateDevice treats a device as its push address and updates in place; an earlier
		// version of this test did that and then asserted two rows had been removed, which was the
		// test misunderstanding the store rather than the store misbehaving.
		for _, token := range []string{"token-old", "token-rotated"} {
			if _, err := s.store.CreateDevice(ctx, domain.Device{
				UserID: "alice", Platform: domain.PlatformAndroid, MLSDeviceID: "old-phone",
				FCMToken: token, CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("create push device: %v", err)
			}
		}
		if _, err := s.store.CreateDevice(ctx, domain.Device{
			UserID: "alice", Platform: domain.PlatformWeb, MLSDeviceID: "current-phone",
			WebPushSub: `{"endpoint":"https://example.test"}`, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("create push device: %v", err)
		}

		removed, err := s.store.DeletePushDevicesForMLSDevice(ctx, "alice", "old-phone")
		if err != nil {
			t.Fatalf("delete push devices: %v", err)
		}
		// BOTH rows for that phone, not just the newest. A termination that removes only the
		// current token leaves the rotated-away row still being pushed to.
		if removed != 2 {
			t.Errorf("removed %d push rows, want both belonging to the terminated device", removed)
		}

		remaining, err := s.store.DevicesForUsers(ctx, []string{"alice"})
		if err != nil {
			t.Fatalf("list push devices: %v", err)
		}
		for _, d := range remaining {
			if d.MLSDeviceID == "old-phone" {
				t.Errorf("a terminated device still has a push address (%+v); the person was told "+
					"the device was removed and it keeps receiving their messages", d)
			}
		}
		if len(remaining) != 1 {
			t.Errorf("%d push rows remain, want the surviving device's one: %+v", len(remaining), remaining)
		}
	})
}

// Removing a single push address by id. This is what the dispatcher calls when a push provider
// reports an address permanently dead, so the caller is covered and this was not.
func TestConformance_DeletingOnePushDevice(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()

		doomed, err := s.store.CreateDevice(ctx, domain.Device{
			UserID: "alice", Platform: domain.PlatformAndroid, FCMToken: "dead-token",
			CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		keeper, err := s.store.CreateDevice(ctx, domain.Device{
			UserID: "alice", Platform: domain.PlatformAndroid, FCMToken: "live-token",
			CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		if err := s.store.DeleteDevice(ctx, doomed.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}

		remaining, err := s.store.DevicesForUsers(ctx, []string{"alice"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(remaining) != 1 || remaining[0].ID != keeper.ID {
			t.Errorf("after deleting one address the user has %+v, want only the live one", remaining)
		}

		// Deleting one that is already gone is not an error. The dispatcher prunes from a result
		// set that may name the same address twice, and a second delete must not fail the batch.
		if err := s.store.DeleteDevice(ctx, doomed.ID); err != nil {
			t.Errorf("deleting an already-deleted device = %v, want nil", err)
		}
		if err := s.store.DeleteDevice(ctx, "000000000000000000000000"); err != nil {
			t.Errorf("deleting an unknown device = %v, want nil", err)
		}
	})
}

// Re-registering must be able to ATTACH an MLS identity that the first registration lacked.
//
// A client registers its push address when the app starts, which can happen before it has minted
// its MLS identity — so the first registration legitimately carries none, and every later one
// carries it. But every later one also matches the dedupe, and the update path listed the fields
// that "legitimately change" without including this one. The row kept its empty value forever, no
// matter how many times the client sent the right thing.
//
// The cost is not cosmetic. A push address that cannot be traced to an MLS device cannot be removed
// when that device is revoked, and so is refused message previews. A phone that registered a moment
// too early would show "New message" for the rest of its life and could not recover.
func TestConformance_ReregisteringAttachesAMissingMLSIdentity(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()

		// First registration: the app started before the MLS identity existed.
		first, err := s.store.CreateDevice(ctx, domain.Device{
			UserID: "u-heal", Platform: domain.PlatformAndroid,
			FCMToken: "token-heal", CanRenderPreview: true,
			CreatedAt: time.Now().UTC(), LastSeenAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("first registration: %v", err)
		}
		if first.MLSDeviceID != "" {
			t.Fatalf("setup: expected no MLS device, got %q", first.MLSDeviceID)
		}

		// A later launch, same push address, now with the identity.
		again, err := s.store.CreateDevice(ctx, domain.Device{
			UserID: "u-heal", Platform: domain.PlatformAndroid,
			FCMToken: "token-heal", CanRenderPreview: true,
			MLSDeviceID: "mls-heal",
			CreatedAt:   time.Now().UTC(), LastSeenAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("re-registration: %v", err)
		}
		if again.ID != first.ID {
			t.Errorf("re-registering created a second row (%s vs %s); the fan-out would push to this "+
				"phone twice for every message", again.ID, first.ID)
		}
		if again.MLSDeviceID != "mls-heal" {
			t.Errorf("re-registration returned MLSDeviceID %q, want mls-heal", again.MLSDeviceID)
		}

		// And it must be PERSISTED, not just reflected in the return value.
		devices, err := s.store.DevicesForUsers(ctx, []string{"u-heal"})
		if err != nil {
			t.Fatalf("load devices: %v", err)
		}
		if len(devices) != 1 {
			t.Fatalf("user has %d device rows, want 1", len(devices))
		}
		if devices[0].MLSDeviceID != "mls-heal" {
			t.Errorf("the stored row still has MLSDeviceID %q. It cannot be removed when the device "+
				"is revoked, and is refused message previews for that reason — permanently, however "+
				"many times the client sends the right value.", devices[0].MLSDeviceID)
		}
	})
}

// The other direction: a registration with no identity must not ERASE one already recorded. A Mac
// has no MLS identity to send, and a client that has not minted one yet sends nothing — neither is
// a reason to forget what an earlier registration established.
func TestConformance_ReregisteringDoesNotEraseAnMLSIdentity(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()

		if _, err := s.store.CreateDevice(ctx, domain.Device{
			UserID: "u-keep", Platform: domain.PlatformAndroid,
			FCMToken: "token-keep", MLSDeviceID: "mls-keep",
			CreatedAt: time.Now().UTC(), LastSeenAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("first registration: %v", err)
		}

		again, err := s.store.CreateDevice(ctx, domain.Device{
			UserID: "u-keep", Platform: domain.PlatformAndroid,
			FCMToken:  "token-keep", // no MLSDeviceID this time
			CreatedAt: time.Now().UTC(), LastSeenAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("re-registration: %v", err)
		}
		if again.MLSDeviceID != "mls-keep" {
			t.Errorf("a registration carrying no identity erased the recorded one (%q); the address "+
				"becomes unrevocable and loses previews", again.MLSDeviceID)
		}
	})
}
