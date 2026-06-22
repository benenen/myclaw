package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/benenen/myclaw/internal/domain"
)

const (
	TypeHTTP  = "http"
	TypeStdio = "stdio"
)

// Service provides validation and orchestration for MCP server management.
type Service struct {
	repo domain.MCPServerRepository
	bots domain.BotRepository
}

// NewService constructs a Service with the given repositories.
func NewService(repo domain.MCPServerRepository, bots domain.BotRepository) *Service {
	return &Service{repo: repo, bots: bots}
}

// CreateInput holds parameters for creating an MCP server entry.
type CreateInput struct {
	Name       string
	ServerType string
	URL        string
	Command    string
	Args       []string
}

// Create validates input and persists a new MCP server.
func (s *Service) Create(ctx context.Context, in CreateInput) (domain.MCPServer, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.MCPServer{}, fmt.Errorf("%w: name is required", domain.ErrInvalidArg)
	}
	serverType := strings.TrimSpace(in.ServerType)
	if serverType == "" {
		serverType = TypeHTTP
	}
	if serverType != TypeHTTP && serverType != TypeStdio {
		return domain.MCPServer{}, fmt.Errorf("%w: server_type must be http or stdio", domain.ErrInvalidArg)
	}
	if serverType == TypeHTTP && strings.TrimSpace(in.URL) == "" {
		return domain.MCPServer{}, fmt.Errorf("%w: url is required for http servers", domain.ErrInvalidArg)
	}
	if serverType == TypeStdio && strings.TrimSpace(in.Command) == "" {
		return domain.MCPServer{}, fmt.Errorf("%w: command is required for stdio servers", domain.ErrInvalidArg)
	}

	// Duplicate-name check — wrap raw driver errors as ErrInvalidArg.
	if _, err := s.repo.GetByName(ctx, name); err == nil {
		return domain.MCPServer{}, fmt.Errorf("%w: server %q already exists", domain.ErrInvalidArg, name)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.MCPServer{}, err
	}

	server := domain.MCPServer{
		ID:         domain.NewPrefixedID("mcp"),
		Name:       name,
		ServerType: serverType,
		URL:        strings.TrimSpace(in.URL),
		Command:    strings.TrimSpace(in.Command),
		Args:       append([]string(nil), in.Args...),
		Enabled:    true,
	}
	return s.repo.Create(ctx, server)
}

// List returns all MCP servers.
func (s *Service) List(ctx context.Context) ([]domain.MCPServer, error) {
	return s.repo.List(ctx)
}

// Remove deletes an MCP server by name.
func (s *Service) Remove(ctx context.Context, name string) error {
	server, err := s.repo.GetByName(ctx, strings.TrimSpace(name))
	if err != nil {
		return err
	}
	return s.repo.DeleteByID(ctx, server.ID)
}

// SetEnabled toggles the enabled flag on an MCP server by name.
func (s *Service) SetEnabled(ctx context.Context, name string, enabled bool) (domain.MCPServer, error) {
	server, err := s.repo.GetByName(ctx, strings.TrimSpace(name))
	if err != nil {
		return domain.MCPServer{}, err
	}
	server.Enabled = enabled
	return s.repo.Update(ctx, server)
}

// ListByBot returns all MCP servers attached to a bot.
func (s *Service) ListByBot(ctx context.Context, botID string) ([]domain.MCPServer, error) {
	return s.repo.ListByBot(ctx, botID)
}

// AttachToBot validates both the bot and server exist, then creates the join.
func (s *Service) AttachToBot(ctx context.Context, botID, serverName string) error {
	if err := s.requireBot(ctx, botID); err != nil {
		return err
	}
	server, err := s.repo.GetByName(ctx, strings.TrimSpace(serverName))
	if err != nil {
		return err
	}
	return s.repo.AttachToBot(ctx, botID, server.ID)
}

// DetachFromBot validates both the bot and server exist, then removes the join.
func (s *Service) DetachFromBot(ctx context.Context, botID, serverName string) error {
	if err := s.requireBot(ctx, botID); err != nil {
		return err
	}
	server, err := s.repo.GetByName(ctx, strings.TrimSpace(serverName))
	if err != nil {
		return err
	}
	return s.repo.DetachFromBot(ctx, botID, server.ID)
}

// SetBotServers replaces the full set of MCP servers for a bot.
// Returns ErrNotFound if the bot does not exist.
// Returns ErrInvalidArg if any server ID is not found.
func (s *Service) SetBotServers(ctx context.Context, botID string, serverIDs []string) error {
	if err := s.requireBot(ctx, botID); err != nil {
		return err
	}
	for _, id := range serverIDs {
		if _, err := s.repo.GetByID(ctx, id); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return fmt.Errorf("%w: mcp server %q not found", domain.ErrInvalidArg, id)
			}
			return err
		}
	}
	return s.repo.SetBotServers(ctx, botID, serverIDs)
}

// requireBot returns ErrNotFound (wrapped) if the bot does not exist.
func (s *Service) requireBot(ctx context.Context, botID string) error {
	if _, err := s.bots.GetByID(ctx, botID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: bot %q not found", domain.ErrNotFound, botID)
		}
		return err
	}
	return nil
}
