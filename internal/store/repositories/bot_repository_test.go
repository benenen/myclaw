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
		ID:                "bot_1",
		UserID:            "usr_1",
		Name:              "sales-bot",
		ChannelType:       "wechat",
		ConnectionStatus:  domain.BotConnectionStatusLoginRequired,
		AgentCapabilityID: "cap_codex",
		AgentMode:         "codex-exec",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "sales-bot" {
		t.Fatalf("unexpected bot name: %s", created.Name)
	}
	if created.AgentCapabilityID != "cap_codex" {
		t.Fatalf("unexpected agent capability id: %s", created.AgentCapabilityID)
	}
	if created.AgentMode != "codex-exec" {
		t.Fatalf("unexpected agent mode: %s", created.AgentMode)
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
	if items[0].AgentCapabilityID != "cap_codex" {
		t.Fatalf("unexpected listed agent capability id: %s", items[0].AgentCapabilityID)
	}
	if items[0].AgentMode != "codex-exec" {
		t.Fatalf("unexpected listed agent mode: %s", items[0].AgentMode)
	}
}

func TestBotRepositoryGetByIDPreservesAgentFields(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewBotRepository(db)
	ctx := context.Background()

	_, err := repo.Create(ctx, domain.Bot{
		ID:                "bot_get_1",
		UserID:            "usr_1",
		Name:              "get-bot",
		ChannelType:       "wechat",
		ConnectionStatus:  domain.BotConnectionStatusLoginRequired,
		AgentCapabilityID: "cap_claude",
		AgentMode:         "session",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, "bot_get_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentCapabilityID != "cap_claude" {
		t.Fatalf("unexpected agent capability id: %s", got.AgentCapabilityID)
	}
	if got.AgentMode != "session" {
		t.Fatalf("unexpected agent mode: %s", got.AgentMode)
	}
}

func TestBotRepositoryGetByName(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewBotRepository(db)
	ctx := context.Background()

	_, err := repo.Create(ctx, domain.Bot{
		ID:                "bot_name_1",
		UserID:            "usr_1",
		Name:              "vikunja",
		ChannelType:       "wechat",
		ConnectionStatus:  domain.BotConnectionStatusLoginRequired,
		AgentCapabilityID: "cap_codex",
		AgentMode:         "codex-acp",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByName(ctx, "vikunja")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "bot_name_1" {
		t.Fatalf("unexpected bot id: %s", got.ID)
	}
	if got.AgentCapabilityID != "cap_codex" {
		t.Fatalf("unexpected agent capability id: %s", got.AgentCapabilityID)
	}
	if got.AgentMode != "codex-acp" {
		t.Fatalf("unexpected agent mode: %s", got.AgentMode)
	}

	if _, err := repo.GetByName(ctx, "nonexistent"); err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestBotRepositoryListWithAccountsPreservesAgentFields(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewBotRepository(db)
	ctx := context.Background()

	_, err := repo.Create(ctx, domain.Bot{
		ID:                "bot_acct_1",
		UserID:            "usr_1",
		Name:              "acct-bot",
		ChannelType:       "wechat",
		ChannelAccountID:  "acct_1",
		ConnectionStatus:  domain.BotConnectionStatusConnected,
		AgentCapabilityID: "cap_codex",
		AgentMode:         "session",
	})
	if err != nil {
		t.Fatal(err)
	}

	items, err := repo.ListWithAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 bot, got %d", len(items))
	}
	if items[0].AgentCapabilityID != "cap_codex" {
		t.Fatalf("unexpected listed agent capability id: %s", items[0].AgentCapabilityID)
	}
	if items[0].AgentMode != "session" {
		t.Fatalf("unexpected listed agent mode: %s", items[0].AgentMode)
	}
}

func TestAgentCapabilityRepositoryUpsertGetAndList(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewAgentCapabilityRepository(db)
	ctx := context.Background()

	first, err := repo.Upsert(ctx, domain.AgentCapability{
		ID:              "cap_codex",
		Key:             "codex",
		Label:           "Codex CLI",
		Command:         "codex",
		Args:            []string{"reply"},
		SupportedModes:  []string{"codex-exec", "session"},
		Available:       true,
		DetectionSource: "path_scan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Key != "codex" {
		t.Fatalf("unexpected key: %s", first.Key)
	}
	if !first.Available {
		t.Fatal("expected capability to be available")
	}

	updated, err := repo.Upsert(ctx, domain.AgentCapability{
		ID:              "cap_codex_new",
		Key:             "codex",
		Label:           "Codex CLI",
		Command:         "/usr/local/bin/codex",
		Args:            []string{"reply", "--plain"},
		SupportedModes:  []string{"codex-exec", "session"},
		Available:       false,
		DetectionSource: "path_scan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != "cap_codex" {
		t.Fatalf("expected upsert to keep original id, got %s", updated.ID)
	}
	if updated.Command != "/usr/local/bin/codex" {
		t.Fatalf("unexpected command: %s", updated.Command)
	}
	if updated.Available {
		t.Fatal("expected capability to be unavailable after update")
	}
	if len(updated.Args) != 2 || updated.Args[1] != "--plain" {
		t.Fatalf("unexpected args: %#v", updated.Args)
	}

	gotByID, err := repo.GetByID(ctx, "cap_codex")
	if err != nil {
		t.Fatal(err)
	}
	if gotByID.Key != "codex" {
		t.Fatalf("unexpected key from get by id: %s", gotByID.Key)
	}

	gotByKey, err := repo.GetByKey(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if gotByKey.Command != "/usr/local/bin/codex" {
		t.Fatalf("unexpected command from get by key: %s", gotByKey.Command)
	}

	items, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 capability, got %d", len(items))
	}
	if items[0].Key != "codex" {
		t.Fatalf("unexpected listed key: %s", items[0].Key)
	}
}

func TestAgentCapabilityRepositoryMissingRecords(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewAgentCapabilityRepository(db)
	ctx := context.Background()

	if _, err := repo.GetByID(ctx, "missing"); err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound from GetByID, got %v", err)
	}
	if _, err := repo.GetByKey(ctx, "missing"); err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound from GetByKey, got %v", err)
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

func TestBotRepositoryPreservesCLIAlias(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewBotRepository(db)
	ctx := context.Background()

	_, err := repo.Create(ctx, domain.Bot{
		ID:               "bot_alias_1",
		UserID:           "usr_1",
		Name:             "alias-bot",
		ChannelType:      "wechat",
		ConnectionStatus: domain.BotConnectionStatusLoginRequired,
		CLIAlias:         "cx",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, "bot_alias_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.CLIAlias != "cx" {
		t.Fatalf("CLIAlias after create = %q, want cx", got.CLIAlias)
	}

	got.CLIAlias = ""
	if _, err := repo.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, err := repo.GetByID(ctx, "bot_alias_1")
	if err != nil {
		t.Fatal(err)
	}
	if again.CLIAlias != "" {
		t.Fatalf("CLIAlias after clear = %q, want empty", again.CLIAlias)
	}
}

func TestBotRepositoryPreservesWorkspace(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewBotRepository(db)
	ctx := context.Background()
	if _, err := repo.Create(ctx, domain.Bot{
		ID: "bot_ws_1", UserID: "usr_1", Name: "ws-bot", ChannelType: "wechat",
		ConnectionStatus: domain.BotConnectionStatusLoginRequired, Workspace: "/data/bots/bot_ws_1/workspace",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, "bot_ws_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Workspace != "/data/bots/bot_ws_1/workspace" {
		t.Fatalf("Workspace = %q", got.Workspace)
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

func TestBotRepositorySystemPromptRoundTrip(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewBotRepository(db)
	ctx := context.Background()

	created, err := repo.Create(ctx, domain.Bot{
		ID:               "bot_sp",
		UserID:           "usr_1",
		Name:             "router",
		ChannelType:      "feishu",
		ConnectionStatus: domain.BotConnectionStatusLoginRequired,
		SystemPrompt:     "you are a router",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.SystemPrompt != "you are a router" {
		t.Fatalf("create system_prompt = %q", created.SystemPrompt)
	}

	got, err := repo.GetByID(ctx, "bot_sp")
	if err != nil {
		t.Fatal(err)
	}
	if got.SystemPrompt != "you are a router" {
		t.Fatalf("get system_prompt = %q", got.SystemPrompt)
	}

	got.SystemPrompt = ""
	updated, err := repo.Update(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	if updated.SystemPrompt != "" {
		t.Fatalf("after clear system_prompt = %q", updated.SystemPrompt)
	}
}
