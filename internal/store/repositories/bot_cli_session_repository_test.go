package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/testutil"
)

func TestBotCLISessionRepositoryUpsertGet(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewBotCLISessionRepository(db)
	ctx := context.Background()

	if _, err := repo.Get(ctx, "bot1", "claude"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := repo.Upsert(ctx, domain.BotCLISession{BotID: "bot1", CLIType: "claude", SessionID: "sess_a", WorkDir: "/w"}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "bot1", "claude")
	if err != nil || got.SessionID != "sess_a" || got.WorkDir != "/w" {
		t.Fatalf("get after upsert = %#v err=%v", got, err)
	}
	// upsert again on same key overwrites
	if err := repo.Upsert(ctx, domain.BotCLISession{BotID: "bot1", CLIType: "claude", SessionID: "sess_b", WorkDir: "/w2"}); err != nil {
		t.Fatal(err)
	}
	got2, _ := repo.Get(ctx, "bot1", "claude")
	if got2.SessionID != "sess_b" || got2.WorkDir != "/w2" {
		t.Fatalf("overwrite failed = %#v", got2)
	}
	// different cli_type is a separate row
	if err := repo.Upsert(ctx, domain.BotCLISession{BotID: "bot1", CLIType: "codex", SessionID: "conv_x"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := repo.Get(ctx, "bot1", "codex"); c.SessionID != "conv_x" {
		t.Fatalf("codex row = %#v", c)
	}
	if a, _ := repo.Get(ctx, "bot1", "claude"); a.SessionID != "sess_b" {
		t.Fatalf("claude row clobbered = %#v", a)
	}
}
