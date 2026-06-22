package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/store/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MCPServerRepository struct {
	db *gorm.DB
}

func NewMCPServerRepository(db *gorm.DB) *MCPServerRepository {
	return &MCPServerRepository{db: db}
}

func (r *MCPServerRepository) Create(ctx context.Context, s domain.MCPServer) (domain.MCPServer, error) {
	now := time.Now().UTC()
	m, err := toModelMCPServer(s)
	if err != nil {
		return domain.MCPServer{}, err
	}
	m.CreatedAt = now
	m.UpdatedAt = now
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return domain.MCPServer{}, err
	}
	return toDomainMCPServer(m), nil
}

func (r *MCPServerRepository) GetByID(ctx context.Context, id string) (domain.MCPServer, error) {
	return r.first(ctx, "id = ?", id)
}

func (r *MCPServerRepository) GetByName(ctx context.Context, name string) (domain.MCPServer, error) {
	return r.first(ctx, "name = ?", name)
}

func (r *MCPServerRepository) first(ctx context.Context, query string, arg any) (domain.MCPServer, error) {
	var m models.MCPServer
	if err := r.db.WithContext(ctx).Where(query, arg).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.MCPServer{}, domain.ErrNotFound
		}
		return domain.MCPServer{}, err
	}
	return toDomainMCPServer(m), nil
}

func (r *MCPServerRepository) List(ctx context.Context) ([]domain.MCPServer, error) {
	var rows []models.MCPServer
	if err := r.db.WithContext(ctx).Order("name asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	return toDomainMCPServers(rows), nil
}

func (r *MCPServerRepository) Update(ctx context.Context, s domain.MCPServer) (domain.MCPServer, error) {
	m, err := toModelMCPServer(s)
	if err != nil {
		return domain.MCPServer{}, err
	}
	m.UpdatedAt = time.Now().UTC()
	if err := r.db.WithContext(ctx).Model(&models.MCPServer{}).Where("id = ?", s.ID).Updates(map[string]any{
		"name":        m.Name,
		"server_type": m.ServerType,
		"url":         m.URL,
		"command":     m.Command,
		"args_json":   m.ArgsJSON,
		"enabled":     m.Enabled,
		"updated_at":  m.UpdatedAt,
	}).Error; err != nil {
		return domain.MCPServer{}, err
	}
	return r.GetByID(ctx, s.ID)
}

func (r *MCPServerRepository) DeleteByID(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("mcp_server_id = ?", id).Delete(&models.BotMCPServer{}).Error; err != nil {
			return err
		}
		result := tx.Where("id = ?", id).Delete(&models.MCPServer{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return domain.ErrNotFound
		}
		return nil
	})
}

func toModelMCPServer(s domain.MCPServer) (models.MCPServer, error) {
	argsJSON := "[]"
	if len(s.Args) > 0 {
		data, err := json.Marshal(s.Args)
		if err != nil {
			return models.MCPServer{}, err
		}
		argsJSON = string(data)
	}
	enabled := s.Enabled
	return models.MCPServer{
		ID:         s.ID,
		Name:       s.Name,
		ServerType: s.ServerType,
		URL:        s.URL,
		Command:    s.Command,
		ArgsJSON:   argsJSON,
		Enabled:    &enabled,
	}, nil
}

func toDomainMCPServer(m models.MCPServer) domain.MCPServer {
	var args []string
	if m.ArgsJSON != "" && m.ArgsJSON != "[]" {
		_ = json.Unmarshal([]byte(m.ArgsJSON), &args)
	}
	if args == nil {
		args = []string{}
	}
	enabled := true
	if m.Enabled != nil {
		enabled = *m.Enabled
	}
	return domain.MCPServer{
		ID:         m.ID,
		Name:       m.Name,
		ServerType: m.ServerType,
		URL:        m.URL,
		Command:    m.Command,
		Args:       args,
		Enabled:    enabled,
		CreatedAt:  m.CreatedAt,
		UpdatedAt:  m.UpdatedAt,
	}
}

func toDomainMCPServers(rows []models.MCPServer) []domain.MCPServer {
	items := make([]domain.MCPServer, 0, len(rows))
	for _, row := range rows {
		items = append(items, toDomainMCPServer(row))
	}
	return items
}

func (r *MCPServerRepository) ListByBot(ctx context.Context, botID string) ([]domain.MCPServer, error) {
	return r.listJoined(ctx, botID, false)
}

func (r *MCPServerRepository) ListEnabledByBot(ctx context.Context, botID string) ([]domain.MCPServer, error) {
	return r.listJoined(ctx, botID, true)
}

func (r *MCPServerRepository) listJoined(ctx context.Context, botID string, onlyEnabled bool) ([]domain.MCPServer, error) {
	q := r.db.WithContext(ctx).
		Joins("JOIN bot_mcp_servers bms ON bms.mcp_server_id = mcp_servers.id").
		Where("bms.bot_id = ?", botID)
	if onlyEnabled {
		q = q.Where("mcp_servers.enabled = ?", true)
	}
	var rows []models.MCPServer
	if err := q.Order("mcp_servers.name asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	return toDomainMCPServers(rows), nil
}

func (r *MCPServerRepository) AttachToBot(ctx context.Context, botID, serverID string) error {
	m := models.BotMCPServer{BotID: botID, MCPServerID: serverID, CreatedAt: time.Now().UTC()}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&m).Error
}

func (r *MCPServerRepository) DetachFromBot(ctx context.Context, botID, serverID string) error {
	return r.db.WithContext(ctx).
		Where("bot_id = ? AND mcp_server_id = ?", botID, serverID).
		Delete(&models.BotMCPServer{}).Error
}

func (r *MCPServerRepository) SetBotServers(ctx context.Context, botID string, serverIDs []string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("bot_id = ?", botID).Delete(&models.BotMCPServer{}).Error; err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, id := range serverIDs {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			if err := tx.Create(&models.BotMCPServer{BotID: botID, MCPServerID: id, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

var _ domain.MCPServerRepository = (*MCPServerRepository)(nil)
