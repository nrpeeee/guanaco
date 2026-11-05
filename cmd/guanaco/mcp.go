package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// A small MCP (Model Context Protocol) client over stdio. Servers are programs
// the user configures and approves; they are started on demand and stopped when
// Guanaco exits. Tool CALLS only ever happen after the user approves them in the
// browser (see /api/mcp/call) — the model can request, never execute.

type mcpCfg struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
}

type mcpTool struct {
	Server      string          `json:"server"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	ReadOnly    bool            `json:"read_only"`
}

type rpcMsg struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type mcpConn struct {
	cfg     mcpCfg
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	wmu     sync.Mutex
	mu      sync.Mutex
	next    int64
	pending map[int64]chan rpcMsg
	tools   []mcpTool
	dead    chan struct{}
	stderr  bytes.Buffer
	info    string
}

func (c *mcpConn) isDead() bool {
	select {
	case <-c.dead:
		return true
	default:
		return false
	}
}

func (c *mcpConn) tail() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := strings.TrimSpace(c.stderr.String())
	if len(s) > 600 {
		s = "…" + s[len(s)-600:]
	}
	return s
}

func (c *mcpConn) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

// mcpMaxLine caps one JSON-RPC message from a server. A server that streams
// more than this without a newline is misbehaving; we kill it rather than
// buffer without limit.
const mcpMaxLine = 16 << 20

var errMCPLineTooLong = errors.New("message from the tool server exceeded the size limit")

// readLine returns the next newline-terminated line, or errMCPLineTooLong once
// it has seen more than max bytes without reaching a newline.
func readLine(br *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(buf)+len(chunk) > max {
			return nil, errMCPLineTooLong
		}
		buf = append(buf, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return buf, err
	}
}

func (c *mcpConn) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := readLine(br, mcpMaxLine)
		if len(bytes.TrimSpace(line)) > 0 {
			var m rpcMsg
			if json.Unmarshal(line, &m) == nil {
				c.dispatch(m)
			}
		}
		if err != nil {
			if errors.Is(err, errMCPLineTooLong) {
				c.mu.Lock()
				c.stderr.WriteString(err.Error())
				c.mu.Unlock()
				if c.cmd != nil && c.cmd.Process != nil {
					c.cmd.Process.Kill()
				}
			}
			return
		}
	}
}

func (c *mcpConn) dispatch(m rpcMsg) {
	if m.Method != "" {
		if len(m.ID) == 0 {
			return // notification
		}
		// A request from the server: answer the few that are harmless, refuse the rest.
		reply := map[string]any{"jsonrpc": "2.0", "id": m.ID}
		switch m.Method {
		case "ping":
			reply["result"] = map[string]any{}
		case "roots/list":
			reply["result"] = map[string]any{"roots": []any{}}
		default:
			reply["error"] = map[string]any{"code": -32601, "message": "not supported by this client"}
		}
		c.send(reply)
		return
	}
	var id int64
	if json.Unmarshal(m.ID, &id) != nil {
		return
	}
	c.mu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ch != nil {
		ch <- m
	}
}

func (c *mcpConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan rpcMsg, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, errors.New(m.Error.Message)
		}
		return m.Result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-c.dead:
		msg := "server exited"
		if t := c.tail(); t != "" {
			msg += ": " + t
		}
		return nil, errors.New(msg)
	}
}

func (c *mcpConn) stop() {
	c.stdin.Close()
	select {
	case <-c.dead:
	case <-time.After(1500 * time.Millisecond):
		if c.cmd.Process != nil {
			c.cmd.Process.Kill()
		}
	}
}

func startMCP(ctx context.Context, cfg mcpCfg) (*mcpConn, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	hideWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	c := &mcpConn{cfg: cfg, cmd: cmd, stdin: stdin, pending: map[int64]chan rpcMsg{}, dead: make(chan struct{})}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("can't run %q: %v", cfg.Command, err)
	}
	go c.readLoop(stdout)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				c.mu.Lock()
				if c.stderr.Len() > 8000 {
					c.stderr.Reset()
				}
				c.stderr.Write(buf[:n])
				c.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { cmd.Wait(); close(c.dead) }()

	fail := func(err error) (*mcpConn, error) {
		c.stop()
		if t := c.tail(); t != "" {
			err = fmt.Errorf("%v — %s", err, t)
		}
		return nil, err
	}
	res, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "Guanaco", "version": "1"},
	})
	if err != nil {
		return fail(fmt.Errorf("handshake failed: %v", err))
	}
	var init struct {
		ServerInfo struct{ Name, Version string } `json:"serverInfo"`
	}
	json.Unmarshal(res, &init)
	c.info = strings.TrimSpace(init.ServerInfo.Name + " " + init.ServerInfo.Version)
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})

	cursor := ""
	for page := 0; page < 20; page++ {
		p := map[string]any{}
		if cursor != "" {
			p["cursor"] = cursor
		}
		res, err := c.call(ctx, "tools/list", p)
		if err != nil {
			return fail(fmt.Errorf("listing tools failed: %v", err))
		}
		var l struct {
			Tools []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"inputSchema"`
				Annotations struct {
					ReadOnlyHint bool `json:"readOnlyHint"`
				} `json:"annotations"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if json.Unmarshal(res, &l) != nil {
			break
		}
		for _, t := range l.Tools {
			schema := t.InputSchema
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			c.tools = append(c.tools, mcpTool{Server: cfg.ID, Name: t.Name, Description: t.Description, Schema: schema, ReadOnly: t.Annotations.ReadOnlyHint})
		}
		if l.NextCursor == "" {
			break
		}
		cursor = l.NextCursor
	}
	return c, nil
}

// ───────────────────────── hub ─────────────────────────

type mcpHub struct {
	path  string
	mu    sync.Mutex
	cfgs  []mcpCfg
	conns map[string]*mcpConn
	errs  map[string]string
}

var reSlug = regexp.MustCompile(`[^a-z0-9_-]+`)

func slug(s string) string {
	s = strings.Trim(reSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-")
	if len(s) > 32 {
		s = s[:32]
	}
	if s == "" {
		s = "server"
	}
	return s
}

func newMCPHub(dir string) (*mcpHub, error) {
	h := &mcpHub{path: filepath.Join(dir, "mcp.json"), conns: map[string]*mcpConn{}, errs: map[string]string{}}
	if b, err := os.ReadFile(h.path); err == nil {
		if err := json.Unmarshal(b, &h.cfgs); err != nil {
			return nil, fmt.Errorf("mcp.json is corrupt: %w", err)
		}
	}
	return h, nil
}

func (h *mcpHub) saveLocked() error {
	b, _ := json.MarshalIndent(h.cfgs, "", "  ")
	return writeAtomic(h.path, b)
}

type mcpStatus struct {
	mcpCfg
	Fp     string    `json:"fp"`     // hash of command+args+env: changes whenever the server is reconfigured
	Status string    `json:"status"` // running | stopped | error
	Error  string    `json:"error,omitempty"`
	Info   string    `json:"info,omitempty"`
	Tools  []mcpTool `json:"tools"`
}

func (h *mcpHub) List() []mcpStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []mcpStatus{}
	for _, c := range h.cfgs {
		s := mcpStatus{mcpCfg: c, Fp: cfgFingerprint(c), Status: "stopped", Tools: []mcpTool{}}
		if conn := h.conns[c.ID]; conn != nil && !conn.isDead() {
			s.Status, s.Tools, s.Info = "running", conn.tools, conn.info
		} else if e := h.errs[c.ID]; e != "" {
			s.Status, s.Error = "error", e
		}
		if s.Args == nil {
			s.Args = []string{}
		}
		out = append(out, s)
	}
	return out
}

func (h *mcpHub) Upsert(c mcpCfg) (mcpCfg, error) {
	c.Name, c.Command = strings.TrimSpace(c.Name), strings.TrimSpace(c.Command)
	if c.Name == "" || c.Command == "" {
		return c, errors.New("a name and a command are required")
	}
	if len(c.Command) > 500 || strings.ContainsRune(c.Command, 0) || len(c.Args) > 60 || len(c.Env) > 60 {
		return c, errors.New("command, arguments or environment too large")
	}
	for _, a := range c.Args {
		if len(a) > 4000 || strings.ContainsRune(a, 0) {
			return c, errors.New("argument too long")
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if c.ID == "" {
		base := slug(c.Name)
		c.ID = base
		for n := 2; h.find(c.ID) >= 0; n++ {
			c.ID = fmt.Sprintf("%s-%d", base, n)
		}
	}
	if !regexp.MustCompile(`^[a-z0-9_-]{1,40}$`).MatchString(c.ID) {
		return c, errors.New("bad server id")
	}
	old := append([]mcpCfg(nil), h.cfgs...)
	if i := h.find(c.ID); i >= 0 {
		h.cfgs[i] = c
	} else {
		h.cfgs = append(h.cfgs, c)
	}
	if err := h.saveLocked(); err != nil { // not saved: leave everything as it was
		h.cfgs = old
		return mcpCfg{}, err
	}
	if conn := h.conns[c.ID]; conn != nil { // config changed: restart on next use
		go conn.stop()
		delete(h.conns, c.ID)
	}
	delete(h.errs, c.ID)
	return c, nil
}

// cfgFingerprint identifies what a server actually runs. Per-tool "always
// allow" grants are tied to it, so editing the command (in the app or by hand
// in mcp.json) silently revokes them.
func cfgFingerprint(c mcpCfg) string {
	keys := make([]string, 0, len(c.Env))
	for k := range c.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	fmt.Fprintf(h, "%q\x00%q\x00", c.ID, c.Command)
	for _, a := range c.Args {
		fmt.Fprintf(h, "%q\x00", a)
	}
	for _, k := range keys {
		fmt.Fprintf(h, "%q=%q\x00", k, c.Env[k])
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func (h *mcpHub) find(id string) int {
	for i, c := range h.cfgs {
		if c.ID == id {
			return i
		}
	}
	return -1
}

func (h *mcpHub) Delete(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	i := h.find(id)
	if i < 0 {
		return errors.New("not found")
	}
	old := append([]mcpCfg(nil), h.cfgs...)
	h.cfgs = append(h.cfgs[:i], h.cfgs[i+1:]...)
	if err := h.saveLocked(); err != nil {
		h.cfgs = old
		return err
	}
	if conn := h.conns[id]; conn != nil {
		go conn.stop()
		delete(h.conns, id)
	}
	delete(h.errs, id)
	return nil
}

func (h *mcpHub) Stop(id string) {
	h.mu.Lock()
	conn := h.conns[id]
	delete(h.conns, id)
	h.mu.Unlock()
	if conn != nil {
		conn.stop()
	}
}

func (h *mcpHub) StopAll() {
	h.mu.Lock()
	var cs []*mcpConn
	for _, c := range h.conns {
		cs = append(cs, c)
	}
	h.conns = map[string]*mcpConn{}
	h.mu.Unlock()
	var wg sync.WaitGroup
	for _, c := range cs {
		wg.Add(1)
		go func(c *mcpConn) { defer wg.Done(); c.stop() }(c)
	}
	wg.Wait()
}

// Ensure returns a live connection, starting the server if needed.
func (h *mcpHub) Ensure(ctx context.Context, id string) (*mcpConn, error) {
	h.mu.Lock()
	i := h.find(id)
	if i < 0 {
		h.mu.Unlock()
		return nil, errors.New("unknown tool server")
	}
	cfg := h.cfgs[i]
	if c := h.conns[id]; c != nil && !c.isDead() {
		h.mu.Unlock()
		return c, nil
	}
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second) // first run of `npx` may download packages
	defer cancel()
	c, err := startMCP(ctx, cfg)
	h.mu.Lock()
	defer h.mu.Unlock()
	if err != nil {
		h.errs[id] = err.Error()
		return nil, err
	}
	if old := h.conns[id]; old != nil && !old.isDead() { // lost a race
		go c.stop()
		return old, nil
	}
	delete(h.errs, id)
	h.conns[id] = c
	return c, nil
}

// Tools returns the tools of every enabled server (starting them as needed).
func (h *mcpHub) Tools(ctx context.Context) []mcpTool {
	h.mu.Lock()
	var ids []string
	for _, c := range h.cfgs {
		if c.Enabled {
			ids = append(ids, c.ID)
		}
	}
	h.mu.Unlock()
	res := make([][]mcpTool, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			if c, err := h.Ensure(ctx, id); err == nil {
				res[i] = c.tools
			}
		}(i, id)
	}
	wg.Wait()
	var out []mcpTool
	for _, r := range res {
		out = append(out, r...)
	}
	return out
}

const mcpMaxResult = 20000

func (h *mcpHub) Call(ctx context.Context, server, tool string, args json.RawMessage) (string, bool, error) {
	c, err := h.Ensure(ctx, server)
	if err != nil {
		return "", false, err
	}
	known := false
	for _, t := range c.tools {
		if t.Name == tool {
			known = true
		}
	}
	if !known {
		return "", false, fmt.Errorf("%s has no tool %q", c.cfg.Name, tool)
	}
	var a any = map[string]any{}
	if len(args) > 0 && string(args) != "null" {
		a = args
	}
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	res, err := c.call(ctx, "tools/call", map[string]any{"name": tool, "arguments": a})
	if err != nil {
		return "", false, err
	}
	var out struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Resource *struct {
				Text string `json:"text"`
				URI  string `json:"uri"`
			} `json:"resource"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", false, errors.New("unexpected tool result")
	}
	var sb strings.Builder
	for _, p := range out.Content {
		switch {
		case p.Type == "text":
			sb.WriteString(p.Text)
		case p.Resource != nil && p.Resource.Text != "":
			sb.WriteString(p.Resource.Text)
		default:
			fmt.Fprintf(&sb, "[%s content omitted]", p.Type)
		}
		sb.WriteString("\n")
	}
	text := strings.TrimSpace(sb.String())
	if len(text) > mcpMaxResult {
		text = text[:mcpMaxResult] + "\n…[truncated]"
	}
	return text, out.IsError, nil
}

// nsName is the name the model sees: unique across servers and valid for every API.
func nsName(server, tool string) string {
	clean := func(s string) string { return regexp.MustCompile(`[^A-Za-z0-9_-]+`).ReplaceAllString(s, "_") }
	n := clean(server) + "__" + clean(tool)
	if len(n) > 64 {
		n = n[:64]
	}
	return n
}

// ───────────────────────── HTTP ─────────────────────────

func (a *App) mcpLocked(w http.ResponseWriter) bool {
	if a.allowLAN {
		jsonErr(w, 403, "tools are disabled in -lan mode")
		return true
	}
	return false
}

func (a *App) mcpList(w http.ResponseWriter, r *http.Request) {
	servers := a.mcp.List()
	if a.allowLAN {
		// Anyone on the network can read this. Environment variables and
		// arguments are where MCP servers take their tokens, so hide them;
		// tools are disabled in -lan mode anyway.
		for i := range servers {
			servers[i].mcpCfg = redactMCP(servers[i].mcpCfg)
		}
	}
	jsonOut(w, map[string]any{"servers": servers, "locked": a.allowLAN})
}

func redactMCP(c mcpCfg) mcpCfg {
	if len(c.Env) > 0 {
		env := make(map[string]string, len(c.Env))
		for k := range c.Env {
			env[k] = "••••"
		}
		c.Env = env
	}
	if len(c.Args) > 0 {
		c.Args = []string{"…"}
	}
	return c
}

func (a *App) mcpUpsert(w http.ResponseWriter, r *http.Request) {
	if a.mcpLocked(w) {
		return
	}
	var c mcpCfg
	if !decodeJSON(w, r, 1<<16, &c) {
		return
	}
	c, err := a.mcp.Upsert(c)
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	jsonOut(w, c)
}

func (a *App) mcpDelete(w http.ResponseWriter, r *http.Request) {
	if a.mcpLocked(w) {
		return
	}
	if err := a.mcp.Delete(r.PathValue("id")); err != nil {
		jsonErr(w, 404, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (a *App) mcpStart(w http.ResponseWriter, r *http.Request) {
	if a.mcpLocked(w) {
		return
	}
	id := r.PathValue("id")
	if _, err := a.mcp.Ensure(r.Context(), id); err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	for _, s := range a.mcp.List() {
		if s.ID == id {
			jsonOut(w, s)
			return
		}
	}
	w.WriteHeader(204)
}

func (a *App) mcpStop(w http.ResponseWriter, r *http.Request) {
	if a.mcpLocked(w) {
		return
	}
	a.mcp.Stop(r.PathValue("id"))
	w.WriteHeader(204)
}

func (a *App) mcpTools(w http.ResponseWriter, r *http.Request) {
	if a.allowLAN {
		jsonOut(w, map[string]any{"tools": []mcpTool{}})
		return
	}
	t := a.mcp.Tools(r.Context())
	if t == nil {
		t = []mcpTool{}
	}
	jsonOut(w, map[string]any{"tools": t})
}

func (a *App) mcpCall(w http.ResponseWriter, r *http.Request) {
	if a.mcpLocked(w) {
		return
	}
	var req struct {
		Server string          `json:"server"`
		Tool   string          `json:"tool"`
		Args   json.RawMessage `json:"args"`
	}
	if !decodeJSON(w, r, 4<<20, &req) {
		return
	}
	text, isErr, err := a.mcp.Call(r.Context(), req.Server, req.Tool, req.Args)
	if err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	jsonOut(w, map[string]any{"text": text, "is_error": isErr})
}
