package main

import "testing"

func TestServiceURLUsesLocalhostForWildcardAddress(t *testing.T) {
	got := serviceURL(":8080")

	if got != "http://localhost:8080" {
		t.Fatalf("serviceURL(:8080) = %q, want %q", got, "http://localhost:8080")
	}
}

func TestServiceURLPreservesExplicitHost(t *testing.T) {
	got := serviceURL("127.0.0.1:9090")

	if got != "http://127.0.0.1:9090" {
		t.Fatalf("serviceURL(127.0.0.1:9090) = %q, want %q", got, "http://127.0.0.1:9090")
	}
}
