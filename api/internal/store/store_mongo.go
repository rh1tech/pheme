package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/domain"
)

// Mongo is a MongoDB-backed Store. Entity IDs are application-generated hex
// strings stored as the document _id.
type Mongo struct {
	client *mongo.Client
	db     *mongo.Database
	blobs  blob.Store
}

// NewMongo connects to MongoDB, verifies connectivity, and ensures indexes. The
// blob store (may be nil) is used to remove a deleted message's images during
// cascade deletes.
func NewMongo(ctx context.Context, uri, dbName string, blobs blob.Store) (*Mongo, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		return nil, err
	}
	m := &Mongo{client: client, db: client.Database(dbName), blobs: blobs}
	// Migrations FIRST, indexes second. The order matters now that an index enforces an invariant
	// rather than only speeding a query up: a unique index refuses to build while a violation
	// exists, so a deployment carrying old bad rows would fail to start — which is how a fix becomes
	// an outage, and on a self-hosted product it is somebody else's outage. Clean, then enforce.
	if err := m.migrate(ctx); err != nil {
		_ = client.Disconnect(ctx)
		return nil, err
	}
	if err := m.ensureIndexes(ctx); err != nil {
		_ = client.Disconnect(ctx)
		return nil, err
	}
	return m, nil
}

// migrate runs idempotent, additive data backfills on startup.
func (m *Mongo) migrate(ctx context.Context) error {
	// The per-message comments flag is new: messages created before it existed
	// have no `commentsAllowed` field and would otherwise decode to false,
	// silently disabling comments on all history. Backfill them to true (the
	// field's intended default). Idempotent: matches only documents missing it.
	if _, err := m.db.Collection("messages").UpdateMany(ctx,
		bson.M{"commentsAllowed": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"commentsAllowed": true}},
	); err != nil {
		return err
	}
	// One push address, one account — see releasePushAddress. Existing deployments carry rows from
	// before that was enforced, and those rows are not merely untidy: the server rings a handset for
	// whichever accounts claim it, so somebody who signed out of one account and into another on the
	// same phone gets the previous account's calls. This clears them, and is what lets the unique
	// index below be built at all.
	if err := m.releaseSharedPushAddresses(ctx); err != nil {
		return err
	}
	return nil
}

// releaseSharedPushAddresses drops device rows whose push address is also held by a different
// account, keeping the most recently created — the handset's current owner.
//
// Idempotent, and after the first run it matches nothing, so it costs one aggregation on startup.
func (m *Mongo) releaseSharedPushAddresses(ctx context.Context) error {
	for _, field := range []string{"fcmToken", "webPushEndpoint"} {
		pipeline := mongo.Pipeline{
			{{Key: "$match", Value: bson.M{field: bson.M{"$exists": true, "$ne": ""}}}},
			{{Key: "$sort", Value: bson.D{{Key: "createdAt", Value: -1}}}},
			{{Key: "$group", Value: bson.M{
				"_id":   "$" + field,
				"ids":   bson.M{"$push": "$_id"},
				"users": bson.M{"$addToSet": "$userId"},
			}}},
			// Only addresses claimed by more than one account. A device legitimately re-registering
			// under the SAME account is the dedupe's business, not this one's.
			{{Key: "$match", Value: bson.M{"users.1": bson.M{"$exists": true}}}},
		}
		cursor, err := m.db.Collection("devices").Aggregate(ctx, pipeline)
		if err != nil {
			return err
		}
		var groups []struct {
			IDs []string `bson:"ids"`
		}
		if err := cursor.All(ctx, &groups); err != nil {
			return err
		}
		for _, g := range groups {
			if len(g.IDs) < 2 {
				continue
			}
			// Sorted newest-first above, so everything after the first is a previous owner.
			if _, err := m.db.Collection("devices").
				DeleteMany(ctx, bson.M{"_id": bson.M{"$in": g.IDs[1:]}}); err != nil {
				return err
			}
		}
	}
	return nil
}

func mongoID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func (m *Mongo) ensureIndexes(ctx context.Context) error {
	specs := map[string][]mongo.IndexModel{
		"users":    {{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)}, {Keys: bson.D{{Key: "usernameLower", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"usernameLower": bson.M{"$exists": true}})}},
		"channels": {{Keys: bson.D{{Key: "publicId", Value: 1}}, Options: options.Index().SetUnique(true)}, {Keys: bson.D{{Key: "ownerId", Value: 1}}}, {Keys: bson.D{{Key: "aliasLower", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"aliasLower": bson.M{"$exists": true}})}},
		"apiKeys":  {{Keys: bson.D{{Key: "channelId", Value: 1}}}},
		// Superseded backups, newest first per user — the read every restore fallback makes.
		"mlsKeyBackupVersions": {{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "updatedAt", Value: -1}}}},
		// Registration looks an invite up BY the hash of the code presented, so this index is
		// on the read path of every signup. Unique because two invites sharing a hash would be
		// two invites sharing a code, and only one of them could ever be redeemed.
		"invites": {{Keys: bson.D{{Key: "codeHash", Value: 1}}, Options: options.Index().SetUnique(true)}, {Keys: bson.D{{Key: "createdAt", Value: -1}}}},
		"devices": {
			{Keys: bson.D{{Key: "userId", Value: 1}}},
			{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "webPushEndpoint", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"webPushEndpoint": bson.M{"$exists": true}})},
			// The mobile half of the same idea. NOT unique: two rows already exist in the wild
			// from before dedupe (that is the bug this indexes for), and a unique index would
			// refuse to build until they were cleaned up — turning a fix into an outage.
			{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "fcmToken", Value: 1}}, Options: options.Index().SetPartialFilterExpression(bson.M{"fcmToken": bson.M{"$exists": true}})},
			// The token WITHOUT the user, which is how CreateDevice finds rows holding this
			// handset's address under somebody else — see releasePushAddress. A handset that
			// changes hands is otherwise invisible to every index here, because they all lead
			// with userId, and the one query that has to cross accounts would scan.
			//
			// UNIQUE, and that is the point: it makes "one handset, one account" a property of the
			// database rather than of remembering to call releasePushAddress. Three separate code
			// paths had to agree to keep that true, and the one that forgot rang the wrong person's
			// phone for five hours.
			//
			// Safe to build because migrate() runs FIRST and has already cleared any address held by
			// two accounts — see NewMongo. Without that ordering this index would refuse to build on
			// exactly the deployments carrying the bug, and refuse to start the API with it.
			{Keys: bson.D{{Key: "fcmToken", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"fcmToken": bson.M{"$exists": true}})},
			{Keys: bson.D{{Key: "webPushEndpoint", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"webPushEndpoint": bson.M{"$exists": true}})},
		},
		"subscriptions":  {{Keys: bson.D{{Key: "channelId", Value: 1}, {Key: "status", Value: 1}}}, {Keys: bson.D{{Key: "deviceId", Value: 1}}}},
		"channelMembers": {{Keys: bson.D{{Key: "channelId", Value: 1}, {Key: "userId", Value: 1}}, Options: options.Index().SetUnique(true)}, {Keys: bson.D{{Key: "channelId", Value: 1}, {Key: "status", Value: 1}}}, {Keys: bson.D{{Key: "userId", Value: 1}}}},
		// A peer host is tracked once per channel; the upsert in AddRemoteSubscription relies on this.
		"remoteSubscriptions": {{Keys: bson.D{{Key: "channelId", Value: 1}, {Key: "peerDomain", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"messages":            {{Keys: bson.D{{Key: "channelId", Value: 1}, {Key: "createdAt", Value: -1}}}},
		"deliveries":          {{Keys: bson.D{{Key: "messageId", Value: 1}}}},
		"comments":            {{Keys: bson.D{{Key: "messageId", Value: 1}, {Key: "createdAt", Value: -1}}}, {Keys: bson.D{{Key: "channelId", Value: 1}}}, {Keys: bson.D{{Key: "userId", Value: 1}}}, {Keys: bson.D{{Key: "createdAt", Value: -1}}}},
		// Conversations: a unique partial index on directKey enforces one direct
		// chat per user pair; chatMessages sorts by (conversationId, createdAt desc)
		// like messages does by channel.
		"conversations":       {{Keys: bson.D{{Key: "directKey", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"directKey": bson.M{"$exists": true}})}},
		"conversationMembers": {{Keys: bson.D{{Key: "conversationId", Value: 1}, {Key: "userId", Value: 1}}, Options: options.Index().SetUnique(true)}, {Keys: bson.D{{Key: "userId", Value: 1}}}},
		"chatMessages":        {{Keys: bson.D{{Key: "conversationId", Value: 1}, {Key: "createdAt", Value: -1}}}},
		// The partial unique index is what actually enforces one last-resort package per
		// device: the handler's check-then-insert would otherwise let two concurrent
		// publishes (two tabs replenishing at once) both slip through.
		"mlsKeyPackages": {
			{Keys: bson.D{{Key: "userId", Value: 1}}},
			{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "deviceId", Value: 1}}},
			{
				Keys: bson.D{{Key: "userId", Value: 1}, {Key: "deviceId", Value: 1}, {Key: "lastResort", Value: 1}},
				Options: options.Index().SetUnique(true).
					SetPartialFilterExpression(bson.M{"lastResort": true}),
			},
		},
		"mlsKeyBackups": {{Keys: bson.D{{Key: "userId", Value: 1}}, Options: options.Index().SetUnique(true)}},
	}
	for coll, models := range specs {
		if _, err := m.db.Collection(coll).Indexes().CreateMany(ctx, models); err != nil {
			return err
		}
	}
	return nil
}

func mapErr(err error) error {
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ErrNotFound
	}
	return err
}

func (m *Mongo) CreateUser(ctx context.Context, u domain.User) (domain.User, error) {
	if u.ID == "" {
		u.ID = mongoID()
	}
	u = u.WithNewUserDefaults()
	_, err := m.db.Collection("users").InsertOne(ctx, u)
	return u, err
}

func (m *Mongo) UserByID(ctx context.Context, id string) (domain.User, error) {
	var u domain.User
	err := m.db.Collection("users").FindOne(ctx, bson.M{"_id": id}).Decode(&u)
	return u, mapErr(err)
}

func (m *Mongo) UserByEmail(ctx context.Context, email string) (domain.User, error) {
	var u domain.User
	err := m.db.Collection("users").FindOne(ctx, bson.M{"email": email}).Decode(&u)
	return u, mapErr(err)
}

func (m *Mongo) UserByUsername(ctx context.Context, usernameLower string) (domain.User, error) {
	if usernameLower == "" {
		return domain.User{}, ErrNotFound
	}
	var u domain.User
	err := m.db.Collection("users").FindOne(ctx, bson.M{"usernameLower": usernameLower}).Decode(&u)
	return u, mapErr(err)
}

func (m *Mongo) UsersByIDs(ctx context.Context, ids []string) (map[string]domain.User, error) {
	out := map[string]domain.User{}
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := m.db.Collection("users").Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	var users []domain.User
	if err := cur.All(ctx, &users); err != nil {
		return nil, err
	}
	for _, u := range users {
		out[u.ID] = u
	}
	return out, nil
}

func (m *Mongo) UpdateUserProfile(ctx context.Context, userID string, p domain.UserProfileUpdate) (domain.User, error) {
	// nil means "leave it alone" for every field: see domain.UserProfileUpdate.
	trimmed := ""
	if p.Username != nil {
		trimmed = strings.TrimSpace(*p.Username)
	}
	lower := strings.ToLower(trimmed)
	if lower != "" {
		// Clean ErrUsernameTaken instead of a raw duplicate-key error; the unique
		// partial index is the real backstop.
		var clash domain.User
		err := m.db.Collection("users").
			FindOne(ctx, bson.M{"usernameLower": lower, "_id": bson.M{"$ne": userID}}).Decode(&clash)
		if err == nil {
			return domain.User{}, ErrUsernameTaken
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return domain.User{}, err
		}
	}
	set := bson.M{}
	for field, value := range map[string]*string{
		"displayName": p.DisplayName,
		"bio":         p.Bio,
		"phone":       p.Phone,
		"website":     p.Website,
	} {
		if value != nil {
			set[field] = strings.TrimSpace(*value)
		}
	}
	update := bson.M{"$set": set}
	unset := bson.M{}
	// Only touch the username if the caller said something about it. Clearing it is still possible
	// — that is a non-nil empty string — but no longer the accidental consequence of not mentioning
	// it.
	if p.Username != nil {
		if lower == "" {
			unset["username"] = ""
			unset["usernameLower"] = ""
		} else {
			set["username"] = trimmed
			set["usernameLower"] = lower
		}
	}
	// nil means "not supplied, leave it": see UserProfileUpdate. Always written EXPLICITLY,
	// never unset back to empty — an absent field means "this account predates the setting"
	// and must keep meaning only that. See domain.NotificationPrivacy.Effective.
	if p.NotificationPrivacy != nil {
		set["notificationPrivacy"] = string(*p.NotificationPrivacy)
	}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	// An update that changes nothing is a valid request — a client may send only the fields it
	// knows about — but Mongo rejects an empty $set outright.
	if len(set) == 0 {
		delete(update, "$set")
	}
	if len(update) == 0 {
		return m.UserByID(ctx, userID)
	}
	res, err := m.db.Collection("users").UpdateOne(ctx, bson.M{"_id": userID}, update)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return domain.User{}, ErrUsernameTaken
		}
		return domain.User{}, err
	}
	if res.MatchedCount == 0 {
		return domain.User{}, ErrNotFound
	}
	return m.UserByID(ctx, userID)
}

func (m *Mongo) SetUserAvatar(ctx context.Context, userID, avatarID string) (domain.User, error) {
	prev, err := m.UserByID(ctx, userID)
	if err != nil {
		return domain.User{}, err
	}
	var update bson.M
	if avatarID == "" {
		update = bson.M{"$unset": bson.M{"avatarId": ""}}
	} else {
		update = bson.M{"$set": bson.M{"avatarId": avatarID}}
	}
	if _, err := m.db.Collection("users").UpdateOne(ctx, bson.M{"_id": userID}, update); err != nil {
		return domain.User{}, err
	}
	if prev.AvatarID != "" && prev.AvatarID != avatarID {
		deleteBlobs(ctx, m.blobs, []string{prev.AvatarID})
	}
	return m.UserByID(ctx, userID)
}

func (m *Mongo) SetChannelAvatar(ctx context.Context, channelID, avatarID string) (domain.Channel, error) {
	prev, err := m.ChannelByID(ctx, channelID)
	if err != nil {
		return domain.Channel{}, err
	}
	var update bson.M
	if avatarID == "" {
		update = bson.M{"$unset": bson.M{"avatarId": ""}}
	} else {
		update = bson.M{"$set": bson.M{"avatarId": avatarID}}
	}
	if _, err := m.db.Collection("channels").UpdateOne(ctx, bson.M{"_id": channelID}, update); err != nil {
		return domain.Channel{}, err
	}
	if prev.AvatarID != "" && prev.AvatarID != avatarID {
		deleteBlobs(ctx, m.blobs, []string{prev.AvatarID})
	}
	return m.ChannelByID(ctx, channelID)
}

func (m *Mongo) UpdateUserRole(ctx context.Context, userID string, role domain.Role) error {
	res, err := m.db.Collection("users").UpdateOne(ctx, bson.M{"_id": userID}, bson.M{"$set": bson.M{"role": role}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *Mongo) UpdateUserStatus(ctx context.Context, userID string, status domain.UserStatus) error {
	res, err := m.db.Collection("users").UpdateOne(ctx, bson.M{"_id": userID}, bson.M{"$set": bson.M{"status": status}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *Mongo) UpdateUserPassword(ctx context.Context, userID, passwordHash string) error {
	res, err := m.db.Collection("users").UpdateOne(ctx, bson.M{"_id": userID}, bson.M{"$set": bson.M{"passwordHash": passwordHash}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *Mongo) ListUsers(ctx context.Context) ([]domain.User, error) {
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}})
	cur, err := m.db.Collection("users").Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, err
	}
	var out []domain.User
	return out, cur.All(ctx, &out)
}

func (m *Mongo) AdminListUsers(ctx context.Context, query string, offset, limit int) ([]domain.User, int64, error) {
	filter := bson.M{}
	if q := strings.TrimSpace(query); q != "" {
		filter["email"] = primitive.Regex{Pattern: regexp.QuoteMeta(q), Options: "i"}
	}
	return findPaged[domain.User](ctx, m.db.Collection("users"), filter, offset, limit)
}

func (m *Mongo) SearchUsers(ctx context.Context, query string, limit int) ([]domain.User, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	rx := primitive.Regex{Pattern: regexp.QuoteMeta(q), Options: "i"}
	filter := bson.M{
		"status": domain.UserActive,
		"$or":    bson.A{bson.M{"username": rx}, bson.M{"displayName": rx}},
	}
	opts := options.Find().SetSort(bson.D{{Key: "username", Value: 1}})
	if limit > 0 {
		opts.SetLimit(int64(limit))
	}
	cur, err := m.db.Collection("users").Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	var out []domain.User
	return out, cur.All(ctx, &out)
}

func (m *Mongo) DeleteUser(ctx context.Context, userID string) error {
	// Capture the avatar blob id before deletion so it can be reclaimed after.
	prev, err := m.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	// Cascade: delete the user's channels (and their dependents), then devices
	// and their subscriptions, then the user.
	channels, err := m.ChannelsByOwner(ctx, userID)
	if err != nil {
		return err
	}
	for _, c := range channels {
		if err := m.DeleteChannel(ctx, c.ID); err != nil {
			return err
		}
	}

	cur, err := m.db.Collection("devices").Find(ctx, bson.M{"userId": userID})
	if err != nil {
		return err
	}
	var devices []domain.Device
	if err := cur.All(ctx, &devices); err != nil {
		return err
	}
	if len(devices) > 0 {
		ids := make([]string, 0, len(devices))
		for _, d := range devices {
			ids = append(ids, d.ID)
		}
		if _, err := m.db.Collection("subscriptions").DeleteMany(ctx, bson.M{"deviceId": bson.M{"$in": ids}}); err != nil {
			return err
		}
		if _, err := m.db.Collection("devices").DeleteMany(ctx, bson.M{"userId": userID}); err != nil {
			return err
		}
	}

	// Remove the user's memberships across all channels (not just owned ones).
	if _, err := m.db.Collection("channelMembers").DeleteMany(ctx, bson.M{"userId": userID}); err != nil {
		return err
	}

	// Remove the user's comments across all channels.
	if _, err := m.db.Collection("comments").DeleteMany(ctx, bson.M{"userId": userID}); err != nil {
		return err
	}

	res, err := m.db.Collection("users").DeleteOne(ctx, bson.M{"_id": userID})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	if prev.AvatarID != "" {
		deleteBlobs(ctx, m.blobs, []string{prev.AvatarID})
	}
	return nil
}

func (m *Mongo) CreateChannel(ctx context.Context, c domain.Channel) (domain.Channel, error) {
	if c.ID == "" {
		c.ID = mongoID()
	}
	_, err := m.db.Collection("channels").InsertOne(ctx, c)
	return c, err
}

func (m *Mongo) ChannelByID(ctx context.Context, id string) (domain.Channel, error) {
	var c domain.Channel
	err := m.db.Collection("channels").FindOne(ctx, bson.M{"_id": id}).Decode(&c)
	return c, mapErr(err)
}

func (m *Mongo) ChannelByPublicID(ctx context.Context, publicID string) (domain.Channel, error) {
	var c domain.Channel
	err := m.db.Collection("channels").FindOne(ctx, bson.M{"publicId": publicID}).Decode(&c)
	return c, mapErr(err)
}

func (m *Mongo) ChannelByAlias(ctx context.Context, aliasLower string) (domain.Channel, error) {
	if aliasLower == "" {
		return domain.Channel{}, ErrNotFound
	}
	var c domain.Channel
	err := m.db.Collection("channels").FindOne(ctx, bson.M{"aliasLower": aliasLower}).Decode(&c)
	return c, mapErr(err)
}

func (m *Mongo) ChannelByOriginPublicID(ctx context.Context, originDomain, originPublicID string) (domain.Channel, error) {
	var c domain.Channel
	err := m.db.Collection("channels").
		FindOne(ctx, bson.M{"originDomain": originDomain, "originPublicId": originPublicID}).Decode(&c)
	return c, mapErr(err)
}

func (m *Mongo) AddRemoteSubscription(ctx context.Context, channelID, peerDomain string) error {
	// Upsert on the pair, so a host is tracked exactly once however many of its
	// users subscribe. The unique index on (channelId, peerDomain) is the real
	// backstop; the upsert makes a repeat a no-op rather than an error.
	_, err := m.db.Collection("remoteSubscriptions").UpdateOne(ctx,
		bson.M{"channelId": channelID, "peerDomain": peerDomain},
		bson.M{"$setOnInsert": bson.M{
			"channelId":  channelID,
			"peerDomain": peerDomain,
			"createdAt":  time.Now().UTC(),
		}},
		options.Update().SetUpsert(true),
	)
	return err
}

func (m *Mongo) RemoteSubscriberHosts(ctx context.Context, channelID string) ([]string, error) {
	cur, err := m.db.Collection("remoteSubscriptions").Find(ctx, bson.M{"channelId": channelID})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []string
	for cur.Next(ctx) {
		var rs domain.RemoteSubscription
		if err := cur.Decode(&rs); err != nil {
			return nil, err
		}
		out = append(out, rs.PeerDomain)
	}
	return out, cur.Err()
}

func (m *Mongo) SetChannelAlias(ctx context.Context, channelID, alias string) (domain.Channel, error) {
	trimmed := strings.TrimSpace(alias)
	lower := strings.ToLower(trimmed)
	if lower != "" {
		// Guard against another channel already holding the alias. The unique
		// partial index is the real backstop; this gives a clean ErrAliasTaken
		// instead of a raw duplicate-key error.
		var clash domain.Channel
		err := m.db.Collection("channels").
			FindOne(ctx, bson.M{"aliasLower": lower, "_id": bson.M{"$ne": channelID}}).Decode(&clash)
		if err == nil {
			return domain.Channel{}, ErrAliasTaken
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return domain.Channel{}, err
		}
	}
	var update bson.M
	if lower == "" {
		update = bson.M{"$unset": bson.M{"alias": "", "aliasLower": ""}}
	} else {
		update = bson.M{"$set": bson.M{"alias": trimmed, "aliasLower": lower}}
	}
	res, err := m.db.Collection("channels").UpdateOne(ctx, bson.M{"_id": channelID}, update)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return domain.Channel{}, ErrAliasTaken
		}
		return domain.Channel{}, err
	}
	if res.MatchedCount == 0 {
		return domain.Channel{}, ErrNotFound
	}
	return m.ChannelByID(ctx, channelID)
}

func (m *Mongo) ChannelsByOwner(ctx context.Context, ownerID string) ([]domain.Channel, error) {
	cur, err := m.db.Collection("channels").Find(ctx, bson.M{"ownerId": ownerID})
	if err != nil {
		return nil, err
	}
	var out []domain.Channel
	return out, cur.All(ctx, &out)
}

func (m *Mongo) UpdateChannel(ctx context.Context, id, name string, mode domain.SubscriptionMode) (domain.Channel, error) {
	set := bson.M{}
	if name != "" {
		set["name"] = name
	}
	if mode != "" {
		set["subscriptionMode"] = mode
	}
	if len(set) > 0 {
		if _, err := m.db.Collection("channels").UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": set}); err != nil {
			return domain.Channel{}, err
		}
	}
	return m.ChannelByID(ctx, id)
}

func (m *Mongo) UpdateChannelStatus(ctx context.Context, id string, status domain.ChannelStatus) (domain.Channel, error) {
	res, err := m.db.Collection("channels").UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"status": status}})
	if err != nil {
		return domain.Channel{}, err
	}
	if res.MatchedCount == 0 {
		return domain.Channel{}, ErrNotFound
	}
	return m.ChannelByID(ctx, id)
}

func (m *Mongo) DeleteChannel(ctx context.Context, id string) error {
	// Collect message IDs first so dependent deliveries can be removed.
	cur, err := m.db.Collection("messages").Find(ctx, bson.M{"channelId": id})
	if err != nil {
		return err
	}
	var msgs []domain.Message
	if err := cur.All(ctx, &msgs); err != nil {
		return err
	}
	var imageIDs []string
	// The channel's own avatar is a blob like any message image, and would leak
	// if it were not swept up with them.
	if prev, err := m.ChannelByID(ctx, id); err == nil && prev.AvatarID != "" {
		imageIDs = append(imageIDs, prev.AvatarID)
	}
	if len(msgs) > 0 {
		ids := make([]string, 0, len(msgs))
		for _, msg := range msgs {
			ids = append(ids, msg.ID)
			for _, img := range msg.Images {
				imageIDs = append(imageIDs, img.ID)
			}
		}
		if _, err := m.db.Collection("deliveries").DeleteMany(ctx, bson.M{"messageId": bson.M{"$in": ids}}); err != nil {
			return err
		}
	}
	for _, coll := range []string{"messages", "subscriptions", "apiKeys", "channelMembers", "comments"} {
		if _, err := m.db.Collection(coll).DeleteMany(ctx, bson.M{"channelId": id}); err != nil {
			return err
		}
	}
	res, err := m.db.Collection("channels").DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	deleteBlobs(ctx, m.blobs, imageIDs)
	return nil
}

func (m *Mongo) CreateAPIKey(ctx context.Context, k domain.APIKey) (domain.APIKey, error) {
	if k.ID == "" {
		k.ID = mongoID()
	}
	_, err := m.db.Collection("apiKeys").InsertOne(ctx, k)
	return k, err
}

func (m *Mongo) APIKeyByID(ctx context.Context, keyID string) (domain.APIKey, error) {
	var k domain.APIKey
	err := m.db.Collection("apiKeys").FindOne(ctx, bson.M{"_id": keyID}).Decode(&k)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return domain.APIKey{}, ErrNotFound
	}
	return k, err
}

func (m *Mongo) APIKeysByChannel(ctx context.Context, channelID string) ([]domain.APIKey, error) {
	cur, err := m.db.Collection("apiKeys").Find(ctx, bson.M{"channelId": channelID})
	if err != nil {
		return nil, err
	}
	var out []domain.APIKey
	return out, cur.All(ctx, &out)
}

func (m *Mongo) RevokeAPIKey(ctx context.Context, keyID string) error {
	now := time.Now().UTC()
	res, err := m.db.Collection("apiKeys").UpdateOne(ctx,
		bson.M{"_id": keyID, "revokedAt": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"revokedAt": now}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		// Either the key does not exist or it is already revoked; treat a missing
		// key as not found, an already-revoked key as success.
		count, cerr := m.db.Collection("apiKeys").CountDocuments(ctx, bson.M{"_id": keyID})
		if cerr != nil {
			return cerr
		}
		if count == 0 {
			return ErrNotFound
		}
	}
	return nil
}

func (m *Mongo) CreateInvite(ctx context.Context, i domain.Invite) (domain.Invite, error) {
	if i.ID == "" {
		i.ID = mongoID()
	}
	_, err := m.db.Collection("invites").InsertOne(ctx, i)
	return i, err
}

func (m *Mongo) InviteByCodeHash(ctx context.Context, codeHash string) (domain.Invite, error) {
	if codeHash == "" {
		return domain.Invite{}, ErrNotFound
	}
	var i domain.Invite
	err := m.db.Collection("invites").FindOne(ctx, bson.M{"codeHash": codeHash}).Decode(&i)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return domain.Invite{}, ErrNotFound
	}
	return i, err
}

func (m *Mongo) InviteByID(ctx context.Context, id string) (domain.Invite, error) {
	var i domain.Invite
	err := m.db.Collection("invites").FindOne(ctx, bson.M{"_id": id}).Decode(&i)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return domain.Invite{}, ErrNotFound
	}
	return i, err
}

func (m *Mongo) AdminListInvites(ctx context.Context, query string, offset, limit int) ([]domain.Invite, int64, error) {
	filter := bson.M{}
	if q := strings.TrimSpace(query); q != "" {
		rx := primitive.Regex{Pattern: regexp.QuoteMeta(q), Options: "i"}
		filter["$or"] = []bson.M{{"note": rx}, {"prefix": rx}}
	}
	return findPaged[domain.Invite](ctx, m.db.Collection("invites"), filter, offset, limit)
}

// ConsumeInvite spends an invite in ONE conditional update. The filter carries the whole
// redeemability rule — unused, unrevoked, unexpired — so the database, not the handler,
// decides which of two simultaneous redemptions wins. A read-then-write here would let both
// callers see a pending invite and both create an account from it.
func (m *Mongo) ConsumeInvite(ctx context.Context, id, userID string, now time.Time) error {
	at := now.UTC()
	res, err := m.db.Collection("invites").UpdateOne(ctx,
		bson.M{
			"_id":       id,
			"usedAt":    bson.M{"$exists": false},
			"revokedAt": bson.M{"$exists": false},
			"$or": []bson.M{
				{"expiresAt": bson.M{"$exists": false}},
				{"expiresAt": bson.M{"$gt": at}},
			},
		},
		bson.M{"$set": bson.M{"usedAt": at, "usedBy": userID}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		count, cerr := m.db.Collection("invites").CountDocuments(ctx, bson.M{"_id": id})
		if cerr != nil {
			return cerr
		}
		if count == 0 {
			return ErrNotFound
		}
		return ErrInviteSpent
	}
	return nil
}

func (m *Mongo) ReleaseInvite(ctx context.Context, id string) error {
	res, err := m.db.Collection("invites").UpdateOne(ctx,
		bson.M{"_id": id},
		bson.M{"$unset": bson.M{"usedAt": "", "usedBy": ""}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *Mongo) RevokeInvite(ctx context.Context, id string, now time.Time) error {
	res, err := m.db.Collection("invites").UpdateOne(ctx,
		bson.M{"_id": id, "revokedAt": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"revokedAt": now.UTC()}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		// Missing is not found; already revoked is success — the caller asked for a state
		// the row is already in.
		count, cerr := m.db.Collection("invites").CountDocuments(ctx, bson.M{"_id": id})
		if cerr != nil {
			return cerr
		}
		if count == 0 {
			return ErrNotFound
		}
	}
	return nil
}

func (m *Mongo) CreateDevice(ctx context.Context, d domain.Device) (domain.Device, error) {
	// A push address belongs to ONE account at a time — whoever is signed in on that handset now.
	//
	// Sign out of one account on a phone and into another, and the old row survived: the dedupe
	// below is scoped by userId, so the new registration became a SECOND row and the first one was
	// left pointing at the same physical device. The server then rang that phone for the previous
	// account's calls. Signed in as one user, calling another, the caller's own phone rang — because
	// it was still, as far as the directory was concerned, the callee's device.
	//
	// So the address is claimed here before it is registered. Deliberately not scoped to the
	// platform or to the MLS device: it is the token that names the handset, and any row holding it
	// under a different user is stale by definition.
	if err := m.releasePushAddress(ctx, d); err != nil {
		return domain.Device{}, err
	}

	// A device IS its push address. Registering the same address again updates the row rather than
	// adding another, because two rows for one phone means the fan-out sends to it twice and the
	// user gets every message twice — which is exactly what happened: mobile had no dedupe at all,
	// so every app start left another row behind, and only web (keyed on its endpoint) was safe.
	if filter := devicePushIdentity(d); filter != nil {
		var existing domain.Device
		err := m.db.Collection("devices").FindOne(ctx, filter).Decode(&existing)
		if err == nil {
			// Everything that legitimately CHANGES for a device that is otherwise the same one:
			// a refreshed subscription or token, and the capability its current build reports.
			// Pinning any of these to first-registration values is how a device ends up
			// permanently described by the app version it was first seen with.
			set := bson.M{
				"lastSeenAt":       d.LastSeenAt,
				"canRenderPreview": d.CanRenderPreview,
			}
			// The MLS device this address belongs to, when the registration carries one.
			//
			// This was missing, and it made the omission permanent. A client registers when the app
			// starts, which can be before it has minted its MLS identity, so the first registration
			// legitimately has none. Every later one does — but every later one also matches this
			// dedupe, and the update dropped the field, so the row kept its empty value forever.
			//
			// That is not cosmetic: an address with no MLS device cannot be removed when that device
			// is revoked, and is therefore refused message previews. The device would show "New
			// message" for the rest of its life with no way to recover, having done nothing wrong
			// and having sent the right value on every launch.
			//
			// Only when non-empty, so a registration that genuinely has no identity — a Mac, a
			// client that has not minted one yet — cannot erase a link that already exists.
			if d.MLSDeviceID != "" {
				set["mlsDeviceId"] = d.MLSDeviceID
			}
			if d.WebPushSub != "" {
				set["webPushSub"] = d.WebPushSub
			}
			if d.FCMToken != "" {
				set["fcmToken"] = d.FCMToken
			}
			if d.VoIPToken != "" {
				set["voipToken"] = d.VoIPToken
			}
			if _, uerr := m.db.Collection("devices").
				UpdateOne(ctx, bson.M{"_id": existing.ID}, bson.M{"$set": set}); uerr != nil {
				return domain.Device{}, uerr
			}
			existing.LastSeenAt = d.LastSeenAt
			existing.CanRenderPreview = d.CanRenderPreview
			if d.MLSDeviceID != "" {
				existing.MLSDeviceID = d.MLSDeviceID
			}
			if d.WebPushSub != "" {
				existing.WebPushSub = d.WebPushSub
			}
			if d.FCMToken != "" {
				existing.FCMToken = d.FCMToken
			}
			if d.VoIPToken != "" {
				existing.VoIPToken = d.VoIPToken
			}
			return existing, nil
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return domain.Device{}, err
		}
		if endpoint := webPushEndpoint(d.WebPushSub); endpoint != "" {
			d.WebPushEndpoint = endpoint
		}
	}
	if d.ID == "" {
		d.ID = mongoID()
	}
	if _, err := m.db.Collection("devices").InsertOne(ctx, d); err != nil {
		// The address index is unique now, so two registrations racing for the same handset can
		// reach here together and one loses. That is not an error worth showing anybody: the row
		// the winner wrote is the row this caller wanted. Re-read it rather than failing a
		// registration over a race the user cannot see or avoid.
		if mongo.IsDuplicateKeyError(err) {
			if filter := devicePushIdentity(d); filter != nil {
				var winner domain.Device
				if ferr := m.db.Collection("devices").FindOne(ctx, filter).Decode(&winner); ferr == nil {
					return winner, nil
				}
			}
		}
		return domain.Device{}, err
	}
	return d, nil
}

// releasePushAddress drops any device row holding this registration's push address under a
// DIFFERENT user, so one handset cannot be two people's device at once.
func (m *Mongo) releasePushAddress(ctx context.Context, d domain.Device) error {
	if d.UserID == "" {
		return nil
	}
	address := pushAddress(d)
	if address == nil {
		return nil
	}
	address["userId"] = bson.M{"$ne": d.UserID}
	_, err := m.db.Collection("devices").DeleteMany(ctx, address)
	return err
}

// pushAddress is the part of devicePushIdentity that names the HANDSET rather than the account: the
// web push endpoint, or the FCM token. Nil when a registration carries neither, which is a device
// the directory cannot address and therefore cannot confuse with another.
func pushAddress(d domain.Device) bson.M {
	if endpoint := webPushEndpoint(d.WebPushSub); endpoint != "" {
		return bson.M{"webPushEndpoint": endpoint}
	}
	if d.FCMToken != "" {
		return bson.M{"fcmToken": d.FCMToken}
	}
	return nil
}

// devicePushIdentity is the query that finds an already-registered device, or nil when this
// registration carries no push address to recognise it by.
//
// A device with NO push address (a Mac, a browser that declined notifications) is deliberately
// never matched: it has nothing unique to match on, and collapsing them would merge distinct
// devices into one — and the device id is what the call answer-lock is keyed on.
func devicePushIdentity(d domain.Device) bson.M {
	if endpoint := webPushEndpoint(d.WebPushSub); endpoint != "" {
		return bson.M{"userId": d.UserID, "webPushEndpoint": endpoint}
	}
	if d.FCMToken != "" {
		return bson.M{"userId": d.UserID, "fcmToken": d.FCMToken}
	}
	return nil
}

func (m *Mongo) Subscribe(ctx context.Context, s domain.Subscription) (domain.Subscription, error) {
	// Upsert on (channelId, deviceId) so a device has at most one subscription
	// per channel.
	filter := bson.M{"channelId": s.ChannelID, "deviceId": s.DeviceID}
	var existing domain.Subscription
	err := m.db.Collection("subscriptions").FindOne(ctx, filter).Decode(&existing)
	if err == nil {
		if _, uerr := m.db.Collection("subscriptions").UpdateOne(ctx, filter,
			bson.M{"$set": bson.M{"status": s.Status}}); uerr != nil {
			return domain.Subscription{}, uerr
		}
		existing.Status = s.Status
		return existing, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return domain.Subscription{}, err
	}
	if s.ID == "" {
		s.ID = mongoID()
	}
	_, err = m.db.Collection("subscriptions").InsertOne(ctx, s)
	return s, err
}

func (m *Mongo) SubscriptionForDevice(ctx context.Context, channelID, deviceID string) (domain.Subscription, error) {
	var s domain.Subscription
	err := m.db.Collection("subscriptions").
		FindOne(ctx, bson.M{"channelId": channelID, "deviceId": deviceID}).Decode(&s)
	return s, mapErr(err)
}

func (m *Mongo) Unsubscribe(ctx context.Context, channelID, deviceID string) error {
	res, err := m.db.Collection("subscriptions").
		DeleteOne(ctx, bson.M{"channelId": channelID, "deviceId": deviceID})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *Mongo) SetSubscriptionStatusForUser(ctx context.Context, channelID, userID string, status domain.SubscriptionStatus) error {
	cur, err := m.db.Collection("devices").Find(ctx, bson.M{"userId": userID})
	if err != nil {
		return err
	}
	var devices []domain.Device
	if err := cur.All(ctx, &devices); err != nil {
		return err
	}
	if len(devices) == 0 {
		return nil
	}
	ids := make([]string, 0, len(devices))
	for _, d := range devices {
		ids = append(ids, d.ID)
	}
	_, err = m.db.Collection("subscriptions").UpdateMany(ctx,
		bson.M{"channelId": channelID, "deviceId": bson.M{"$in": ids}},
		bson.M{"$set": bson.M{"status": status}})
	return err
}

func (m *Mongo) ActiveDevicesForChannel(ctx context.Context, channelID string) ([]domain.Device, error) {
	subs, err := m.db.Collection("subscriptions").Find(ctx,
		bson.M{"channelId": channelID, "status": domain.SubActive})
	if err != nil {
		return nil, err
	}
	var subscriptions []domain.Subscription
	if err := subs.All(ctx, &subscriptions); err != nil {
		return nil, err
	}
	if len(subscriptions) == 0 {
		return nil, nil
	}
	deviceIDs := make([]string, 0, len(subscriptions))
	for _, s := range subscriptions {
		deviceIDs = append(deviceIDs, s.DeviceID)
	}
	cur, err := m.db.Collection("devices").Find(ctx, bson.M{"_id": bson.M{"$in": deviceIDs}})
	if err != nil {
		return nil, err
	}
	var out []domain.Device
	return out, cur.All(ctx, &out)
}

func (m *Mongo) DevicesForUsers(ctx context.Context, userIDs []string) ([]domain.Device, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	cur, err := m.db.Collection("devices").Find(ctx, bson.M{"userId": bson.M{"$in": userIDs}})
	if err != nil {
		return nil, err
	}
	var out []domain.Device
	return out, cur.All(ctx, &out)
}

// --- Channel members ---

func (m *Mongo) UpsertMember(ctx context.Context, mem domain.ChannelMember) (domain.ChannelMember, error) {
	filter := bson.M{"channelId": mem.ChannelID, "userId": mem.UserID}
	var existing domain.ChannelMember
	err := m.db.Collection("channelMembers").FindOne(ctx, filter).Decode(&existing)
	if err == nil {
		// Keep the existing membership: never downgrade active→pending on re-join.
		return existing, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return domain.ChannelMember{}, err
	}
	if mem.ID == "" {
		mem.ID = mongoID()
	}
	if _, ierr := m.db.Collection("channelMembers").InsertOne(ctx, mem); ierr != nil {
		if mongo.IsDuplicateKeyError(ierr) {
			// Lost a race with a concurrent join; return the row that won.
			var raced domain.ChannelMember
			if ferr := m.db.Collection("channelMembers").FindOne(ctx, filter).Decode(&raced); ferr == nil {
				return raced, nil
			}
		}
		return domain.ChannelMember{}, ierr
	}
	return mem, nil
}

func (m *Mongo) MembershipForUser(ctx context.Context, channelID, userID string) (domain.ChannelMember, error) {
	var mem domain.ChannelMember
	err := m.db.Collection("channelMembers").
		FindOne(ctx, bson.M{"channelId": channelID, "userId": userID}).Decode(&mem)
	return mem, mapErr(err)
}

func (m *Mongo) ListMembers(ctx context.Context, channelID string, status domain.MemberStatus, offset, limit int) ([]domain.ChannelMember, int64, error) {
	filter := bson.M{"channelId": channelID}
	if status != "" {
		filter["status"] = status
	}
	return findPaged[domain.ChannelMember](ctx, m.db.Collection("channelMembers"), filter, offset, limit)
}

func (m *Mongo) UpdateMemberStatus(ctx context.Context, channelID, userID string, status domain.MemberStatus) error {
	res, err := m.db.Collection("channelMembers").UpdateOne(ctx,
		bson.M{"channelId": channelID, "userId": userID},
		bson.M{"$set": bson.M{"status": status}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *Mongo) UpdateMemberRole(ctx context.Context, channelID, userID string, role domain.Role) error {
	res, err := m.db.Collection("channelMembers").UpdateOne(ctx,
		bson.M{"channelId": channelID, "userId": userID},
		bson.M{"$set": bson.M{"role": role}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *Mongo) RemoveMember(ctx context.Context, channelID, userID string) error {
	res, err := m.db.Collection("channelMembers").
		DeleteOne(ctx, bson.M{"channelId": channelID, "userId": userID})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *Mongo) ChannelsForMember(ctx context.Context, userID string) ([]domain.Channel, error) {
	cur, err := m.db.Collection("channelMembers").Find(ctx, bson.M{"userId": userID})
	if err != nil {
		return nil, err
	}
	var members []domain.ChannelMember
	if err := cur.All(ctx, &members); err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(members))
	for _, mem := range members {
		ids = append(ids, mem.ChannelID)
	}
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}})
	ccur, err := m.db.Collection("channels").Find(ctx, bson.M{"_id": bson.M{"$in": ids}}, opts)
	if err != nil {
		return nil, err
	}
	var out []domain.Channel
	return out, ccur.All(ctx, &out)
}

func (m *Mongo) CreateMessage(ctx context.Context, msg domain.Message) (domain.Message, error) {
	if msg.ID == "" {
		msg.ID = mongoID()
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	_, err := m.db.Collection("messages").InsertOne(ctx, msg)
	return msg, err
}

// MessagesByChannel returns messages newest-first. cursor is an exclusive
// message ID: results continue from just after that message. query, if
// non-empty, keeps only messages whose title or body matches (case-insensitive).
func (m *Mongo) MessageByID(ctx context.Context, id string) (domain.Message, error) {
	var msg domain.Message
	err := m.db.Collection("messages").FindOne(ctx, bson.M{"_id": id}).Decode(&msg)
	if err != nil {
		return domain.Message{}, mapErr(err)
	}
	return msg, nil
}

func (m *Mongo) MessagesByChannel(ctx context.Context, channelID, cursor, query string, limit int) ([]domain.Message, error) {
	filter := bson.M{"channelId": channelID}
	if q := strings.TrimSpace(query); q != "" {
		rx := primitive.Regex{Pattern: regexp.QuoteMeta(q), Options: "i"}
		filter["$or"] = bson.A{bson.M{"title": rx}, bson.M{"body": rx}}
	}
	if cursor != "" {
		var anchor domain.Message
		err := m.db.Collection("messages").FindOne(ctx, bson.M{"_id": cursor}).Decode(&anchor)
		if err == nil {
			filter["createdAt"] = bson.M{"$lt": anchor.CreatedAt}
		}
	}
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}})
	if limit > 0 {
		opts.SetLimit(int64(limit))
	}
	cur, err := m.db.Collection("messages").Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	var out []domain.Message
	return out, cur.All(ctx, &out)
}

// MessagesAround reads outwards from the centre message in both directions. Both
// halves are served by the existing (channelId, createdAt) index.
func (m *Mongo) MessagesAround(ctx context.Context, channelID, messageID string, limit int) ([]domain.Message, error) {
	centre, err := m.MessageByID(ctx, messageID)
	if err != nil {
		return nil, err
	}
	if centre.ChannelID != channelID {
		return nil, ErrNotFound
	}
	if limit <= 0 {
		limit = 50
	}
	half := int64(limit / 2)

	// Newer than the centre, read ascending then flipped, so the window stays
	// newest-first like every other message list.
	newer, err := m.messageWindow(ctx, channelID, bson.M{"$gt": centre.CreatedAt}, 1, half)
	if err != nil {
		return nil, err
	}
	older, err := m.messageWindow(ctx, channelID, bson.M{"$lt": centre.CreatedAt}, -1, half)
	if err != nil {
		return nil, err
	}

	out := make([]domain.Message, 0, len(newer)+1+len(older))
	for i := len(newer) - 1; i >= 0; i-- {
		out = append(out, newer[i])
	}
	out = append(out, centre)
	out = append(out, older...)
	return out, nil
}

func (m *Mongo) messageWindow(ctx context.Context, channelID string, createdAt bson.M, order int, limit int64) ([]domain.Message, error) {
	opts := options.Find().
		SetSort(bson.D{{Key: "createdAt", Value: order}}).
		SetLimit(limit)
	cur, err := m.db.Collection("messages").
		Find(ctx, bson.M{"channelId": channelID, "createdAt": createdAt}, opts)
	if err != nil {
		return nil, err
	}
	var out []domain.Message
	return out, cur.All(ctx, &out)
}

// LastMessagesByChannels groups by channel and keeps the newest message of each.
// The sort is served by the messages (channelId, createdAt desc) index.
func (m *Mongo) LastMessagesByChannels(ctx context.Context, channelIDs []string) (map[string]domain.Message, error) {
	out := make(map[string]domain.Message, len(channelIDs))
	if len(channelIDs) == 0 {
		return out, nil
	}
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"channelId": bson.M{"$in": channelIDs}}}},
		{{Key: "$sort", Value: bson.D{{Key: "channelId", Value: 1}, {Key: "createdAt", Value: -1}}}},
		{{Key: "$group", Value: bson.M{"_id": "$channelId", "doc": bson.M{"$first": "$$ROOT"}}}},
		{{Key: "$replaceRoot", Value: bson.M{"newRoot": "$doc"}}},
	}
	cur, err := m.db.Collection("messages").Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	var msgs []domain.Message
	if err := cur.All(ctx, &msgs); err != nil {
		return nil, err
	}
	for _, msg := range msgs {
		out[msg.ChannelID] = msg
	}
	return out, nil
}

func (m *Mongo) CreateDelivery(ctx context.Context, d domain.Delivery) (domain.Delivery, error) {
	if d.ID == "" {
		d.ID = mongoID()
	}
	if d.SentAt.IsZero() {
		d.SentAt = time.Now().UTC()
	}
	_, err := m.db.Collection("deliveries").InsertOne(ctx, d)
	return d, err
}

// --- Comments ---

func (m *Mongo) CreateComment(ctx context.Context, c domain.Comment) (domain.Comment, error) {
	if c.ID == "" {
		c.ID = mongoID()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	_, err := m.db.Collection("comments").InsertOne(ctx, c)
	return c, err
}

func (m *Mongo) CommentByID(ctx context.Context, id string) (domain.Comment, error) {
	var c domain.Comment
	err := m.db.Collection("comments").FindOne(ctx, bson.M{"_id": id}).Decode(&c)
	return c, mapErr(err)
}

func (m *Mongo) CommentsByMessage(ctx context.Context, messageID, cursor string, limit int) ([]domain.Comment, error) {
	filter := bson.M{"messageId": messageID}
	if cursor != "" {
		var anchor domain.Comment
		if err := m.db.Collection("comments").FindOne(ctx, bson.M{"_id": cursor}).Decode(&anchor); err == nil {
			filter["createdAt"] = bson.M{"$lt": anchor.CreatedAt}
		}
	}
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}})
	if limit > 0 {
		opts.SetLimit(int64(limit))
	}
	cur, err := m.db.Collection("comments").Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	var out []domain.Comment
	return out, cur.All(ctx, &out)
}

// CommentCountsByMessages tallies comments per message. The $match is served by
// the comments (messageId, createdAt desc) index prefix.
func (m *Mongo) CommentCountsByMessages(ctx context.Context, messageIDs []string) (map[string]int64, error) {
	out := make(map[string]int64, len(messageIDs))
	if len(messageIDs) == 0 {
		return out, nil
	}
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"messageId": bson.M{"$in": messageIDs}}}},
		{{Key: "$group", Value: bson.M{"_id": "$messageId", "count": bson.M{"$sum": 1}}}},
	}
	cur, err := m.db.Collection("comments").Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID    string `bson:"_id"`
		Count int64  `bson:"count"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = r.Count
	}
	return out, nil
}

func (m *Mongo) DeleteMessage(ctx context.Context, id string) error {
	msg, err := m.MessageByID(ctx, id)
	if err != nil {
		return err
	}
	imageIDs := make([]string, 0, len(msg.Images))
	for _, img := range msg.Images {
		imageIDs = append(imageIDs, img.ID)
	}
	if _, err := m.db.Collection("comments").DeleteMany(ctx, bson.M{"messageId": id}); err != nil {
		return err
	}
	if _, err := m.db.Collection("deliveries").DeleteMany(ctx, bson.M{"messageId": id}); err != nil {
		return err
	}
	res, err := m.db.Collection("messages").DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	deleteBlobs(ctx, m.blobs, imageIDs)
	return nil
}

func (m *Mongo) DeleteComment(ctx context.Context, id string) error {
	res, err := m.db.Collection("comments").DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *Mongo) AdminListComments(ctx context.Context, query string, offset, limit int) ([]domain.Comment, int64, error) {
	filter := bson.M{}
	if q := strings.TrimSpace(query); q != "" {
		filter["body"] = primitive.Regex{Pattern: regexp.QuoteMeta(q), Options: "i"}
	}
	return findPaged[domain.Comment](ctx, m.db.Collection("comments"), filter, offset, limit)
}

func (m *Mongo) ListAllChannels(ctx context.Context) ([]domain.Channel, error) {
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}})
	cur, err := m.db.Collection("channels").Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, err
	}
	var out []domain.Channel
	return out, cur.All(ctx, &out)
}

func (m *Mongo) AdminListChannels(ctx context.Context, query string, offset, limit int) ([]domain.Channel, int64, error) {
	filter := bson.M{}
	if q := strings.TrimSpace(query); q != "" {
		filter["name"] = primitive.Regex{Pattern: regexp.QuoteMeta(q), Options: "i"}
	}
	return findPaged[domain.Channel](ctx, m.db.Collection("channels"), filter, offset, limit)
}

// findPaged runs a filtered, newest-first, paginated query and also returns the
// total count of matching documents (ignoring pagination).
func findPaged[T any](ctx context.Context, coll *mongo.Collection, filter bson.M, offset, limit int) ([]T, int64, error) {
	total, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}})
	if offset > 0 {
		opts.SetSkip(int64(offset))
	}
	if limit > 0 {
		opts.SetLimit(int64(limit))
	}
	cur, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	var out []T
	if err := cur.All(ctx, &out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func (m *Mongo) AdminStats(ctx context.Context, topN, recentN int) (domain.AdminStats, error) {
	var stats domain.AdminStats

	counts := []struct {
		coll string
		dst  *int64
	}{
		{"users", &stats.Users},
		{"channels", &stats.Channels},
		{"messages", &stats.Messages},
		{"deliveries", &stats.Deliveries},
		{"devices", &stats.Devices},
	}
	for _, c := range counts {
		n, err := m.db.Collection(c.coll).EstimatedDocumentCount(ctx)
		if err != nil {
			return domain.AdminStats{}, err
		}
		*c.dst = n
	}

	// Top channels by message volume via aggregation.
	pipeline := mongo.Pipeline{
		{{Key: "$group", Value: bson.D{{Key: "_id", Value: "$channelId"}, {Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}}}}},
		{{Key: "$sort", Value: bson.D{{Key: "count", Value: -1}}}},
		{{Key: "$limit", Value: int64(maxInt(topN, 1))}},
	}
	cur, err := m.db.Collection("messages").Aggregate(ctx, pipeline)
	if err != nil {
		return domain.AdminStats{}, err
	}
	var grouped []struct {
		ID    string `bson:"_id"`
		Count int64  `bson:"count"`
	}
	if err := cur.All(ctx, &grouped); err != nil {
		return domain.AdminStats{}, err
	}
	for _, g := range grouped {
		name := g.ID
		if ch, err := m.ChannelByID(ctx, g.ID); err == nil {
			name = ch.Name
		}
		stats.TopChannels = append(stats.TopChannels, domain.ChannelVolume{ChannelID: g.ID, Name: name, Count: g.Count})
	}

	// Recent messages across all channels.
	mopts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(int64(maxInt(recentN, 1)))
	mcur, err := m.db.Collection("messages").Find(ctx, bson.M{}, mopts)
	if err != nil {
		return domain.AdminStats{}, err
	}
	if err := mcur.All(ctx, &stats.RecentMessages); err != nil {
		return domain.AdminStats{}, err
	}
	return stats, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m *Mongo) Close(ctx context.Context) error {
	return m.client.Disconnect(ctx)
}

// Verify Mongo satisfies the Store interface.
var _ Store = (*Mongo)(nil)
