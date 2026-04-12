package app

import (
	"context"
	"errors"
	"log"
	"sync"

	"github.com/benenen/myclaw/internal/channel"
	"github.com/benenen/myclaw/internal/domain"
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
}

func NewBotConnectionManager(bots domain.BotRepository, accounts domain.ChannelAccountRepository, starter channel.RuntimeStarter) *BotConnectionManager {
	return &BotConnectionManager{
		handles:  make(map[string]channel.RuntimeHandle),
		bots:     bots,
		accounts: accounts,
		starter:  starter,
	}
}

func NewBotConnectionManagerWithCipher(bots domain.BotRepository, accounts domain.ChannelAccountRepository, starter channel.RuntimeStarter, cipher *security.Cipher) *BotConnectionManager {
	return &BotConnectionManager{
		handles:  make(map[string]channel.RuntimeHandle),
		bots:     bots,
		accounts: accounts,
		starter:  starter,
		cipher:   cipher,
	}
}

func (m *BotConnectionManager) Start(ctx context.Context, botID string) error {
	m.mu.Lock()
	if _, exists := m.handles[botID]; exists {
		m.mu.Unlock()
		return ErrRuntimeAlreadyStarted
	}
	m.mu.Unlock()

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
					log.Printf("runtime_message bot_id=%s channel_type=%s message_id=%s from=%s text=%q", ev.BotID, ev.ChannelType, ev.MessageID, ev.From, ev.Text)
				},
				OnState: func(ev channel.RuntimeStateEvent) {
					m.handleState(bot, ev)
				},
			},
		}
	}

	handle, err := m.starter.StartRuntime(ctx, req)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.handles[botID]; exists {
		handle.Stop()
		return ErrRuntimeAlreadyStarted
	}
	m.handles[botID] = handle
	if m.bots != nil && m.accounts != nil {
		bot, err := m.bots.GetByID(ctx, botID)
		if err == nil && bot.ConnectionStatus == domain.BotConnectionStatusError {
			delete(m.handles, botID)
		}
	}
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
		bot.ConnectionStatus = domain.BotConnectionStatusError
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

var _ = log.Printf
