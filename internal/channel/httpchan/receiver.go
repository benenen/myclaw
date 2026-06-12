package httpchan

import (
	"fmt"
	"sync"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
)

type IncomingMessage struct {
	UserID      string
	Text        string
	MessageID   string
	CallbackURL string
}

type botEntry struct {
	callbacks channel.RuntimeCallbacks
	accountUID string
}

type chatWaiter struct {
	replyCh chan agent.Response
}

type Receiver struct {
	mu   sync.RWMutex
	bots map[string]botEntry
	// chatWaiters maps bot_id:message_id -> waiter for synchronous chat replies
	chatWaiters map[string]*chatWaiter
}

func NewReceiver() *Receiver {
	return &Receiver{
		bots:        make(map[string]botEntry),
		chatWaiters: make(map[string]*chatWaiter),
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
	// Clean up any chat waiters for this bot
	for key := range r.chatWaiters {
		if len(key) > len(botID) && key[:len(botID)] == botID {
			delete(r.chatWaiters, key)
		}
	}
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
			"bot_id":       botID,
			"message_id":   msg.MessageID,
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

// RegisterChat creates a waiter keyed by bot_id:message_id and returns a
// channel that will receive the bot's reply. The caller should defer
// UnregisterChat to clean up.
func (r *Receiver) RegisterChat(botID, messageID string) (replyCh <-chan agent.Response, unregister func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := botID + ":" + messageID
	w := &chatWaiter{replyCh: make(chan agent.Response, 5)}
	r.chatWaiters[key] = w
	return w.replyCh, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if existing, ok := r.chatWaiters[key]; ok && existing == w {
			delete(r.chatWaiters, key)
		}
	}
}

// DeliverChatReply delivers a reply to a waiting chat session. The waiter
// is NOT removed after delivery to allow orchestrator bots to send an ack
// first and then the real answer — the last reply wins.
func (r *Receiver) DeliverChatReply(botID, messageID string, resp agent.Response) bool {
	r.mu.RLock()
	key := botID + ":" + messageID
	w, ok := r.chatWaiters[key]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	select {
	case w.replyCh <- resp:
	default:
	}
	return true
}
