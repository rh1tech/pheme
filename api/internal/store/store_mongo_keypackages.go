package store

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/mlschain"
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
func (m *Mongo) MLSControlMessagesSince(ctx context.Context, conversationID, groupID string, sinceEpoch int64) ([]domain.ChatMessage, error) {
	filter := bson.M{"conversationId": conversationID, "mlsEpoch": bson.M{"$gt": sinceEpoch}}
	if groupID != "" {
		// This group's own history, plus anything written before control messages recorded which
		// group they belonged to. Excluding the untagged ones would leave every conversation that
		// predates the field unable to catch up at all.
		filter["$or"] = []bson.M{
			{"mlsGroupId": groupID},
			{"mlsGroupId": bson.M{"$exists": false}},
			{"mlsGroupId": ""},
		}
	}
	cur, err := m.db.Collection("chatMessages").Find(
		ctx,
		filter,
		options.Find().SetSort(bson.D{
			{Key: "mlsEpoch", Value: 1},
			// "application/mls-welcome" < "application/mls-commit" is false alphabetically,
			// so sort descending on contentType to put the Welcome first.
			{Key: "contentType", Value: -1},
			// Within one epoch and one type, oldest first. Without this the order of two commits
			// that share an epoch — which is what a re-established conversation produces among its
			// untagged history — is whatever the storage engine feels like.
			{Key: "createdAt", Value: 1},
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

// SetMLSGroupInfo keeps the latest GroupInfo for the current group, in its own collection so a
// group-state read never drags the bytes along. One document per conversation.
//
// The read-then-write is deliberately not one atomic step: GroupInfo is derived and self-correcting,
// so the worst a lost race does is store a slightly older snapshot, which a joiner's compare-and-set
// refuses and refetches. Not worth a transaction.
func (m *Mongo) SetMLSGroupInfo(
	ctx context.Context, conversationID, groupID string, epoch int64, groupInfo []byte,
) error {
	state, err := m.MLSGroupState(ctx, conversationID)
	if err != nil {
		return err
	}
	if state.GroupID != groupID {
		return nil // not the current group; ignore
	}
	var existing struct {
		GroupID string `bson:"groupId"`
		Epoch   int64  `bson:"epoch"`
	}
	err = m.db.Collection("mlsGroupInfo").
		FindOne(ctx, bson.M{"_id": conversationID}).Decode(&existing)
	if err == nil && existing.GroupID == groupID && existing.Epoch >= epoch {
		return nil // already have something at least as new
	}
	_, err = m.db.Collection("mlsGroupInfo").UpdateOne(
		ctx,
		bson.M{"_id": conversationID},
		bson.M{"$set": bson.M{"groupId": groupID, "epoch": epoch, "groupInfo": groupInfo}},
		options.Update().SetUpsert(true),
	)
	return err
}

// MLSGroupInfo returns the latest GroupInfo, or ErrNotFound if none is published for the current
// group.
func (m *Mongo) MLSGroupInfo(ctx context.Context, conversationID string) (domain.MLSGroupInfo, error) {
	var doc struct {
		GroupID   string `bson:"groupId"`
		Epoch     int64  `bson:"epoch"`
		GroupInfo []byte `bson:"groupInfo"`
	}
	err := m.db.Collection("mlsGroupInfo").
		FindOne(ctx, bson.M{"_id": conversationID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) || len(doc.GroupInfo) == 0 {
		return domain.MLSGroupInfo{}, ErrNotFound
	}
	if err != nil {
		return domain.MLSGroupInfo{}, err
	}
	// Stale if the group has since been retired.
	if state, err := m.MLSGroupState(ctx, conversationID); err == nil && state.GroupID != doc.GroupID {
		return domain.MLSGroupInfo{}, ErrNotFound
	}
	return domain.MLSGroupInfo{GroupID: doc.GroupID, Epoch: doc.Epoch, GroupInfo: doc.GroupInfo}, nil
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

	// Extend the ordering chain in the same $set as the epoch advance. prevHash is
	// the current head; reading it before the CAS is race-safe because the CAS
	// filter still pins mlsEpoch == baseEpoch — if another commit advances the epoch
	// (and so the chain) between this read and the update, the filter misses and we
	// return a conflict rather than writing a link on a stale prevHash. On an
	// establish there is no prior head, so prevHash is nil.
	var prevHash []byte
	if baseEpoch != 0 {
		if cur, cerr := m.MLSGroupState(ctx, conversationID); cerr == nil {
			prevHash = cur.ChainHash
		}
	}
	newHash := mlschain.Link(prevHash, baseEpoch+1, groupID, commitCiphertext(msgs))
	msgs = stampChainHash(msgs, newHash)

	var updated domain.Conversation
	err := m.db.Collection("conversations").FindOneAndUpdate(
		ctx,
		filter,
		bson.M{"$set": bson.M{"mlsGroupId": groupID, "mlsEpoch": baseEpoch + 1, "mlsChainHash": newHash}},
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
		m.rollbackMLSGroup(ctx, conversationID, groupID, baseEpoch, prevHash)
		return domain.MLSGroupState{}, nil, err
	}
	return updated.MLS, stored, nil
}

// rollbackMLSGroup undoes a compare-and-set whose Commit could not be relayed. Best
// effort: if it fails there is nothing further to try, so it is logged by the caller's
// error rather than reported here.
func (m *Mongo) rollbackMLSGroup(ctx context.Context, conversationID, groupID string, baseEpoch int64, prevHash []byte) {
	restore := bson.M{"mlsGroupId": groupID, "mlsEpoch": baseEpoch, "mlsChainHash": prevHash}
	if baseEpoch == 0 {
		// It was an establish: there was no group before us, so there must be none after.
		restore = bson.M{"mlsGroupId": "", "mlsEpoch": int64(0), "mlsChainHash": nil}
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
				"deviceId":         backup.DeviceID,
				"salt":             backup.Salt,
				"nonce":            backup.Nonce,
				"ciphertextBlobId": backup.CiphertextBlobID,
				// Written even when empty, so re-uploading a backup WITHOUT transcripts
				// does not leave a stale transcript blob id from a previous upload behind —
				// one that would restore somebody's history as of months ago and call it
				// current.
				"transcriptSalt":   backup.TranscriptSalt,
				"transcriptNonce":  backup.TranscriptNonce,
				"transcriptBlobId": backup.TranscriptBlobID,
				"updatedAt":        backup.UpdatedAt,
			},
			"$setOnInsert": bson.M{"_id": mongoID(), "userId": backup.UserID},
		},
		options.Update().SetUpsert(true),
	)
	return err
}

// UpsertMLSDevice records or refreshes one of a user's devices. One document per (user, device);
// createdAt is set once, lastSeenAt and label move with each publish.
func (m *Mongo) UpsertMLSDevice(ctx context.Context, d domain.MLSDevice) error {
	_, err := m.db.Collection("mlsDevices").UpdateOne(
		ctx,
		bson.M{"userId": d.UserID, "deviceId": d.DeviceID},
		bson.M{
			"$set":         upsertDeviceSet(d),
			"$setOnInsert": bson.M{"_id": mongoID(), "userId": d.UserID, "deviceId": d.DeviceID, "createdAt": d.CreatedAt},
		},
		options.Update().SetUpsert(true),
	)
	return err
}

// upsertDeviceSet is the $set for a device upsert: label and last-seen always move, and the
// session id moves too WHEN one is supplied — a blank must never erase a device's known
// session, or "terminate this device" would lose the login it needs to revoke.
func upsertDeviceSet(d domain.MLSDevice) bson.M {
	set := bson.M{"label": d.Label, "lastSeenAt": d.LastSeenAt}
	if d.SessionID != "" {
		set["sessionId"] = d.SessionID
	}
	return set
}

// ListMLSDevices returns a user's devices, most recently seen first.
func (m *Mongo) ListMLSDevices(ctx context.Context, userID string) ([]domain.MLSDevice, error) {
	cur, err := m.db.Collection("mlsDevices").Find(
		ctx,
		bson.M{"userId": userID},
		options.Find().SetSort(bson.D{{Key: "lastSeenAt", Value: -1}}),
	)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := make([]domain.MLSDevice, 0)
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteMLSDevice forgets one device — a terminated one.
func (m *Mongo) DeleteMLSDevice(ctx context.Context, userID, deviceID string) error {
	_, err := m.db.Collection("mlsDevices").DeleteOne(ctx, bson.M{"userId": userID, "deviceId": deviceID})
	return err
}

// DeletePushDevicesForMLSDevice removes the push addresses belonging to one MLS device.
//
// Scoped by userId as well as the device id, so a caller cannot delete another account's push rows
// by naming a device id it does not own.
func (m *Mongo) DeletePushDevicesForMLSDevice(ctx context.Context, userID, mlsDeviceID string) (int64, error) {
	if mlsDeviceID == "" {
		// A blank id would match every legacy row for this user, which is the whole account rather
		// than the one device that was terminated.
		return 0, nil
	}
	res, err := m.db.Collection("devices").DeleteMany(ctx, bson.M{"userId": userID, "mlsDeviceId": mlsDeviceID})
	if err != nil {
		return 0, err
	}
	return res.DeletedCount, nil
}

// RevokeMLSDevice marks a device terminated, keeping the row as a tombstone.
func (m *Mongo) RevokeMLSDevice(ctx context.Context, userID, deviceID string, at time.Time) error {
	res, err := m.db.Collection("mlsDevices").UpdateOne(
		ctx,
		bson.M{"userId": userID, "deviceId": deviceID},
		bson.M{"$set": bson.M{"revokedAt": at}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokedDeviceIDs returns the terminated device ids per user.
func (m *Mongo) RevokedDeviceIDs(ctx context.Context, userIDs []string) (map[string][]string, error) {
	out := make(map[string][]string, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	cur, err := m.db.Collection("mlsDevices").Find(ctx, bson.M{
		"userId":    bson.M{"$in": userIDs},
		"revokedAt": bson.M{"$exists": true},
	})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var d domain.MLSDevice
		if err := cur.Decode(&d); err != nil {
			return nil, err
		}
		out[d.UserID] = append(out[d.UserID], d.DeviceID)
	}
	return out, cur.Err()
}

// RevokeUserTokensBefore refuses every token this user holds that was issued before cutoff.
func (m *Mongo) RevokeUserTokensBefore(ctx context.Context, userID string, cutoff, expiresAt time.Time) error {
	_, err := m.db.Collection("revokedUsers").UpdateOne(
		ctx,
		bson.M{"userId": userID},
		bson.M{"$set": bson.M{"cutoff": cutoff, "expiresAt": expiresAt}},
		options.Update().SetUpsert(true),
	)
	return err
}

// ActiveUserRevocations returns the per-user cutoffs that have not yet expired.
func (m *Mongo) ActiveUserRevocations(ctx context.Context, now time.Time) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	cur, err := m.db.Collection("revokedUsers").Find(ctx, bson.M{"expiresAt": bson.M{"$gt": now}})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var row struct {
			UserID string    `bson:"userId"`
			Cutoff time.Time `bson:"cutoff"`
		}
		if err := cur.Decode(&row); err != nil {
			return nil, err
		}
		out[row.UserID] = row.Cutoff
	}
	return out, cur.Err()
}

// MLSHistoryOffers returns the most recent history offers in a conversation, newest first.
func (m *Mongo) MLSHistoryOffers(ctx context.Context, conversationID string, limit int) ([]domain.ChatMessage, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	cur, err := m.db.Collection("chatMessages").Find(
		ctx,
		bson.M{"conversationId": conversationID, "contentType": domain.ContentTypeMLSHistoryOffer},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(int64(limit)),
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

// DeleteDevice removes one push device by id.
func (m *Mongo) DeleteDevice(ctx context.Context, deviceID string) error {
	_, err := m.db.Collection("devices").DeleteOne(ctx, bson.M{"_id": deviceID})
	return err
}

// RevokeSession records a terminated session's id in the deny list, keyed by the id so a
// repeat revoke just refreshes its expiry. The token is rejected on expiry regardless, so
// the stored expiry is only there to let entries be reaped once they can no longer matter.
func (m *Mongo) RevokeSession(ctx context.Context, sessionID string, expiresAt time.Time) error {
	_, err := m.db.Collection("revokedSessions").UpdateOne(
		ctx,
		bson.M{"sessionId": sessionID},
		bson.M{
			"$set":         bson.M{"sessionId": sessionID, "expiresAt": expiresAt},
			"$setOnInsert": bson.M{"_id": mongoID()},
		},
		options.Update().SetUpsert(true),
	)
	return err
}

// ActiveRevokedSessions returns the session ids still within their expiry as of now — the
// set the in-memory deny list hydrates from at startup. Expired entries are skipped (their
// tokens are already rejected on expiry) and left for a background reap.
func (m *Mongo) ActiveRevokedSessions(ctx context.Context, now time.Time) ([]string, error) {
	cur, err := m.db.Collection("revokedSessions").Find(ctx, bson.M{"expiresAt": bson.M{"$gt": now}})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var docs []domain.RevokedSession
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.SessionID)
	}
	return out, nil
}

func (m *Mongo) GetKeyBackup(ctx context.Context, userID string) (domain.MLSKeyBackup, error) {
	var b domain.MLSKeyBackup
	err := m.db.Collection("mlsKeyBackups").
		FindOne(ctx, bson.M{"userId": userID}).Decode(&b)
	return b, mapErr(err)
}

// ResetMLSGroup retires the current group so a new one can be established. See the Store
// interface: the old group is remembered, not deleted, so nothing anyone still holds is lost.
//
// Conditional on a group actually being established, so two clients that both notice the
// conversation is stuck cannot retire two groups between them.
func (m *Mongo) ResetMLSGroup(ctx context.Context, conversationID string) (domain.MLSGroupState, error) {
	current, err := m.MLSGroupState(ctx, conversationID)
	if err != nil {
		return domain.MLSGroupState{}, err
	}
	if current.GroupID == "" {
		return current, nil
	}

	var updated domain.Conversation
	err = m.db.Collection("conversations").FindOneAndUpdate(
		ctx,
		bson.M{"_id": conversationID, "mlsGroupId": current.GroupID},
		bson.M{
			"$set":  bson.M{"mlsGroupId": "", "mlsEpoch": int64(0)},
			"$push": bson.M{"mlsPriorGroupIds": bson.M{"$each": []string{current.GroupID}, "$position": 0, "$slice": maxPriorGroups}},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updated)
	if err != nil {
		if mapErr(err) == ErrNotFound {
			// Somebody else retired it first. Their reset is as good as ours.
			return m.MLSGroupState(ctx, conversationID)
		}
		return domain.MLSGroupState{}, mapErr(err)
	}
	return updated.MLS, nil
}
