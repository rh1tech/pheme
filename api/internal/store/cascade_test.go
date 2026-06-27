package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/domain"
)

// TestDeleteChannelCascadesImageBlobs verifies that deleting a channel removes
// the image blobs of its messages via the injected blob store.
func TestDeleteChannelCascadesImageBlobs(t *testing.T) {
	ctx := context.Background()
	blobs := blob.NewMemory()
	db := NewMemory(blobs)

	ch, err := db.CreateChannel(ctx, domain.Channel{
		PublicID: "ch_test", OwnerID: "u1", Name: "Photos",
		SubscriptionMode: domain.ModeOpen, Status: domain.ChannelActive, CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	id, err := blobs.Put(ctx, []byte("jpeg-bytes"), "image/jpeg")
	if err != nil {
		t.Fatalf("blob Put: %v", err)
	}
	if _, err := db.CreateMessage(ctx, domain.Message{
		ChannelID: ch.ID,
		Title:     "hi",
		Images:    []domain.MessageImage{{ID: id, Width: 800, Height: 600}},
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	if err := db.DeleteChannel(ctx, ch.ID); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}

	if _, _, err := blobs.Get(ctx, id); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("blob still present after channel delete (err=%v)", err)
	}
}

// TestDeleteUserCascadesImageBlobs verifies the same through the user-delete path
// (which deletes the user's channels and their messages' images).
func TestDeleteUserCascadesImageBlobs(t *testing.T) {
	ctx := context.Background()
	blobs := blob.NewMemory()
	db := NewMemory(blobs)

	u, err := db.CreateUser(ctx, domain.User{Email: "u@x.com", Role: domain.RoleUser, Status: domain.UserActive, CreatedAt: time.Now()})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	ch, err := db.CreateChannel(ctx, domain.Channel{
		PublicID: "ch_u", OwnerID: u.ID, Name: "Photos",
		SubscriptionMode: domain.ModeOpen, Status: domain.ChannelActive, CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	id, _ := blobs.Put(ctx, []byte("jpeg"), "image/jpeg")
	if _, err := db.CreateMessage(ctx, domain.Message{
		ChannelID: ch.ID,
		Images:    []domain.MessageImage{{ID: id}},
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	if err := db.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, _, err := blobs.Get(ctx, id); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("blob still present after user delete (err=%v)", err)
	}
}
