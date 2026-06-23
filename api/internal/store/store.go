// Package store defines persistence contracts for Pheme and ships an in-memory
// implementation used for local development and tests.
//
// A MongoDB-backed implementation should satisfy the same Store interface; see
// store_mongo.go (TODO) for the production adapter.
package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("not found")

// webPushEndpoint extracts the endpoint URL from a Web Push subscription JSON
// string, or returns "" if the input is empty or not a web push subscription.
func webPushEndpoint(webPushSub string) string {
	if webPushSub == "" {
		return ""
	}
	var s struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal([]byte(webPushSub), &s); err != nil {
		return ""
	}
	return s.Endpoint
}

// Store is the persistence contract used by the App API and Dispatcher.
type Store interface {
	// Users
	CreateUser(ctx context.Context, u domain.User) (domain.User, error)
	UserByEmail(ctx context.Context, email string) (domain.User, error)
	UpdateUserRole(ctx context.Context, userID string, role domain.Role) error
	UpdateUserStatus(ctx context.Context, userID string, status domain.UserStatus) error
	ListUsers(ctx context.Context) ([]domain.User, error)
	AdminListUsers(ctx context.Context, query string, offset, limit int) ([]domain.User, int64, error)
	DeleteUser(ctx context.Context, userID string) error

	// Channels
	CreateChannel(ctx context.Context, c domain.Channel) (domain.Channel, error)
	ChannelByID(ctx context.Context, id string) (domain.Channel, error)
	ChannelByPublicID(ctx context.Context, publicID string) (domain.Channel, error)
	ChannelsByOwner(ctx context.Context, ownerID string) ([]domain.Channel, error)
	UpdateChannel(ctx context.Context, id, name string, mode domain.SubscriptionMode) (domain.Channel, error)
	UpdateChannelStatus(ctx context.Context, id string, status domain.ChannelStatus) (domain.Channel, error)
	DeleteChannel(ctx context.Context, id string) error
	ListAllChannels(ctx context.Context) ([]domain.Channel, error)
	AdminListChannels(ctx context.Context, query string, offset, limit int) ([]domain.Channel, int64, error)

	// API keys
	CreateAPIKey(ctx context.Context, k domain.APIKey) (domain.APIKey, error)
	APIKeysByChannel(ctx context.Context, channelID string) ([]domain.APIKey, error)
	RevokeAPIKey(ctx context.Context, keyID string) error

	// Devices & subscriptions
	CreateDevice(ctx context.Context, d domain.Device) (domain.Device, error)
	Subscribe(ctx context.Context, s domain.Subscription) (domain.Subscription, error)
	SubscriptionForDevice(ctx context.Context, channelID, deviceID string) (domain.Subscription, error)
	Unsubscribe(ctx context.Context, channelID, deviceID string) error
	ActiveDevicesForChannel(ctx context.Context, channelID string) ([]domain.Device, error)

	// Messages & deliveries
	CreateMessage(ctx context.Context, m domain.Message) (domain.Message, error)
	MessagesByChannel(ctx context.Context, channelID, cursor, query string, limit int) ([]domain.Message, error)
	CreateDelivery(ctx context.Context, d domain.Delivery) (domain.Delivery, error)

	// Admin
	AdminStats(ctx context.Context, topN, recentN int) (domain.AdminStats, error)

	// Lifecycle
	Close(ctx context.Context) error
}
