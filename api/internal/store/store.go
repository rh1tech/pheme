// Package store defines persistence contracts for Pheme and ships an in-memory
// implementation used for local development and tests.
//
// A MongoDB-backed implementation should satisfy the same Store interface; see
// store_mongo.go (TODO) for the production adapter.
package store

import (
	"context"
	"errors"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("not found")

// Store is the persistence contract used by the App API and Dispatcher.
type Store interface {
	// Users
	CreateUser(ctx context.Context, u domain.User) (domain.User, error)
	UserByEmail(ctx context.Context, email string) (domain.User, error)
	UpdateUserRole(ctx context.Context, userID string, role domain.Role) error
	ListUsers(ctx context.Context) ([]domain.User, error)
	DeleteUser(ctx context.Context, userID string) error

	// Channels
	CreateChannel(ctx context.Context, c domain.Channel) (domain.Channel, error)
	ChannelByID(ctx context.Context, id string) (domain.Channel, error)
	ChannelByPublicID(ctx context.Context, publicID string) (domain.Channel, error)
	ChannelsByOwner(ctx context.Context, ownerID string) ([]domain.Channel, error)
	UpdateChannel(ctx context.Context, id, name string, mode domain.SubscriptionMode) (domain.Channel, error)
	DeleteChannel(ctx context.Context, id string) error
	ListAllChannels(ctx context.Context) ([]domain.Channel, error)

	// API keys
	CreateAPIKey(ctx context.Context, k domain.APIKey) (domain.APIKey, error)
	APIKeysByChannel(ctx context.Context, channelID string) ([]domain.APIKey, error)
	RevokeAPIKey(ctx context.Context, keyID string) error

	// Devices & subscriptions
	CreateDevice(ctx context.Context, d domain.Device) (domain.Device, error)
	Subscribe(ctx context.Context, s domain.Subscription) (domain.Subscription, error)
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
