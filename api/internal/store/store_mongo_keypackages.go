package store

import (
	"context"

	"github.com/rh1tech/pheme/api/internal/domain"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// AddKeyPackages stores exactly what it is given. Whether a package is last-resort
// is decided by the client that built it (an RFC 9420 extension in the bytes) — the
// server only records the fact, it cannot confer it.
func (m *Mongo) AddKeyPackages(ctx context.Context, packages []domain.MLSKeyPackage) error {
	if len(packages) == 0 {
		return nil
	}
	docs := make([]any, 0, len(packages))
	for _, kp := range packages {
		if kp.ID == "" {
			kp.ID = mongoID()
		}
		docs = append(docs, kp)
	}
	// Unordered, so a rejected duplicate does not abandon the rest of the batch. The
	// only duplicate possible is a second last-resort package, which the partial
	// unique index refuses — two tabs replenishing at once is a race we expect, and
	// losing it simply means the package we already have stands.
	_, err := m.db.Collection("mlsKeyPackages").
		InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	if mongo.IsDuplicateKeyError(err) {
		return nil
	}
	return err
}

func (m *Mongo) HasLastResortKeyPackage(ctx context.Context, userID, deviceID string) (bool, error) {
	n, err := m.db.Collection("mlsKeyPackages").CountDocuments(ctx,
		bson.M{"userId": userID, "deviceId": deviceID, "lastResort": true})
	return n > 0, err
}

// ClaimKeyPackage hands out one of a user's KeyPackages. A single-use package is
// removed atomically (FindOneAndDelete, so two concurrent claimants never get the
// same one). When only the last-resort package is left it is returned WITHOUT being
// deleted: KeyPackages are otherwise single-use, so a caller looping on this
// endpoint could otherwise drain a user's stock and leave them unreachable.
func (m *Mongo) ClaimKeyPackage(ctx context.Context, userID string) (domain.MLSKeyPackage, error) {
	col := m.db.Collection("mlsKeyPackages")
	var kp domain.MLSKeyPackage
	err := col.FindOneAndDelete(ctx,
		bson.M{"userId": userID, "lastResort": bson.M{"$ne": true}}).Decode(&kp)
	if err == nil {
		return kp, nil
	}
	if mapErr(err) != ErrNotFound {
		return domain.MLSKeyPackage{}, mapErr(err)
	}
	// Stock exhausted — fall back to the reusable last-resort package.
	err = col.FindOne(ctx, bson.M{"userId": userID, "lastResort": true}).Decode(&kp)
	return kp, mapErr(err)
}

// CountKeyPackages counts only the consumable packages. The last-resort one is
// never used up, so counting it would tell the client it has stock it does not
// have, and it would stop replenishing.
func (m *Mongo) CountKeyPackages(ctx context.Context, userID, deviceID string) (int64, error) {
	return m.db.Collection("mlsKeyPackages").CountDocuments(ctx,
		bson.M{"userId": userID, "deviceId": deviceID, "lastResort": bson.M{"$ne": true}})
}

func (m *Mongo) PutKeyBackup(ctx context.Context, backup domain.MLSKeyBackup) error {
	// One backup per user: upsert on userId so a re-upload replaces the previous.
	// _id is immutable, so it is only set on insert, never in the update body.
	_, err := m.db.Collection("mlsKeyBackups").UpdateOne(
		ctx,
		bson.M{"userId": backup.UserID},
		bson.M{
			"$set": bson.M{
				"deviceId":   backup.DeviceID,
				"salt":       backup.Salt,
				"nonce":      backup.Nonce,
				"ciphertext": backup.Ciphertext,
				"updatedAt":  backup.UpdatedAt,
			},
			"$setOnInsert": bson.M{"_id": mongoID(), "userId": backup.UserID},
		},
		options.Update().SetUpsert(true),
	)
	return err
}

func (m *Mongo) GetKeyBackup(ctx context.Context, userID string) (domain.MLSKeyBackup, error) {
	var b domain.MLSKeyBackup
	err := m.db.Collection("mlsKeyBackups").
		FindOne(ctx, bson.M{"userId": userID}).Decode(&b)
	return b, mapErr(err)
}
