package repositories

import (
	"context"
	"testing"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/testutil"
)

func TestBotRepositoryCreateAndListByUserID(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewBotRepository(db)
	ctx := context.Background()

	created, err := repo.Create(ctx, domain.Bot{
		ID:               "bot_1",
		UserID:           "usr_1",
		Name:             "sales-bot",
		ChannelType:      "wechat",
		ConnectionStatus: domain.BotConnectionStatusLoginRequired,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "sales-bot" {
		t.Fatalf("unexpected bot name: %s", created.Name)
	}

	items, err := repo.ListByUserID(ctx, "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 bot, got %d", len(items))
	}
	if items[0].Name != "sales-bot" {
		t.Fatalf("unexpected listed bot name: %s", items[0].Name)
	}
}

func TestBotRepositoryUpdateConnectionState(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewBotRepository(db)
	ctx := context.Background()

	bot, err := repo.Create(ctx, domain.Bot{
		ID:               "bot_2",
		UserID:           "usr_1",
		Name:             "support-bot",
		ChannelType:      "wechat",
		ConnectionStatus: domain.BotConnectionStatusLoginRequired,
	})
	if err != nil {
		t.Fatal(err)
	}

	bot.ChannelAccountID = "acct_1"
	bot.ConnectionStatus = domain.BotConnectionStatusConnected
	bot.ConnectionError = ""
	got, err := repo.Update(ctx, bot)
	if err != nil {
		t.Fatal(err)
	}
	if got.ChannelAccountID != "acct_1" {
		t.Fatalf("unexpected channel account id: %s", got.ChannelAccountID)
	}
	if got.ConnectionStatus != domain.BotConnectionStatusConnected {
		t.Fatalf("unexpected connection status: %s", got.ConnectionStatus)
	}
}

func TestBotRepositoryDeleteByID(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewBotRepository(db)
	ctx := context.Background()

	_, err := repo.Create(ctx, domain.Bot{
		ID:               "bot_3",
		UserID:           "usr_1",
		Name:             "cleanup-bot",
		ChannelType:      "wechat",
		ConnectionStatus: domain.BotConnectionStatusLoginRequired,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := repo.DeleteByID(ctx, "bot_3"); err != nil {
		t.Fatal(err)
	}

	_, err = repo.GetByID(ctx, "bot_3")
	if err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
