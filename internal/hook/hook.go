package hook

import (
	"context"
	stdhttp "net/http"
)

// Hook defines the interface for platform-specific webhook handlers.
// Each implementation handles authentication, data extraction, and prompt
// generation for a specific external platform (e.g., vikunja, gitlab).
type Hook interface {
	// ID returns the platform identifier matching the URL segment (e.g., "vikunja").
	ID() string

	// Handle validates and processes the incoming webhook request.
	// It returns the prompt text to send to the agent for processing,
	// or an error if the request should be rejected.
	Handle(ctx context.Context, r *stdhttp.Request) (prompt string, err error)
}
