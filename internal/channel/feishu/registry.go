package feishu

import "sync"

// Registry holds the in-memory credentials for every active feishu bot,
// keyed by bot ID. The runtime registers on start and unregisters on stop;
// the reply gateway looks creds up by bot ID. Keeping the App Secret here
// (not in reply metadata) keeps it out of events, logs, and agent context.
type Registry struct {
	mu    sync.RWMutex
	creds map[string]Credentials
}

func NewRegistry() *Registry {
	return &Registry{creds: make(map[string]Credentials)}
}

func (r *Registry) Register(botID string, creds Credentials) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.creds[botID] = creds
}

func (r *Registry) Unregister(botID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.creds, botID)
}

func (r *Registry) Lookup(botID string) (Credentials, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.creds[botID]
	return c, ok
}
