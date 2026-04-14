package bot

import (
	"context"
	"errors"
	"sync"

	"github.com/benenen/myclaw/internal/channel"
	"github.com/benenen/myclaw/internal/channel/wechat"
	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/logging"
	"github.com/benenen/myclaw/internal/security"
)

var ErrRuntimeAlreadyStarted = errors.New("runtime already started")

type BotConnectionManager struct {
	mu       sync.Mutex
	handles  map[string]channel.RuntimeHandle
	bots     domain.BotRepository
	accounts domain.ChannelAccountRepository
	starter  channel.RuntimeStarter
	cipher   *security.Cipher
	logger   *logging.Logger
	onEvent  func(channel.RuntimeEvent)
}

func NewBotConnectionManager(bots domain.BotRepository, accounts domain.ChannelAccountRepository, starter channel.RuntimeStarter, logger *logging.Logger) *BotConnectionManager {
	return NewBotConnectionManagerWithCallbacks(bots, accounts, starter, nil, logger, nil)
}

func NewBotConnectionManagerWithCipher(bots domain.BotRepository, accounts domain.ChannelAccountRepository, starter channel.RuntimeStarter, cipher *security.Cipher, logger *logging.Logger) *BotConnectionManager {
	return NewBotConnectionManagerWithCallbacks(bots, accounts, starter, cipher, logger, nil)
}

func NewBotConnectionManagerWithCallbacks(bots domain.BotRepository, accounts domain.ChannelAccountRepository, starter channel.RuntimeStarter, cipher *security.Cipher, logger *logging.Logger, onEvent func(channel.RuntimeEvent)) *BotConnectionManager {
	return &BotConnectionManager{
		handles:  make(map[string]channel.RuntimeHandle),
		bots:     bots,
		accounts: accounts,
		starter:  starter,
		cipher:   cipher,
		logger:   logger,
		onEvent:  onEvent,
	}
}

func (m *BotConnectionManager) Start(ctx context.Context, botID string) error {
	m.mu.Lock()
	if _, exists := m.handles[botID]; exists {
		m.mu.Unlock()
		return ErrRuntimeAlreadyStarted
	}
	m.handles[botID] = nil
	m.mu.Unlock()

	cleanupReserved := true
	defer func() {
		if !cleanupReserved {
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if handle, exists := m.handles[botID]; exists && handle == nil {
			delete(m.handles, botID)
		}
	}()

	req := channel.StartRuntimeRequest{BotID: botID}
	if m.bots != nil && m.accounts != nil {
		bot, err := m.bots.GetByID(ctx, botID)
		if err != nil {
			return err
		}
		account, err := m.accounts.GetByID(ctx, bot.ChannelAccountID)
		if err != nil {
			return err
		}
		credentialPayload := account.CredentialCiphertext
		if m.cipher != nil {
			decrypted, err := m.cipher.Decrypt(account.CredentialCiphertext)
			if err != nil {
				return err
			}
			credentialPayload = decrypted
		}
		req = channel.StartRuntimeRequest{
			BotID:             bot.ID,
			ChannelType:       bot.ChannelType,
			AccountUID:        account.AccountUID,
			CredentialPayload: credentialPayload,
			CredentialVersion: account.CredentialVersion,
			Callbacks: channel.RuntimeCallbacks{
				OnEvent: func(ev channel.RuntimeEvent) {
					if m.logger != nil {
						m.logger.Info("runtime message", "bot_id", ev.BotID, "channel_type", ev.ChannelType, "message_id", ev.MessageID, "from", ev.From, "text", ev.Text, "context_token", ev.ReplyTarget.MetadataValue("context_token"))
					}
					if m.onEvent != nil {
						m.onEvent(ev)
					}
				},
				OnState: func(ev channel.RuntimeStateEvent) {
					m.handleState(bot, ev)
				},
			},
		}
	}

	runtimeCtx := context.Background()
	if ctx != nil {
		runtimeCtx = context.WithoutCancel(ctx)
	}
	handle, err := m.starter.StartRuntime(runtimeCtx, req)
	if err != nil {
		return err
	}

	m.mu.Lock()
	if current, exists := m.handles[botID]; !exists {
		m.mu.Unlock()
		handle.Stop()
		return ErrRuntimeAlreadyStarted
	} else if current != nil {
		m.mu.Unlock()
		handle.Stop()
		return ErrRuntimeAlreadyStarted
	}
	m.handles[botID] = handle
	cleanupReserved = false
	m.mu.Unlock()
	return nil
}

func (m *BotConnectionManager) remove(botID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.handles, botID)
}

func (m *BotConnectionManager) handleState(bot domain.Bot, ev channel.RuntimeStateEvent) {
	switch ev.State {
	case channel.RuntimeStateConnected:
		bot.ConnectionStatus = domain.BotConnectionStatusConnected
		bot.ConnectionError = ""
		_, _ = m.bots.Update(context.Background(), bot)
	case channel.RuntimeStateError:
		if errors.Is(ev.Err, wechat.ErrSessionExpired) {
			bot.ConnectionStatus = domain.BotConnectionStatusLoginRequired
		} else {
			bot.ConnectionStatus = domain.BotConnectionStatusError
		}
		if ev.Err != nil {
			bot.ConnectionError = ev.Err.Error()
		}
		_, _ = m.bots.Update(context.Background(), bot)
		m.remove(bot.ID)
	case channel.RuntimeStateStopped:
		m.remove(bot.ID)
	}
}

func (m *BotConnectionManager) Active(botID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.handles[botID]
	return ok
}
