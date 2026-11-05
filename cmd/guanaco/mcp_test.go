package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestHelperMCP is not a test: when GUANACO_MCP_HELPER is set the test binary
// re-runs itself as a tiny fake MCP server (stdio, newline-delimited JSON-RPC).
func TestHelperMCP(t *testing.T) {
	mode := os.Getenv("GUANACO_MCP_HELPER")
	if mode == "" {
		t.Skip("helper process only")
	}
	in := bufio.NewReader(os.Stdin)
	out := os.Stdout
	reply := func(id json.RawMessage, result any) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		out.Write(append(b, '\n'))
	}
	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			os.Exit(0)
		}
		var m rpcMsg
		if json.Unmarshal(line, &m) != nil || len(m.ID) == 0 {
			continue
		}
		switch m.Method {
		case "initialize":
			if mode == "flood" { // never sends a newline
				chunk := []byte(strings.Repeat("x", 1<<20))
				for {
					if _, err := out.Write(chunk); err != nil {
						os.Exit(0)
					}
				}
			}
			reply(m.ID, map[string]any{"protocolVersion": "2024-11-05", "serverInfo": map[string]any{"name": "fake", "version": "1"}})
		case "tools/list":
			reply(m.ID, map[string]any{"tools": []any{map[string]any{
				"name": "echo", "description": "echoes", "inputSchema": map[string]any{"type": "object"},
				"annotations": map[string]any{"readOnlyHint": true},
			}}})
		case "tools/call":
			var p struct {
				Arguments map[string]any `json:"arguments"`
			}
			json.Unmarshal(m.Params, &p)
			txt, _ := p.Arguments["text"].(string)
			reply(m.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": "echo:" + txt}}})
		default:
			reply(m.ID, map[string]any{})
		}
	}
}

func helperCfg(mode string) mcpCfg {
	return mcpCfg{Name: "fake", Command: os.Args[0], Args: []string{"-test.run=^TestHelperMCP$"}, Env: map[string]string{"GUANACO_MCP_HELPER": mode}, Enabled: true}
}

func TestMCPClientEndToEnd(t *testing.T) {
	h, _ := newMCPHub(t.TempDir())
	cfg, err := h.Upsert(helperCfg("ok"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.StopAll()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tools := h.Tools(ctx)
	if len(tools) != 1 || tools[0].Name != "echo" || !tools[0].ReadOnly {
		t.Fatalf("tools = %+v", tools)
	}
	text, isErr, err := h.Call(ctx, cfg.ID, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil || isErr || text != "echo:hi" {
		t.Fatalf("call = %q %v %v", text, isErr, err)
	}
	if _, _, err := h.Call(ctx, cfg.ID, "no_such_tool", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unknown tool must be refused")
	}
}

func TestMCPOversizedMessageKillsServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	c, err := startMCP(ctx, mcpCfg{ID: "flood", Name: "flood", Command: helperCfg("flood").Command, Args: helperCfg("flood").Args, Env: helperCfg("flood").Env})
	if err == nil {
		c.stop()
		t.Fatal("a server that streams without a newline must fail the handshake")
	}
	if time.Since(start) > 25*time.Second {
		t.Fatal("took too long: the line cap did not trigger")
	}
}

func TestReadLineCap(t *testing.T) {
	br := bufio.NewReaderSize(strings.NewReader("short\n"+strings.Repeat("x", 5000)+"\n"), 64)
	if l, err := readLine(br, 100); err != nil || string(l) != "short\n" {
		t.Fatalf("first line = %q %v", l, err)
	}
	if _, err := readLine(br, 100); !errors.Is(err, errMCPLineTooLong) {
		t.Fatalf("long line err = %v, want errMCPLineTooLong", err)
	}
	// A line exactly at the limit is fine, and EOF without newline still returns the data.
	br = bufio.NewReaderSize(strings.NewReader(strings.Repeat("y", 100)), 16)
	l, err := readLine(br, 100)
	if len(l) != 100 || err != io.EOF {
		t.Fatalf("tail line len=%d err=%v", len(l), err)
	}
}

func TestCfgFingerprintChangesWithConfig(t *testing.T) {
	a := mcpCfg{ID: "s", Command: "npx", Args: []string{"-y", "pkg", "/home/me"}, Env: map[string]string{"K": "1"}}
	b := a
	if cfgFingerprint(a) != cfgFingerprint(b) {
		t.Fatal("same config must give the same fingerprint")
	}
	for name, mut := range map[string]func(*mcpCfg){
		"command": func(c *mcpCfg) { c.Command = "node" },
		"arg":     func(c *mcpCfg) { c.Args = []string{"-y", "pkg", "/"} },
		"env":     func(c *mcpCfg) { c.Env = map[string]string{"K": "2"} },
		"id":      func(c *mcpCfg) { c.ID = "t" },
	} {
		c := a
		c.Args = append([]string(nil), a.Args...)
		mut(&c)
		if cfgFingerprint(c) == cfgFingerprint(a) {
			t.Errorf("changing %s must change the fingerprint", name)
		}
	}
	// "a b" as one argument must differ from "a","b".
	x := mcpCfg{ID: "s", Command: "c", Args: []string{"a b"}}
	y := mcpCfg{ID: "s", Command: "c", Args: []string{"a", "b"}}
	if cfgFingerprint(x) == cfgFingerprint(y) {
		t.Fatal("argument boundaries must be part of the fingerprint")
	}
}

// In -lan mode anyone on the network can list tool servers: their secrets
// (environment values, arguments) must not be in the response, and the stored
// config must be left untouched.
func TestMCPListRedactedInLANMode(t *testing.T) {
	hub, err := newMCPHub(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Upsert(mcpCfg{Name: "gh", Command: "npx", Args: []string{"--token", "ghp_secret"}, Env: map[string]string{"GITHUB_TOKEN": "ghp_secret"}}); err != nil {
		t.Fatal(err)
	}
	for _, lan := range []bool{false, true} {
		a := &App{mcp: hub, allowLAN: lan}
		rec := httptest.NewRecorder()
		a.mcpList(rec, httptest.NewRequest("GET", "/api/mcp", nil))
		leaked := strings.Contains(rec.Body.String(), "ghp_secret")
		if lan && leaked {
			t.Errorf("-lan response leaks secrets: %s", rec.Body.String())
		}
		if !lan && !leaked {
			t.Errorf("local response should keep the config editable: %s", rec.Body.String())
		}
	}
	if hub.List()[0].Env["GITHUB_TOKEN"] != "ghp_secret" {
		t.Fatal("redaction modified the stored config")
	}
}
