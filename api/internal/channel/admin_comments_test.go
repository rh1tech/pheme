package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/store"
)

func TestAdminListAndDeleteComment(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "admin@b.com", domain.RoleAdmin)
	author := seedUser(t, db, "author@b.com", domain.RoleUser)

	ctx := context.Background()
	ch, _ := db.CreateChannel(ctx, domain.Channel{Name: "Chan", OwnerID: author.ID, Status: domain.ChannelActive, CreatedAt: time.Now().UTC()})
	msg, _ := db.CreateMessage(ctx, domain.Message{ChannelID: ch.ID, Title: "Post", CommentsAllowed: true, CreatedAt: time.Now().UTC()})
	c, _ := db.CreateComment(ctx, domain.Comment{MessageID: msg.ID, ChannelID: ch.ID, UserID: author.ID, Body: "spam here", CreatedAt: time.Now().UTC()})

	// List with search shows the enriched comment.
	rec := adminReq(mux, http.MethodGet, "/v1/admin/comments?q=spam", admin.ID, "admin", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var list struct {
		Comments []adminComment `json:"comments"`
		Total    int64          `json:"total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if list.Total != 1 || len(list.Comments) != 1 {
		t.Fatalf("unexpected list: %+v", list)
	}
	got := list.Comments[0]
	if got.AuthorEmail != "author@b.com" || got.ChannelName != "Chan" || got.MessageTitle != "Post" {
		t.Fatalf("enrichment missing: %+v", got)
	}

	// Delete it.
	rec = adminReq(mux, http.MethodDelete, "/v1/admin/comments/"+c.ID, admin.ID, "admin", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	if _, err := db.CommentByID(ctx, c.ID); err == nil {
		t.Fatalf("comment should be gone")
	}
}

func TestAdminCommentsRequireAdmin(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	u := seedUser(t, db, "plain@b.com", domain.RoleUser)
	rec := adminReq(mux, http.MethodGet, "/v1/admin/comments", u.ID, "user", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}
