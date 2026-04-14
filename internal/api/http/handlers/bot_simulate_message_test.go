package handlers

import (
	"context"
	"encoding/json"
	stdhttp "net/http"
	"testing"

	httpapi "github.com/benenen/myclaw/internal/api/http"
	botapp "github.com/benenen/myclaw/internal/app/bot"
	"github.com/benenen/myclaw/internal/testutil"
)

type simulateMessageServiceStub struct {
	input  botapp.SimulateMessageInput
	output botapp.SimulateMessageOutput
	err    error
}

func (s *simulateMessageServiceStub) Simulate(ctx context.Context, input botapp.SimulateMessageInput) (botapp.SimulateMessageOutput, error) {
	s.input = input
	return s.output, s.err
}

func TestSimulateBotMessageHandlerReturnsEnvelope(t *testing.T) {
	stub := &simulateMessageServiceStub{
		output: botapp.SimulateMessageOutput{
			BotID:       "bot_1",
			From:        "user_1",
			Text:        "hello",
			MessageID:   "msg_1",
			RecipientID: "user_1",
		},
	}
	mux := stdhttp.NewServeMux()
	RegisterRoutes(mux, Dependencies{MessageSimulator: stub})

	rr := testutil.PostJSON(t, mux, "/api/v1/bots/simulate-message", `{"bot_id":"bot_1","from":"user_1","text":"hello"}`)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}
	var env httpapi.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Code != "OK" {
		t.Fatalf("unexpected code: %s", env.Code)
	}
	if stub.input.BotID != "bot_1" || stub.input.From != "user_1" || stub.input.Text != "hello" {
		t.Fatalf("unexpected input: %#v", stub.input)
	}
	payload, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data type: %T", env.Data)
	}
	if payload["message_id"] != "msg_1" {
		t.Fatalf("unexpected message id: %#v", payload["message_id"])
	}
	if payload["recipient_id"] != "user_1" {
		t.Fatalf("unexpected recipient id: %#v", payload["recipient_id"])
	}
}

func TestSimulateBotMessageHandlerRejectsMissingFields(t *testing.T) {
	mux := stdhttp.NewServeMux()
	RegisterRoutes(mux, Dependencies{MessageSimulator: &simulateMessageServiceStub{}})

	rr := testutil.PostJSON(t, mux, "/api/v1/bots/simulate-message", `{"bot_id":"bot_1"}`)
	testutil.AssertJSONCode(t, rr, "INVALID_ARGUMENT")
}
