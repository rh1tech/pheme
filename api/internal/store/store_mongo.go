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

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Mongo is a MongoDB-backed Store. Entity IDs are application-generated hex
// strings stored as the document _id.
type Mongo struct {
	client *mongo.Client
	db     *mongo.Database
}

// NewMongo connects to MongoDB, verifies connectivity, and ensures indexes.
func NewMongo(ctx context.Context, uri, dbName string) (*Mongo, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		return nil, err
	}
	m := &Mongo{client: client, db: client.Database(dbName)}
	if err := m.ensureIndexes(ctx); err != nil {
		_ = client.Disconnect(ctx)
		return nil, err
	}
	return m, nil
}

func mongoID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Mongo) ensureIndexes(ctx context.Context) error {
	specs := map[string][]mongo.IndexModel{
		"users":         {{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"channels":      {{Keys: bson.D{{Key: "publicId", Value: 1}}, Options: options.Index().SetUnique(true)}, {Keys: bson.D{{Key: "ownerId", Value: 1}}}},
		"apiKeys":       {{Keys: bson.D{{Key: "channelId", Value: 1}}}},
		"devices":       {{Keys: bson.D{{Key: "userId", Value: 1}}}},
		"subscriptions": {{Keys: bson.D{{Key: "channelId", Value: 1}, {Key: "status", Value: 1}}}, {Keys: bson.D{{Key: "deviceId", Value: 1}}}},
		"messages":      {{Keys: bson.D{{Key: "channelId", Value: 1}, {Key: "createdAt", Value: -1}}}},
		"deliveries":    {{Keys: bson.D{{Key: "messageId", Value: 1}}}},
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
	_, err := m.db.Collection("users").InsertOne(ctx, u)
	return u, err
}

func (m *Mongo) UserByEmail(ctx context.Context, email string) (domain.User, error) {
	var u domain.User
	err := m.db.Collection("users").FindOne(ctx, bson.M{"email": email}).Decode(&u)
	return u, mapErr(err)
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

func (m *Mongo) ListUsers(ctx context.Context) ([]domain.User, error) {
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}})
	cur, err := m.db.Collection("users").Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, err
	}
	var out []domain.User
	return out, cur.All(ctx, &out)
}

func (m *Mongo) DeleteUser(ctx context.Context, userID string) error {
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

	res, err := m.db.Collection("users").DeleteOne(ctx, bson.M{"_id": userID})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
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
	if len(msgs) > 0 {
		ids := make([]string, 0, len(msgs))
		for _, msg := range msgs {
			ids = append(ids, msg.ID)
		}
		if _, err := m.db.Collection("deliveries").DeleteMany(ctx, bson.M{"messageId": bson.M{"$in": ids}}); err != nil {
			return err
		}
	}
	for _, coll := range []string{"messages", "subscriptions", "apiKeys"} {
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
	return nil
}

func (m *Mongo) CreateAPIKey(ctx context.Context, k domain.APIKey) (domain.APIKey, error) {
	if k.ID == "" {
		k.ID = mongoID()
	}
	_, err := m.db.Collection("apiKeys").InsertOne(ctx, k)
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

func (m *Mongo) CreateDevice(ctx context.Context, d domain.Device) (domain.Device, error) {
	if d.ID == "" {
		d.ID = mongoID()
	}
	_, err := m.db.Collection("devices").InsertOne(ctx, d)
	return d, err
}

func (m *Mongo) Subscribe(ctx context.Context, s domain.Subscription) (domain.Subscription, error) {
	if s.ID == "" {
		s.ID = mongoID()
	}
	_, err := m.db.Collection("subscriptions").InsertOne(ctx, s)
	return s, err
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

func (m *Mongo) ListAllChannels(ctx context.Context) ([]domain.Channel, error) {
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}})
	cur, err := m.db.Collection("channels").Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, err
	}
	var out []domain.Channel
	return out, cur.All(ctx, &out)
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
