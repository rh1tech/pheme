package store

import (
	"context"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/domain"
)

// Starting up against a database that already carries the bug.
//
// This is the test that matters most about the unique index on a push address, and it is not about
// the index at all — it is about STARTUP. A unique index refuses to build while a violation exists,
// so shipping one without clearing the violations first would fail NewMongo on exactly the
// deployments carrying the bug, and a failed NewMongo is an API that does not start. On a
// self-hosted product that is somebody else's outage, caused by our fix.
//
// So migrate() runs before ensureIndexes(), and this proves the pair works on dirty data rather
// than only on a clean database where any ordering passes.
func TestMongoStartsAndCleansUpDevicesSharingAPushAddress(t *testing.T) {
	uri := os.Getenv("PHEME_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("PHEME_TEST_MONGO_URI not set — this one needs the real thing")
	}
	ctx := context.Background()
	blobs := blob.NewMemory()
	dbName := "pheme_test_device_migration"

	// Seeded through a RAW client, not through NewMongo. NewMongo is the thing under test: it builds
	// the unique index, so seeding through it would be seeding into a database that already refuses
	// the state we need — as it did on the first attempt at this test. A database from before the
	// fix has no such index, and that is what has to be simulated.
	raw, err := mongo.Connect(ctx, options.Client().ApplyURI(withPoolLimits(t, uri)))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	rawDB := raw.Database(dbName)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = rawDB.Drop(clean)
		_ = raw.Disconnect(clean)
	})
	if err := rawDB.Drop(ctx); err != nil {
		t.Fatalf("clear any previous run: %v", err)
	}

	// Inserted RAW, deliberately: CreateDevice would refuse to produce this state now, and the
	// state under test is the one left behind by the version that would.
	const shared = "one-handset-one-token"
	older := time.Now().UTC().Add(-2 * time.Hour)
	newer := time.Now().UTC()
	docs := []any{
		domain.Device{
			ID: "row-previous-owner", UserID: "medved", Platform: domain.PlatformIOS,
			FCMToken: shared, VoIPToken: "voip", CreatedAt: older, LastSeenAt: older,
		},
		domain.Device{
			ID: "row-current-owner", UserID: "xtreme", Platform: domain.PlatformIOS,
			FCMToken: shared, VoIPToken: "voip", CreatedAt: newer, LastSeenAt: newer,
		},
	}
	if _, err := rawDB.Collection("devices").InsertMany(ctx, docs); err != nil {
		t.Fatalf("seed the pre-fix state: %v", err)
	}

	// The upgrade: a fresh process opening the same database.
	upgraded, err := NewMongo(ctx, withPoolLimits(t, uri), dbName, blobs)
	if err != nil {
		t.Fatalf("startup against a database holding the bug failed — this is the API refusing to "+
			"boot on an existing deployment: %v", err)
	}
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = upgraded.client.Disconnect(clean)
	}()

	var rows []domain.Device
	cursor, err := upgraded.db.Collection("devices").Find(ctx, bson.M{"fcmToken": shared})
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if err := cursor.All(ctx, &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("the address is still held by %d rows; the handset remains two people's device", len(rows))
	}
	if rows[0].UserID != "xtreme" {
		t.Errorf("kept the wrong row: %q — the most recent registration is the current owner", rows[0].UserID)
	}

	// And the invariant is now the database's, not the code's: a second account cannot take the
	// address without releasing it first.
	_, err = upgraded.db.Collection("devices").InsertOne(ctx, domain.Device{
		ID: "row-interloper", UserID: "someone-else", Platform: domain.PlatformIOS,
		FCMToken: shared, CreatedAt: newer, LastSeenAt: newer,
	})
	if err == nil {
		t.Error("a second account was able to claim the same push address; the unique index is not in force")
	}
}
