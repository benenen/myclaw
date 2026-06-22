package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRegistry(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "servers.json")
	if err := os.WriteFile(p, []byte(`[{"name":"w","description":"weather","endpoint":"http://x/a2a","auth_token":"sek"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := loadRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := reg.find("w")
	if !ok || s.Endpoint != "http://x/a2a" || s.AuthToken != "sek" {
		t.Fatalf("find: %+v ok=%v", s, ok)
	}
	if _, ok := reg.find("nope"); ok {
		t.Fatal("unexpected find")
	}
}

func TestLoadRegistryEmptyPath(t *testing.T) {
	reg, err := loadRegistry("")
	if err != nil || len(reg.servers) != 0 {
		t.Fatalf("empty path -> empty registry, got %+v err %v", reg, err)
	}
}

func TestLoadRegistryMissingFile(t *testing.T) {
	if _, err := loadRegistry("/no/such/file.json"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestRunListOmitsToken(t *testing.T) {
	reg := Registry{servers: []Server{{Name: "w", Description: "d", Endpoint: "e", AuthToken: "SECRET"}}}
	out := runList(reg)
	if len(out.Servers) != 1 || out.Servers[0].Name != "w" || out.Servers[0].Endpoint != "e" {
		t.Fatalf("bad list: %+v", out)
	}
	// ServerView has no token field — leaking the auth token is a compile error.
}
