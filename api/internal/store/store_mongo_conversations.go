package store

import (
	"context"
	"errors"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// sortConversationsByActivity orders newest-first by last message time, falling
// back to a conversation's own creation time when it has no messages yet.
func sortConversationsByActivity(convs []domain.Conversation, last map[string]domain.ChatMessage) {
	activity := func(c domain.Conversation) int64 {
		if msg, ok := last[c.ID]; ok {
			return msg.CreatedAt.UnixNano()
		}
		return c.CreatedAt.UnixNano()
	}
	sort.SliceStable(convs, func(i, j int) bool { return activity(convs[i]) > activity(convs[j]) })
}

func (m *Mongo) CreateConversation(ctx context.Context, c domain.Conversation, members []domain.ConversationMember) (domain.Conversation, error) {
	if c.ID == "" {
		c.ID = mongoID()
	}
	if _, err := m.db.Collection("conversations").InsertOne(ctx, c); err != nil {
		// A racing direct-chat create hits the unique directKey index; surface it
		// so the handler can fall back to the existing conversation.
		if mongo.IsDuplicateKeyError(err) {
			return domain.Conversation{}, ErrAliasTaken
		}
		return domain.Conversation{}, err
	}
	docs := make([]any, 0, len(members))
	for _, mem := range members {
		if mem.ID == "" {
			mem.ID = mongoID()
		}
		mem.ConversationID = c.ID
		docs = append(docs, mem)
	}
	if len(docs) > 0 {
		if _, err := m.db.Collection("conversationMembers").InsertMany(ctx, docs); err != nil {
			return domain.Conversation{}, err
		}
	}
	return c, nil
}

func (m *Mongo) ConversationByID(ctx context.Context, id string) (domain.Conversation, error) {
	var c domain.Conversation
	err := m.db.Collection("conversations").FindOne(ctx, bson.M{"_id": id}).Decode(&c)
	return c, mapErr(err)
}

func (m *Mongo) ConversationByDirectKey(ctx context.Context, directKey string) (domain.Conversation, error) {
	if directKey == "" {
		return domain.Conversation{}, ErrNotFound
	}
	var c domain.Conversation
	err := m.db.Collection("conversations").FindOne(ctx, bson.M{"directKey": directKey}).Decode(&c)
	return c, mapErr(err)
}

func (m *Mongo) ConversationsForUser(ctx context.Context, userID string) ([]domain.Conversation, error) {
	// Membership rows → conversation ids.
	cur, err := m.db.Collection("conversationMembers").Find(ctx, bson.M{"userId": userID})
	if err != nil {
		return nil, err
	}
	var memberships []domain.ConversationMember
	if err := cur.All(ctx, &memberships); err != nil {
		return nil, err
	}
	if len(memberships) == 0 {
		return []domain.Conversation{}, nil
	}
	ids := make([]string, 0, len(memberships))
	for _, mem := range memberships {
		ids = append(ids, mem.ConversationID)
	}

	cur, err = m.db.Collection("conversations").Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	var out []domain.Conversation
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}

	// Order by last activity. One aggregate for the newest createdAt per
	// conversation, then sort in memory — the list is a single user's chats.
	last, err := m.LastChatMessagesByConversations(ctx, ids)
	if err != nil {
		return nil, err
	}
	sortConversationsByActivity(out, last)
	return out, nil
}

func (m *Mongo) ConversationMembers(ctx context.Context, conversationID string) ([]domain.ConversationMember, error) {
	opts := options.Find().SetSort(bson.D{{Key: "joinedAt", Value: 1}})
	cur, err := m.db.Collection("conversationMembers").Find(ctx, bson.M{"conversationId": conversationID}, opts)
	if err != nil {
		return nil, err
	}
	var out []domain.ConversationMember
	return out, cur.All(ctx, &out)
}

func (m *Mongo) ConversationMembership(ctx context.Context, conversationID, userID string) (domain.ConversationMember, error) {
	var mem domain.ConversationMember
	err := m.db.Collection("conversationMembers").
		FindOne(ctx, bson.M{"conversationId": conversationID, "userId": userID}).Decode(&mem)
	return mem, mapErr(err)
}

func (m *Mongo) AddConversationMember(ctx context.Context, mem domain.ConversationMember) (domain.ConversationMember, error) {
	// Idempotent on the unique (conversationId, userId) index.
	if existing, err := m.ConversationMembership(ctx, mem.ConversationID, mem.UserID); err == nil {
		return existing, nil
	}
	if mem.ID == "" {
		mem.ID = mongoID()
	}
	if _, err := m.db.Collection("conversationMembers").InsertOne(ctx, mem); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return m.ConversationMembership(ctx, mem.ConversationID, mem.UserID)
		}
		return domain.ConversationMember{}, err
	}
	return mem, nil
}

func (m *Mongo) RemoveConversationMember(ctx context.Context, conversationID, userID string) error {
	res, err := m.db.Collection("conversationMembers").
		DeleteOne(ctx, bson.M{"conversationId": conversationID, "userId": userID})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *Mongo) SetConversationMemberRole(ctx context.Context, conversationID, userID string, role domain.Role) error {
	res, err := m.db.Collection("conversationMembers").UpdateOne(ctx,
		bson.M{"conversationId": conversationID, "userId": userID},
		bson.M{"$set": bson.M{"role": role}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *Mongo) DeleteConversation(ctx context.Context, conversationID string) error {
	if _, err := m.db.Collection("chatMessages").
		DeleteMany(ctx, bson.M{"conversationId": conversationID}); err != nil {
		return err
	}
	if _, err := m.db.Collection("conversationMembers").
		DeleteMany(ctx, bson.M{"conversationId": conversationID}); err != nil {
		return err
	}
	_, err := m.db.Collection("conversations").DeleteOne(ctx, bson.M{"_id": conversationID})
	return err
}

func (m *Mongo) AppendChatMessage(ctx context.Context, msg domain.ChatMessage) (domain.ChatMessage, error) {
	if msg.ID == "" {
		msg.ID = mongoID()
	}
	_, err := m.db.Collection("chatMessages").InsertOne(ctx, msg)
	return msg, err
}

func (m *Mongo) ClearConversationHistory(ctx context.Context, conversationID, userID string, before time.Time) error {
	res, err := m.db.Collection("conversationMembers").UpdateOne(ctx,
		bson.M{"conversationId": conversationID, "userId": userID},
		bson.M{"$set": bson.M{"clearedAt": before}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *Mongo) ChatMessagesByConversation(ctx context.Context, conversationID, cursor string, limit int, after time.Time) ([]domain.ChatMessage, error) {
	filter := bson.M{
		"conversationId": conversationID,
		// The transcript, not the raw log: MLS protocol traffic is excluded here rather than
		// by the client, so a page of `limit` is `limit` messages people actually sent. Filtered
		// client-side it merely arrived and was thrown away, and a chat whose recent log is
		// mostly Commits and device announcements showed a nearly empty page.
		"contentType": bson.M{"$nin": domain.MLSProtocolContentTypes},
	}
	// The caller's clear-history watermark and the load-older cursor both bound
	// createdAt, so they combine into one range condition rather than overwriting.
	created := bson.M{}
	if !after.IsZero() {
		created["$gt"] = after
	}
	if cursor != "" {
		var anchor domain.ChatMessage
		if err := m.db.Collection("chatMessages").FindOne(ctx, bson.M{"_id": cursor}).Decode(&anchor); err == nil {
			created["$lt"] = anchor.CreatedAt
		}
	}
	if len(created) > 0 {
		filter["createdAt"] = created
	}
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}})
	if limit > 0 {
		opts.SetLimit(int64(limit))
	}
	cur, err := m.db.Collection("chatMessages").Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	var out []domain.ChatMessage
	return out, cur.All(ctx, &out)
}

func (m *Mongo) LastChatMessagesByConversations(ctx context.Context, conversationIDs []string) (map[string]domain.ChatMessage, error) {
	out := make(map[string]domain.ChatMessage, len(conversationIDs))
	if len(conversationIDs) == 0 {
		return out, nil
	}
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"conversationId": bson.M{"$in": conversationIDs}}}},
		{{Key: "$sort", Value: bson.D{{Key: "conversationId", Value: 1}, {Key: "createdAt", Value: -1}}}},
		{{Key: "$group", Value: bson.M{"_id": "$conversationId", "doc": bson.M{"$first": "$$ROOT"}}}},
		{{Key: "$replaceRoot", Value: bson.M{"newRoot": "$doc"}}},
	}
	cur, err := m.db.Collection("chatMessages").Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	var msgs []domain.ChatMessage
	if err := cur.All(ctx, &msgs); err != nil {
		return nil, err
	}
	for _, msg := range msgs {
		out[msg.ConversationID] = msg
	}
	return out, nil
}

// --- attachments ---

func (m *Mongo) CreateAttachment(ctx context.Context, a domain.Attachment) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	_, err := m.db.Collection("attachments").InsertOne(ctx, a)
	return err
}

func (m *Mongo) GetAttachment(ctx context.Context, id string) (domain.Attachment, error) {
	var a domain.Attachment
	err := m.db.Collection("attachments").FindOne(ctx, bson.M{"_id": id}).Decode(&a)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return domain.Attachment{}, ErrNotFound
	}
	return a, err
}

func (m *Mongo) ListAttachmentIDs(ctx context.Context, conversationID string) ([]string, error) {
	cur, err := m.db.Collection("attachments").
		Find(ctx, bson.M{"conversationId": conversationID})
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()

	var out []string
	for cur.Next(ctx) {
		var a domain.Attachment
		if err := cur.Decode(&a); err != nil {
			return nil, err
		}
		out = append(out, a.ID)
	}
	return out, cur.Err()
}

func (m *Mongo) DeleteAttachments(ctx context.Context, conversationID string) error {
	_, err := m.db.Collection("attachments").
		DeleteMany(ctx, bson.M{"conversationId": conversationID})
	return err
}
