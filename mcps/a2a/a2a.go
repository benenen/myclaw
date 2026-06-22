package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Server is one registry entry (the auth token stays internal).
type Server struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Endpoint    string `json:"endpoint"`
	AuthToken   string `json:"auth_token,omitempty"`
}

type Registry struct {
	servers []Server
}

func (r Registry) find(name string) (Server, bool) {
	for _, s := range r.servers {
		if s.Name == name {
			return s, true
		}
	}
	return Server{}, false
}

// loadRegistry reads a JSON array of servers from path. Empty path -> empty
// registry (no error); a missing/invalid file -> error (main treats it as empty).
func loadRegistry(path string) (Registry, error) {
	if path == "" {
		return Registry{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, fmt.Errorf("read a2a config %q: %w", path, err)
	}
	var servers []Server
	if err := json.Unmarshal(data, &servers); err != nil {
		return Registry{}, fmt.Errorf("parse a2a config %q: %w", path, err)
	}
	return Registry{servers: servers}, nil
}

// ---- a2a_list tool ----

type ListInput struct{}

type ServerView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Endpoint    string `json:"endpoint"`
}

type ListOutput struct {
	Servers []ServerView `json:"servers" jsonschema:"the A2A servers available to dispatch subtasks to"`
}

func runList(reg Registry) ListOutput {
	views := make([]ServerView, 0, len(reg.servers))
	for _, s := range reg.servers {
		views = append(views, ServerView{Name: s.Name, Description: s.Description, Endpoint: s.Endpoint})
	}
	return ListOutput{Servers: views}
}
