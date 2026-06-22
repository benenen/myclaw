package mcpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/store/repositories"
	"github.com/benenen/myclaw/internal/testutil"
)

func newSvc(t *testing.T) *Service {
	t.Helper()
	db := testutil.OpenTestDB(t)
	return NewService(
		repositories.NewMCPServerRepository(db),
		repositories.NewBotRepository(db),
	)
}

func TestServiceCreateValidation(t *testing.T) {
	svc := newSvc(t)
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
	svc := newSvc(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, CreateInput{Name: "srv", ServerType: "http", URL: "http://x"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.AttachToBot(ctx, "nope", "srv"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("attach to unknown bot should be ErrNotFound, got %v", err)
	}
}
