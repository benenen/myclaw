package httpchan

import (
	"fmt"
	"sync"

	"github.com/benenen/myclaw/internal/channel"
)

type IncomingMessage struct {
	UserID      string
	Text        string
	MessageID   string
	CallbackURL string
}

type botEntry struct {
	callbacks  channel.RuntimeCallbacks
	accountUID string
}

type Receiver struct {
	mu   sync.RWMutex
	bots map[string]botEntry
}

func NewReceiver() *Receiver {
	return &Receiver{
		bots: make(map[string]botEntry),
	}
}

func (r *Receiver) Register(botID string, callbacks channel.RuntimeCallbacks) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bots[botID] = botEntry{callbacks: callbacks}
}

func (r *Receiver) Unregister(botID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.bots, botID)
}

func (r *Receiver) Active(botID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.bots[botID]
	return ok
}

func (r *Receiver) Receive(botID string, msg IncomingMessage) error {
	r.mu.RLock()
	entry, ok := r.bots[botID]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("httpchan: bot %q is not active", botID)
	}

	if entry.callbacks.OnEvent == nil {
		return fmt.Errorf("httpchan: bot %q has no event callback", botID)
	}

	replyTarget := channel.ReplyTarget{
		ChannelType: ChannelType,
		RecipientID: msg.UserID,
		Metadata: map[string]string{
			"callback_url": msg.CallbackURL,
		},
	}

	entry.callbacks.OnEvent(channel.RuntimeEvent{
		BotID:       botID,
		ChannelType: ChannelType,
		MessageID:   msg.MessageID,
		From:        msg.UserID,
		Text:        msg.Text,
		ReplyTarget: replyTarget,
	})

	return nil
}
