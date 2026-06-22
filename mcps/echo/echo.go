package main

// EchoInput is the input schema for the echo tool.
type EchoInput struct {
	Text string `json:"text" jsonschema:"the text to echo back"`
}

// EchoOutput is the output schema for the echo tool.
type EchoOutput struct {
	Text string `json:"text" jsonschema:"the echoed text"`
}

// runEcho is the pure tool logic, testable without a transport.
func runEcho(in EchoInput) EchoOutput {
	return EchoOutput{Text: in.Text}
}
