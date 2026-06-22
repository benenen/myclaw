# boo session capabilities Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enrich each boo-session A2A server's `description` from `<session-cwd>/boo.capabilities.json`, falling back to the session title when absent.

**Architecture:** Add three small file-reading helpers to `mcps/a2a/a2a.go` (`booConfigDir`, `booSessionCwd`, `booCapabilitiesDescription`) and use them in `resolve`'s boo expansion to compute each boo server's `Description`. All best-effort; failures fall back to the title. Tested with real files via `t.Setenv` + `t.TempDir`.

**Tech Stack:** Go 1.23, stdlib (`os`, `path/filepath`, `encoding/json`, `strings`).

Spec: `docs/superpowers/specs/2026-06-22-a2a-boo-capabilities-design.md`. Branch `feat/a2a-boo-capabilities` (off main). Only `mcps/a2a/{a2a.go,a2a_test.go}` change.

---

## Task 1: capability helpers + resolve enrichment

**Files:** Modify `mcps/a2a/a2a.go`, `mcps/a2a/a2a_test.go`.

- [ ] **Step 1: Write the failing tests**

Append to `a2a_test.go` (imports `os`, `path/filepath`, `context` already present from prior tasks; `testing` present):
```go
func TestBooConfigDir(t *testing.T) {
	t.Setenv("BOO_CONFIG", "/x/conf.toml")
	t.Setenv("XDG_CONFIG_HOME", "/y")
	if d := booConfigDir(); d != "/x" {
		t.Fatalf("BOO_CONFIG dir = %q, want /x", d)
	}
	t.Setenv("BOO_CONFIG", "")
	if d := booConfigDir(); d != "/y/boo" {
		t.Fatalf("XDG dir = %q, want /y/boo", d)
	}
}

func TestBooSessionCwd(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BOO_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", tmp)
	os.MkdirAll(filepath.Join(tmp, "boo"), 0o755)
	os.WriteFile(filepath.Join(tmp, "boo", "build.state"), []byte("/home/me/proj\n"), 0o600)

	cwd, ok := booSessionCwd("build")
	if !ok || cwd != "/home/me/proj" {
		t.Fatalf("cwd=%q ok=%v", cwd, ok)
	}
	if _, ok := booSessionCwd("ghost"); ok {
		t.Fatal("missing .state should be !ok")
	}
}

func TestBooCapabilitiesDescription(t *testing.T) {
	cwd := t.TempDir()
	os.WriteFile(filepath.Join(cwd, "boo.capabilities.json"),
		[]byte(`{"description":"coding agent","skills":["go","testing"]}`), 0o600)
	got, ok := booCapabilitiesDescription(cwd)
	if !ok || got != "coding agent [skills: go, testing]" {
		t.Fatalf("got %q ok=%v", got, ok)
	}

	cwd2 := t.TempDir()
	os.WriteFile(filepath.Join(cwd2, "boo.capabilities.json"), []byte(`{"description":"plain"}`), 0o600)
	if got, ok := booCapabilitiesDescription(cwd2); !ok || got != "plain" {
		t.Fatalf("plain: got %q ok=%v", got, ok)
	}

	if _, ok := booCapabilitiesDescription(t.TempDir()); ok {
		t.Fatal("missing file should be !ok")
	}
	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, "boo.capabilities.json"), []byte(`{not json`), 0o600)
	if _, ok := booCapabilitiesDescription(bad); ok {
		t.Fatal("invalid json should be !ok")
	}
}

func TestResolveBooEnrichesDescriptionFromCapabilities(t *testing.T) {
	defer stubBoo(func(args ...string) ([]byte, int, error) {
		return []byte(`[{"name":"build","title":"bash"}]`), 0, nil
	})()
	tmp := t.TempDir()
	t.Setenv("BOO_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", tmp)
	cwd := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "boo"), 0o755)
	os.WriteFile(filepath.Join(tmp, "boo", "build.state"), []byte(cwd+"\n"), 0o600)
	os.WriteFile(filepath.Join(cwd, "boo.capabilities.json"), []byte(`{"description":"go coder"}`), 0o600)

	got := resolve(context.Background(), []Source{{Kind: "boo"}})
	if len(got) != 1 || got[0].Description != "go coder" {
		t.Fatalf("enriched: %+v", got)
	}
}

func TestResolveBooFallsBackToTitleWhenNoCapabilities(t *testing.T) {
	defer stubBoo(func(args ...string) ([]byte, int, error) {
		return []byte(`[{"name":"build","title":"the title"}]`), 0, nil
	})()
	t.Setenv("BOO_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // empty boo dir → no .state
	got := resolve(context.Background(), []Source{{Kind: "boo"}})
	if len(got) != 1 || got[0].Description != "the title" {
		t.Fatalf("fallback: %+v", got)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`booConfigDir` etc. undefined): `go test ./mcps/a2a/`

- [ ] **Step 3: Add the helpers to `a2a.go`**

Add `"path/filepath"` to the import block. Add:
```go
// booConfigDir is where boo keeps per-session restore snapshots (<session>.state).
func booConfigDir() string {
	if c := os.Getenv("BOO_CONFIG"); c != "" {
		return filepath.Dir(c)
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "boo")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".config/boo"
	}
	return filepath.Join(home, ".config", "boo")
}

// booSessionCwd reads the session's saved working directory from its snapshot.
func booSessionCwd(session string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(booConfigDir(), session+".state"))
	if err != nil {
		return "", false
	}
	cwd := strings.TrimSpace(string(data))
	if cwd == "" {
		return "", false
	}
	return cwd, true
}

// booCapabilitiesDescription reads <cwd>/boo.capabilities.json and renders a
// description (with skills appended). Returns ("", false) if absent/invalid/empty.
func booCapabilitiesDescription(cwd string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(cwd, "boo.capabilities.json"))
	if err != nil {
		return "", false
	}
	var c struct {
		Description string   `json:"description"`
		Skills      []string `json:"skills"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return "", false
	}
	desc := strings.TrimSpace(c.Description)
	if len(c.Skills) > 0 {
		if desc != "" {
			desc += " "
		}
		desc += "[skills: " + strings.Join(c.Skills, ", ") + "]"
	}
	if desc == "" {
		return "", false
	}
	return desc, true
}
```

- [ ] **Step 4: Use it in `resolve`'s boo expansion**

In `resolve`, inside the per-session loop of the boo source, replace the `Description: sess.Title` with the enriched value. The loop currently does roughly:
```go
for _, sess := range sessions {
	add(ResolvedServer{Name: sess.Name, Description: sess.Title, Kind: kindBoo, Session: sess.Name, WaitTimeout: wt})
}
```
Change to:
```go
for _, sess := range sessions {
	desc := sess.Title
	if cwd, ok := booSessionCwd(sess.Name); ok {
		if cap, ok := booCapabilitiesDescription(cwd); ok {
			desc = cap
		}
	}
	add(ResolvedServer{Name: sess.Name, Description: desc, Kind: kindBoo, Session: sess.Name, WaitTimeout: wt})
}
```

- [ ] **Step 5: Run — expect PASS** + build/vet + no blast radius

```bash
cd /root/workspace/master/myclaw/mcps/a2a && go test ./... -v && go vet ./... && go build ./...
cd /root/workspace/master/myclaw && go build ./mcps/echo/... ./mcps/ping/... ./mcps/boo/... ./mcps/a2a/... && go test ./mcps/echo/... ./mcps/ping/... ./mcps/boo/... ./mcps/a2a/...
```

- [ ] **Step 6: Commit** (explicit paths; rm any stray `mcps/a2a/a2a` binary first)

```bash
cd /root/workspace/master/myclaw
git add mcps/a2a/a2a.go mcps/a2a/a2a_test.go
git commit -m "feat(mcps/a2a): enrich boo session description from boo.capabilities.json"
```
(Trailer: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.) `git show --stat HEAD` → only the 2 files.

---

## Task 2: real-boo smoke

**Files:** none (verification; fix-forward into `mcps/a2a` if the real `.state`/path handling differs).

- [ ] **Step 1: Build + a real session with a capabilities file**

```bash
cd /root/workspace/master/myclaw/mcps/a2a && go build -o /tmp/a2a-mcp .
CAPDIR=$(mktemp -d); printf '{"description":"smoke capability panel","skills":["alpha"]}' > "$CAPDIR/boo.capabilities.json"
boo new capsmoke -d --cwd "$CAPDIR" -- bash
sleep 1
cat ~/.config/boo/capsmoke.state   # should print $CAPDIR
```

- [ ] **Step 2: Confirm `a2a_list` shows the capability description**

Drive `/tmp/a2a-mcp --config <[{"kind":"boo"}]>` via the MCP handshake (newline-delimited JSON, go-sdk v0.8.0) and `tools/call a2a_list`; assert the `capsmoke` server's `description` == `"smoke capability panel [skills: alpha]"` (NOT the bash title). If a clean handshake is impractical, validate the helper path directly:
```bash
# what resolve() would compute:
CWD=$(cat ~/.config/boo/capsmoke.state); cat "$CWD/boo.capabilities.json"
```
Then create a SECOND session with NO capabilities file in its cwd and confirm `a2a_list` falls back to its title.
Paste the `a2a_list` (or direct) evidence. If the real `.state` content has trailing data beyond the cwd line or a different shape than "one path line", fix `booSessionCwd` (take the first line), add a test, and re-run `go test ./mcps/a2a/...`.

- [ ] **Step 3: Cleanup + optional fix-forward commit**

```bash
boo kill capsmoke   # + the second session
# only if Step 2 required a fix:
git add mcps/a2a/a2a.go mcps/a2a/a2a_test.go && git commit -m "fix(mcps/a2a): align boo .state cwd parsing with real boo"
```

---

## Self-Review

**Spec coverage:** cwd via `<booConfigDir>/<session>.state` (`booConfigDir` + `booSessionCwd`, Task 1 tests) ✓; capabilities from `<cwd>/boo.capabilities.json` folded into description + skills (`booCapabilitiesDescription`) ✓; fallback to title on any miss (`TestResolveBooFallsBackToTitleWhenNoCapabilities`) ✓; resolve enrichment ✓; no schema change (description only) ✓; real-boo smoke (Task 2) ✓. Out-of-scope (watching, AgentCard, structured skills field, auto-routing) absent.

**Placeholder scan:** Task 2's handshake step has a concrete fallback (validate the helper path directly) when no MCP-handshake helper exists; no "TBD".

**Type consistency:** `booConfigDir() string`, `booSessionCwd(string)(string,bool)`, `booCapabilitiesDescription(string)(string,bool)` consistent between helpers, the `resolve` call site, and the tests. `resolve`/`ResolvedServer`/`stubBoo` unchanged from prior tasks.
