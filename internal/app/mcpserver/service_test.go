package mcpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/store/repositories"
	"github.com/benenen/myclaw/internal/testutil"
	"gorm.io/gorm"
)

func newSvc(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db := testutil.OpenTestDB(t)
	return NewService(
		repositories.NewMCPServerRepository(db),
		repositories.NewBotRepository(db),
	), db
}

func TestServiceCreateValidation(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()

	if _, err := svc.Create(ctx, CreateInput{Name: "", ServerType: "http", URL: "x"}); !errors.Is(err, domain.ErrInvalidArg) {
		t.Fatalf("empty name should be invalid, got %v", err)
	}
	if _, err := svc.Create(ctx, CreateInput{Name: "h", ServerType: "http"}); !errors.Is(err, domain.ErrInvalidArg) {
		t.Fatalf("http without url should be invalid, got %v", err)
	}
	if _, err := svc.Create(ctx, CreateInput{Name: "s", ServerType: "stdio"}); !errors.Is(err, domain.ErrInvalidArg) {
		t.Fatalf("stdio without command should be invalid, got %v", err)
	}
	if _, err := svc.Create(ctx, CreateInput{Name: "ok", ServerType: "http", URL: "http://x"}); err != nil {
		t.Fatalf("valid create failed: %v", err)
	}
	if _, err := svc.Create(ctx, CreateInput{Name: "ok", ServerType: "http", URL: "http://y"}); !errors.Is(err, domain.ErrInvalidArg) {
		t.Fatalf("duplicate name should be ErrInvalidArg, got %v", err)
	}
}

func TestServiceAttachValidatesExistence(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, CreateInput{Name: "srv", ServerType: "http", URL: "http://x"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.AttachToBot(ctx, "nope", "srv"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("attach to unknown bot should be ErrNotFound, got %v", err)
	}
}

func TestSetBotServersValidation(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()

	// unknown bot -> ErrNotFound (requireBot)
	if err := svc.SetBotServers(ctx, "no-such-bot", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown bot: want ErrNotFound, got %v", err)
	}

	// seed a real bot so we can test further validations
	botRepo := repositories.NewBotRepository(db)
	seededBot, err := botRepo.Create(ctx, domain.Bot{
		ID:               "bot_mcp_test",
		UserID:           "usr_1",
		Name:             "mcp-test-bot",
		ChannelType:      "wechat",
		ConnectionStatus: domain.BotConnectionStatusLoginRequired,
	})
	if err != nil {
		t.Fatalf("seed bot: %v", err)
	}

	// known bot + unknown server id -> ErrInvalidArg
	if err := svc.SetBotServers(ctx, seededBot.ID, []string{"mcp-missing"}); !errors.Is(err, domain.ErrInvalidArg) {
		t.Fatalf("unknown server id: want ErrInvalidArg, got %v", err)
	}

	// known bot + real server id -> success
	srv, err := svc.Create(ctx, CreateInput{Name: "ok", ServerType: "http", URL: "http://x"})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	if err := svc.SetBotServers(ctx, seededBot.ID, []string{srv.ID}); err != nil {
		t.Fatalf("valid set failed: %v", err)
	}
}
