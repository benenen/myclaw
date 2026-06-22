package main

import "time"

// PingOutput is the output schema for the ping tool.
type PingOutput struct {
	Time    string `json:"time" jsonschema:"the server time in RFC3339"`
	Message string `json:"message" jsonschema:"always 'pong'"`
}

// runPing is the pure tool logic, testable without a transport.
func runPing() PingOutput {
	return PingOutput{
		Time:    time.Now().UTC().Format(time.RFC3339),
		Message: "pong",
	}
}
