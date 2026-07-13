package store

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"github.com/rh1tech/pheme/api/internal/domain"
)

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
	_, err := m.db.Collection("mlsKeyPackages").InsertMany(ctx, docs)
	return err
}

func (m *Mongo) ClaimKeyPackage(ctx context.Context, userID string) (domain.MLSKeyPackage, error) {
	// Atomic claim: find one and delete it in a single round trip, so two
	// concurrent claimants never get the same single-use package.
	var kp domain.MLSKeyPackage
	err := m.db.Collection("mlsKeyPackages").
		FindOneAndDelete(ctx, bson.M{"userId": userID}).Decode(&kp)
	return kp, mapErr(err)
}

func (m *Mongo) CountKeyPackages(ctx context.Context, userID, deviceID string) (int64, error) {
	return m.db.Collection("mlsKeyPackages").
		CountDocuments(ctx, bson.M{"userId": userID, "deviceId": deviceID})
}
