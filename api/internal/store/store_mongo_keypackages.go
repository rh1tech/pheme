package store

import (
	"context"
	"sort"

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

func (m *Mongo) DeleteKeyPackages(ctx context.Context, userID, deviceID string) error {
	_, err := m.db.Collection("mlsKeyPackages").
		DeleteMany(ctx, bson.M{"userId": userID, "deviceId": deviceID})
	return err
}

func (m *Mongo) HasLastResortKeyPackage(ctx context.Context, userID, deviceID string) (bool, error) {
	n, err := m.db.Collection("mlsKeyPackages").CountDocuments(ctx,
		bson.M{"userId": userID, "deviceId": deviceID, "lastResort": true})
	return n > 0, err
}

// ClaimKeyPackage hands out one KeyPackage belonging to ONE DEVICE of a user. A
// single-use package is removed atomically (FindOneAndDelete, so two concurrent
// claimants never get the same one). When only the last-resort package is left it is
// returned WITHOUT being deleted: KeyPackages are otherwise single-use, so a caller
// looping on this endpoint could otherwise drain a device's stock and leave it
// unreachable.
//
// The deviceId in the filter is the whole point. Claiming by user alone returns a
// package belonging to whichever device Mongo found first, so the group that gets
// built contains one arbitrary device of that person — and every other device they
// own sees the conversation as undecryptable noise.
func (m *Mongo) ClaimKeyPackage(ctx context.Context, userID, deviceID string) (domain.MLSKeyPackage, error) {
	col := m.db.Collection("mlsKeyPackages")
	var kp domain.MLSKeyPackage
	err := col.FindOneAndDelete(ctx,
		bson.M{"userId": userID, "deviceId": deviceID, "lastResort": bson.M{"$ne": true}}).Decode(&kp)
	if err == nil {
		return kp, nil
	}
	if mapErr(err) != ErrNotFound {
		return domain.MLSKeyPackage{}, mapErr(err)
	}
	// Stock exhausted — fall back to this device's reusable last-resort package.
	err = col.FindOne(ctx,
		bson.M{"userId": userID, "deviceId": deviceID, "lastResort": true}).Decode(&kp)
	return kp, mapErr(err)
}

// DevicesWithKeyPackages lists the devices each user has published KeyPackages for,
// consuming nothing. It is the non-destructive half of the directory: a member needs
// to know which devices exist before it can tell which of them are missing from a
// group, and it cannot learn that by claiming.
func (m *Mongo) DevicesWithKeyPackages(ctx context.Context, userIDs []string) (map[string][]string, error) {
	out := make(map[string][]string, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	cur, err := m.db.Collection("mlsKeyPackages").Aggregate(ctx, []bson.M{
		{"$match": bson.M{"userId": bson.M{"$in": userIDs}}},
		{"$group": bson.M{"_id": bson.M{"userId": "$userId", "deviceId": "$deviceId"}}},
	})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var rows []struct {
		ID struct {
			UserID   string `bson:"userId"`
			DeviceID string `bson:"deviceId"`
		} `bson:"_id"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.ID.DeviceID == "" {
			continue
		}
		out[r.ID.UserID] = append(out[r.ID.UserID], r.ID.DeviceID)
	}
	for _, devices := range out {
		sort.Strings(devices) // stable order, so callers can diff without sorting
	}
	return out, nil
}

// MLSControlMessagesSince returns the Welcomes and Commits past `sinceEpoch`, oldest
// first — the order a member catching up must apply them in. Within one epoch the
// Welcome sorts before the Commit, or a device being admitted would meet the Commit for
// a group it has not joined yet.
func (m *Mongo) MLSControlMessagesSince(ctx context.Context, conversationID string, sinceEpoch int64) ([]domain.ChatMessage, error) {
	cur, err := m.db.Collection("chatMessages").Find(
		ctx,
		bson.M{"conversationId": conversationID, "mlsEpoch": bson.M{"$gt": sinceEpoch}},
		options.Find().SetSort(bson.D{
			{Key: "mlsEpoch", Value: 1},
			// "application/mls-welcome" < "application/mls-commit" is false alphabetically,
			// so sort descending on contentType to put the Welcome first.
			{Key: "contentType", Value: -1},
		}),
	)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []domain.ChatMessage
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// MLSGroupState reads the conversation's MLS group id and epoch.
func (m *Mongo) MLSGroupState(ctx context.Context, conversationID string) (domain.MLSGroupState, error) {
	conv, err := m.ConversationByID(ctx, conversationID)
	if err != nil {
		return domain.MLSGroupState{}, err
	}
	return conv.MLS, nil
}

// CommitMLSGroup is the compare-and-set that keeps every member on one group history,
// and relays the Commit in the same step.
//
// The decision is a single conditional update, which is what makes it safe: the filter
// names the state the caller believed it was committing against, so of two concurrent
// Commits at the same epoch exactly one matches and the other comes back
// ErrEpochConflict — with the current state, so it can catch up and retry.
//
// Two shapes, both handled by the same update:
//
//   - Establishing the group (baseEpoch 0, no group id recorded): claims the id. Two
//     devices racing to set up the same conversation cannot both win, so the group is
//     never silently replaced.
//   - Advancing it (the id matches and the epoch is exactly baseEpoch): moves to
//     baseEpoch+1.
func (m *Mongo) CommitMLSGroup(
	ctx context.Context, conversationID, groupID string, baseEpoch int64, msgs []domain.ChatMessage,
) (domain.MLSGroupState, []domain.ChatMessage, error) {
	filter := bson.M{"_id": conversationID}
	if baseEpoch == 0 {
		// Establishing: only if nobody else has. An absent field and an empty string
		// both mean "no group yet" — a conversation created before MLS group ids
		// existed has no such field at all.
		filter["$or"] = []bson.M{
			{"mlsGroupId": bson.M{"$exists": false}},
			{"mlsGroupId": ""},
		}
	} else {
		filter["mlsGroupId"] = groupID
		filter["mlsEpoch"] = baseEpoch
	}

	var updated domain.Conversation
	err := m.db.Collection("conversations").FindOneAndUpdate(
		ctx,
		filter,
		bson.M{"$set": bson.M{"mlsGroupId": groupID, "mlsEpoch": baseEpoch + 1}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updated)
	if err != nil {
		if mapErr(err) != ErrNotFound {
			return domain.MLSGroupState{}, nil, mapErr(err)
		}
		// The filter did not match: either the conversation is gone, or — far more
		// likely — somebody else committed first. Say which, and hand back the state to
		// catch up to.
		current, convErr := m.MLSGroupState(ctx, conversationID)
		if convErr != nil {
			return domain.MLSGroupState{}, nil, convErr
		}
		return current, nil, ErrEpochConflict
	}

	stored, err := m.appendChatMessages(ctx, msgs)
	if err != nil {
		// The epoch moved but the Commit never reached anyone. Left alone, every other
		// member would sit an epoch behind forever, with no Commit in the log to catch
		// up on — the conversation would simply stop working. Put the epoch back.
		//
		// The rollback is itself conditional on the state we just wrote, so it cannot
		// undo somebody else's later Commit: if another member has already advanced past
		// us, their Commit IS in the log and the group is healthy.
		m.rollbackMLSGroup(ctx, conversationID, groupID, baseEpoch)
		return domain.MLSGroupState{}, nil, err
	}
	return updated.MLS, stored, nil
}

// rollbackMLSGroup undoes a compare-and-set whose Commit could not be relayed. Best
// effort: if it fails there is nothing further to try, so it is logged by the caller's
// error rather than reported here.
func (m *Mongo) rollbackMLSGroup(ctx context.Context, conversationID, groupID string, baseEpoch int64) {
	restore := bson.M{"mlsGroupId": groupID, "mlsEpoch": baseEpoch}
	if baseEpoch == 0 {
		// It was an establish: there was no group before us, so there must be none after.
		restore = bson.M{"mlsGroupId": "", "mlsEpoch": int64(0)}
	}
	_, _ = m.db.Collection("conversations").UpdateOne(
		ctx,
		bson.M{"_id": conversationID, "mlsGroupId": groupID, "mlsEpoch": baseEpoch + 1},
		bson.M{"$set": restore},
	)
}

func (m *Mongo) appendChatMessages(ctx context.Context, msgs []domain.ChatMessage) ([]domain.ChatMessage, error) {
	stored := make([]domain.ChatMessage, 0, len(msgs))
	for _, msg := range msgs {
		saved, err := m.AppendChatMessage(ctx, msg)
		if err != nil {
			return nil, err
		}
		stored = append(stored, saved)
	}
	return stored, nil
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
