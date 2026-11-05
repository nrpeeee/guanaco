package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// No overall timeout (responses stream for minutes), but a dial timeout so a
// wrong -ollama host fails in seconds instead of hanging on the OS default.
var ollamaClient = &http.Client{
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     90 * time.Second,
		MaxIdleConnsPerHost: 8,
	},
}

func (a *App) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("GET /api/shutdown-token", a.shutdownInfo)
	mux.HandleFunc("POST /api/tabs/alive", a.tabAlive)

	// Ollama-only model management
	mux.HandleFunc("GET /api/models", a.proxyGET("/api/tags"))
	mux.HandleFunc("GET /api/ps", a.proxyGET("/api/ps"))
	mux.HandleFunc("POST /api/show", a.proxyPOST("/api/show"))
	mux.HandleFunc("POST /api/pull", a.pull)
	mux.HandleFunc("POST /api/delete-model", a.deleteModel)
	mux.HandleFunc("POST /api/unload", a.unloadModel)
	mux.HandleFunc("POST /api/ollama/detect", a.detectOllamaHandler)
	mux.HandleFunc("POST /api/ollama/start", a.startOllamaHandler)

	// unified, provider-aware
	mux.HandleFunc("GET /api/catalog", a.catalog)
	mux.HandleFunc("POST /api/chat", a.chat)
	mux.HandleFunc("POST /api/title", a.title)
	mux.HandleFunc("POST /api/search", a.searchOnly)

	mux.HandleFunc("POST /api/transcribe", a.transcribe)

	mux.HandleFunc("GET /api/mcp", a.mcpList)
	mux.HandleFunc("POST /api/mcp/servers", a.mcpUpsert)
	mux.HandleFunc("DELETE /api/mcp/servers/{id}", a.mcpDelete)
	mux.HandleFunc("POST /api/mcp/servers/{id}/start", a.mcpStart)
	mux.HandleFunc("POST /api/mcp/servers/{id}/stop", a.mcpStop)
	mux.HandleFunc("GET /api/mcp/tools", a.mcpTools)
	mux.HandleFunc("POST /api/mcp/call", a.mcpCall)

	mux.HandleFunc("GET /api/rag", a.ragList)
	mux.HandleFunc("POST /api/rag/docs", a.ragAdd)
	mux.HandleFunc("PUT /api/rag/docs/{id}", a.ragPut)
	mux.HandleFunc("DELETE /api/rag/docs/{id}", a.ragDelete)
	mux.HandleFunc("POST /api/rag/test", a.ragTest)

	// provider configuration
	mux.HandleFunc("GET /api/providers", a.listProviders)
	mux.HandleFunc("POST /api/providers", a.addProvider)
	mux.HandleFunc("PUT /api/providers/{id}", a.updateProvider)
	mux.HandleFunc("DELETE /api/providers/{id}", a.removeProvider)
	mux.HandleFunc("PUT /api/providers/{id}/key", a.setKey)
	mux.HandleFunc("DELETE /api/providers/{id}/key", a.deleteKey)
	mux.HandleFunc("POST /api/providers/{id}/test", a.testProvider)

	mux.HandleFunc("GET /api/chats", a.listChats)
	mux.HandleFunc("GET /api/chats/{id}", a.getChat)
	mux.HandleFunc("PUT /api/chats/{id}", a.putChat)
	mux.HandleFunc("DELETE /api/chats/{id}", a.deleteChat)
	mux.HandleFunc("GET /api/chats/{id}/export", a.exportChat)
	mux.HandleFunc("GET /api/export", a.exportAll)
	mux.HandleFunc("POST /api/import", a.importChats)
	mux.HandleFunc("GET /api/backup", a.backup)
	mux.HandleFunc("POST /api/restore", a.restore)

	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(a.store.GetSettings())
	})
	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		b, ok := readBody(w, r, 4<<20)
		if !ok {
			return
		}
		if a.allowLAN {
			var err error
			if b, err = a.pinLANSettings(b); err != nil {
				jsonErr(w, 400, err.Error())
				return
			}
		}
		if err := a.store.PutSettings(b); err != nil {
			jsonErr(w, 400, err.Error())
			return
		}
		w.WriteHeader(204)
	})

	// /shutdown is outside /api/ so sendBeacon can hit it without the X-header.
	mux.HandleFunc("POST /shutdown", a.shutdownHandler)
}

// ---------- helpers ----------

// shutdownInfo hands the page the per-run token. It is served from /api/, so
// the guard's X-header applies and a foreign page cannot read it.
//
// In -lan mode there is no token: other people's tabs must not be able to stop
// the server, so closing a tab never shuts it down there.
func (a *App) shutdownInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	tok := a.shutdownToken
	if a.allowLAN {
		tok = ""
	}
	jsonOut(w, map[string]string{"token": tok})
}

// shutdownHandler lets the browser signal a clean exit via
// navigator.sendBeacon('/shutdown') when the tab closes, so the process doesn't
// linger in the background. It must live outside /api/ because sendBeacon
// cannot set the X-header, so it authenticates with the run token
// instead — without that, any website could POST here and kill the server.
func (a *App) shutdownHandler(w http.ResponseWriter, r *http.Request) {
	if a.allowLAN {
		http.Error(w, "disabled in -lan mode", http.StatusForbidden)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<10))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var in struct {
		Token string `json:"token"`
		Tab   string `json:"tab"`
	}
	if json.Unmarshal(raw, &in) != nil ||
		subtle.ConstantTimeCompare([]byte(in.Token), []byte(a.shutdownToken)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusNoContent)
	// A reload fires the same beacon, and other tabs may still be open: exit
	// only once neither is the case (see tabs.go).
	now := time.Now()
	a.tabs.closed(in.Tab)
	loads := a.pageLoads.Load()
	go a.waitForExit(now, loads, func() {
		a.mcp.StopAll()
		os.Exit(0)
	})
}

func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func ollamaError(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		return e.Error
	}
	return fmt.Sprintf("ollama: %s %s", resp.Status, strings.TrimSpace(string(b)))
}

func (a *App) proxyGET(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", a.oll.Get()+path, nil)
		resp, err := ollamaClient.Do(req)
		if err != nil {
			jsonErr(w, 502, "cannot reach Ollama at "+a.oll.Get())
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}

func (a *App) proxyPOST(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := readBody(w, r, 1<<20)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "POST", a.oll.Get()+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := ollamaClient.Do(req)
		if err != nil {
			jsonErr(w, 502, "cannot reach Ollama at "+a.oll.Get())
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", a.oll.Get()+"/api/version", nil)
	resp, err := ollamaClient.Do(req)
	out := map[string]any{"app": version, "ollama_url": a.oll.Get(), "ok": false, "lan": a.allowLAN, "installed": findOllamaBinary() != ""}
	if err == nil {
		defer resp.Body.Close()
		var v struct {
			Version string `json:"version"`
		}
		json.NewDecoder(resp.Body).Decode(&v)
		out["ok"] = resp.StatusCode == 200
		out["ollama"] = v.Version
	}
	jsonOut(w, out)
}

// ---------- providers ----------

type providerView struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	Local    bool     `json:"local"`
	Enabled  bool     `json:"enabled"`
	BaseURL  string   `json:"base_url"`
	Default  string   `json:"default_base"`
	NeedsKey bool     `json:"needs_key"`
	KeySet   bool     `json:"key_set"`
	KeyHint  string   `json:"key_hint,omitempty"`
	KeySrc   string   `json:"key_source,omitempty"`
	Params   []string `json:"params"`
	Custom   bool     `json:"custom"`
	Blocked  string   `json:"blocked,omitempty"`
}

func (a *App) view(c ProviderConfig) providerView {
	key, src := a.reg.KeyFor(c)
	v := providerView{ID: c.ID, Kind: c.Kind, Name: c.Name, Local: c.isLocal(), Enabled: c.Enabled, BaseURL: c.BaseURL,
		Default: c.spec().DefaultBase, NeedsKey: c.spec().NeedsKey, KeySet: key != "", KeySrc: src,
		Params: c.spec().Params, Custom: c.Kind == "openai-compatible"}
	if c.Kind == "ollama" {
		v.BaseURL = a.oll.Get()
	}
	if key != "" {
		v.KeyHint = keyHint(key)
	}
	if a.allowLAN && !a.reg.lanCloud && !c.isLocal() {
		v.Blocked = "blocked in -lan mode"
	}
	return v
}

func (a *App) listProviders(w http.ResponseWriter, r *http.Request) {
	var out []providerView
	for _, c := range a.reg.Configs() {
		out = append(out, a.view(c))
	}
	jsonOut(w, map[string]any{"providers": out, "protection": credProtection(a.keyDir), "lan": a.allowLAN})
}

// In -lan mode nobody on the network may change provider config or keys:
// redirecting a base URL would leak the stored key to their server.
func (a *App) configLocked(w http.ResponseWriter) bool {
	if a.allowLAN {
		jsonErr(w, 403, "provider settings are read-only in -lan mode")
		return true
	}
	return false
}

func (a *App) addProvider(w http.ResponseWriter, r *http.Request) {
	if a.configLocked(w) {
		return
	}
	var in struct {
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		Local   bool   `json:"local"`
		Key     string `json:"key"`
	}
	if !decodeJSON(w, r, 1<<16, &in) {
		return
	}
	in.Name, in.BaseURL = strings.TrimSpace(in.Name), strings.TrimSpace(in.BaseURL)
	if in.BaseURL == "" {
		jsonErr(w, 400, "base URL is required")
		return
	}
	if err := validBaseURL(in.BaseURL); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if in.Name == "" {
		in.Name = domainOf(in.BaseURL)
	}
	c, err := a.reg.Add(ProviderConfig{Kind: "openai-compatible", Name: truncateRunes(in.Name, 40), BaseURL: in.BaseURL, Enabled: true, Local: boolp(in.Local)})
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if strings.TrimSpace(in.Key) != "" {
		if err := a.creds.Set(c.ID, in.Key); err != nil {
			jsonErr(w, 400, err.Error())
			return
		}
	}
	jsonOut(w, a.view(c))
}

func (a *App) updateProvider(w http.ResponseWriter, r *http.Request) {
	if a.configLocked(w) {
		return
	}
	var in struct {
		Name    *string `json:"name"`
		BaseURL *string `json:"base_url"`
		Enabled *bool   `json:"enabled"`
		Local   *bool   `json:"local"`
	}
	if !decodeJSON(w, r, 1<<16, &in) {
		return
	}
	c, err := a.reg.Update(r.PathValue("id"), func(c *ProviderConfig) error {
		if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
			c.Name = truncateRunes(strings.TrimSpace(*in.Name), 40)
		}
		if in.BaseURL != nil && c.Kind != "ollama" {
			u := strings.TrimSpace(*in.BaseURL)
			if err := validBaseURL(u); err != nil {
				return err
			}
			if u == "" && c.Kind == "openai-compatible" {
				return fmt.Errorf("base URL is required")
			}
			c.BaseURL = u
		}
		if in.Enabled != nil && c.Kind != "ollama" {
			c.Enabled = *in.Enabled
		}
		if in.Local != nil && c.Kind == "openai-compatible" {
			c.Local = in.Local
		}
		return nil
	})
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	jsonOut(w, a.view(c))
}

func (a *App) removeProvider(w http.ResponseWriter, r *http.Request) {
	if a.configLocked(w) {
		return
	}
	if err := a.reg.Remove(r.PathValue("id")); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (a *App) setKey(w http.ResponseWriter, r *http.Request) {
	if a.configLocked(w) {
		return
	}
	id := r.PathValue("id")
	c, ok := a.reg.Config(id)
	if !ok || c.Kind == "ollama" {
		jsonErr(w, 404, "no such provider")
		return
	}
	var in struct {
		Key string `json:"key"`
	}
	if !decodeJSON(w, r, 1<<16, &in) {
		return
	}
	if err := a.creds.Set(id, in.Key); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	// Saving a key is an explicit opt-in: enable the provider.
	c, _ = a.reg.Update(id, func(c *ProviderConfig) error { c.Enabled = true; return nil })
	jsonOut(w, a.view(c))
}

func (a *App) deleteKey(w http.ResponseWriter, r *http.Request) {
	if a.configLocked(w) {
		return
	}
	id := r.PathValue("id")
	if err := a.creds.Delete(id); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	a.reg.invalidate(id)
	c, _ := a.reg.Config(id)
	jsonOut(w, a.view(c))
}

func (a *App) testProvider(w http.ResponseWriter, r *http.Request) {
	if a.configLocked(w) {
		return
	}
	c, ok := a.reg.Config(r.PathValue("id"))
	if !ok {
		jsonErr(w, 404, "no such provider")
		return
	}
	p, _, err := a.reg.build(c, false)
	if err != nil {
		jsonOut(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	t := time.Now()
	ms, err := p.Models(ctx)
	if err != nil {
		jsonOut(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	a.reg.invalidate(c.ID)
	jsonOut(w, map[string]any{"ok": true, "models": len(ms), "ms": time.Since(t).Milliseconds()})
}

// catalog merges the model lists of every enabled provider. Each provider
// fails independently; errors are reported alongside the models.
func (a *App) catalog(w http.ResponseWriter, r *http.Request) {
	fresh := r.URL.Query().Get("fresh") == "1"
	type result struct {
		id     string
		models []ModelEntry
		err    error
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []result
	for _, c := range a.reg.Configs() {
		if !c.Enabled {
			continue
		}
		wg.Add(1)
		go func(c ProviderConfig) {
			defer wg.Done()
			ms, err := a.reg.Models(r.Context(), c, fresh)
			mu.Lock()
			results = append(results, result{c.ID, ms, err})
			mu.Unlock()
		}(c)
	}
	wg.Wait()
	byID := make(map[string]result, len(results))
	for _, res := range results {
		byID[res.id] = res
	}
	models := []ModelEntry{}
	errs := map[string]string{}
	for _, c := range a.reg.Configs() { // keep registry order
		res, ok := byID[c.ID]
		if !ok {
			continue
		}
		if res.err != nil {
			errs[c.ID] = res.err.Error()
		}
		for _, m := range res.models {
			m.Local = c.isLocal()
			models = append(models, m)
		}
	}
	jsonOut(w, map[string]any{"models": models, "errors": errs})
}

// ---------- streaming chat ----------

type chatReq struct {
	Provider  string      `json:"provider"`
	Model     string      `json:"model"`
	Messages  []Message   `json:"messages"`
	Params    GenParams   `json:"params"`
	Think     *bool       `json:"think,omitempty"`
	KeepAlive string      `json:"keep_alive,omitempty"`
	Search    *searchOpts `json:"search,omitempty"`
	RAG       *ragOpts    `json:"rag,omitempty"`
	Tools     bool        `json:"tools,omitempty"`
}

type ragOpts struct {
	Enabled    bool `json:"enabled"`
	K          int  `json:"k,omitempty"`
	AllowCloud bool `json:"allow_cloud,omitempty"` // user opted in to sending excerpts to a cloud chat model
}

type searchOpts struct {
	Enabled  bool   `json:"enabled"`
	Query    string `json:"query,omitempty"`
	Results  int    `json:"results,omitempty"`
	Engine   string `json:"engine,omitempty"` // "duckduckgo" | "searxng"
	SearxURL string `json:"searx_url,omitempty"`
	Rewrite  bool   `json:"rewrite,omitempty"` // ask the model for a search query first
}

// providerID defaults to Ollama for requests from older clients. That is the
// ONLY default: anything named explicitly must resolve exactly or fail.
func providerID(id string) string {
	if id == "" {
		return "ollama"
	}
	return id
}

// Events streamed to the browser as NDJSON (unchanged across providers):
//
//	{"t":"status","c":"Searching the web…"}
//	{"t":"sources","sources":[...]}
//	{"t":"docs","docs":[...],"skipped":"reason"}
//	{"t":"tool_calls","calls":[{id,ns,server,tool,args,read_only}]}
//	{"t":"delta","c":"text","th":"thinking text"}
//	{"t":"done","stats":{...}}
//	{"t":"error","c":"message"}
func (a *App) chat(w http.ResponseWriter, r *http.Request) {
	var cr chatReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<20)).Decode(&cr); err != nil {
		jsonErr(w, 400, "bad request: "+err.Error())
		return
	}
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	enc := json.NewEncoder(w)
	emit := func(v any) {
		enc.Encode(v)
		if flusher != nil {
			flusher.Flush()
		}
	}

	prov, cfg, err := a.reg.Get(providerID(cr.Provider))
	if err != nil {
		emit(map[string]any{"t": "error", "c": err.Error()})
		return
	}
	if strings.TrimSpace(cr.Model) == "" {
		emit(map[string]any{"t": "error", "c": "no model selected"})
		return
	}
	if !has(cfg.spec().Params, "keep_alive") {
		cr.KeepAlive = ""
	}
	cr.Params = filterParams(cr.Params, cfg.spec().Params)

	msgs := cr.Messages
	if cr.RAG != nil && cr.RAG.Enabled && len(msgs) > 0 && msgs[len(msgs)-1].Role == "user" {
		last := msgs[len(msgs)-1]
		if !cfg.isLocal() && !cr.RAG.AllowCloud {
			emit(map[string]any{"t": "docs", "docs": []ragHit{}, "skipped": "Documents were not used: " + cfg.Name + " is a cloud model and sending document excerpts there is off (Preferences → Documents)."})
		} else if q := truncateRunes(strings.TrimSpace(last.Content), 1000); q != "" && len(a.rag.List()) > 0 {
			emit(map[string]any{"t": "status", "c": "Searching your documents…"})
			k := cr.RAG.K
			if k <= 0 || k > 12 {
				k = 5
			}
			hits, notes := a.ragSearch(r.Context(), q, k)
			ev := map[string]any{"t": "docs", "docs": hits}
			if len(hits) == 0 && len(notes) > 0 {
				ev["skipped"] = "Documents were not used: " + notes[0]
			}
			emit(ev)
			if len(hits) > 0 {
				ctxMsg := Message{Role: "system", Content: buildRagContext(hits)}
				msgs = append(append(append([]Message{}, msgs[:len(msgs)-1]...), ctxMsg), last)
			}
		}
	}
	if cr.Search != nil && a.allowLAN {
		cr.Search.SearxURL = a.savedSetting("searxUrl") // see lanpin.go
	}
	if cr.Search != nil && cr.Search.Enabled && len(msgs) > 0 && msgs[len(msgs)-1].Role == "user" {
		last := msgs[len(msgs)-1]
		q := strings.TrimSpace(cr.Search.Query)
		if q == "" && cr.Search.Rewrite {
			emit(map[string]any{"t": "status", "c": "Writing search query…"})
			q = a.rewriteQuery(r.Context(), prov, cr.Model, msgs)
		}
		if q == "" {
			q = last.Content
		}
		q = truncateRunes(strings.TrimSpace(q), 300)
		emit(map[string]any{"t": "status", "c": "Searching the web for “" + truncateRunes(q, 80) + "”"})
		n := cr.Search.Results
		if n <= 0 || n > 8 {
			n = 4
		}
		results, err := webSearch(r.Context(), q, n, cr.Search.Engine, cr.Search.SearxURL)
		if err != nil {
			emit(map[string]any{"t": "status", "c": "Search failed: " + err.Error()})
		} else if len(results) > 0 {
			emit(map[string]any{"t": "status", "c": fmt.Sprintf("Reading %d pages…", len(results))})
			fetchPages(r.Context(), results)
			slim := make([]SearchResult, len(results))
			for i, s := range results {
				slim[i] = SearchResult{Title: s.Title, URL: s.URL, Snippet: s.Snippet}
			}
			emit(map[string]any{"t": "sources", "query": q, "sources": slim})
			ctxMsg := Message{Role: "system", Content: buildSearchContext(q, results)}
			msgs = append(append(append([]Message{}, msgs[:len(msgs)-1]...), ctxMsg), last)
		} else {
			emit(map[string]any{"t": "status", "c": "No search results"})
		}
	}

	var defs []ToolDef
	toolMap := map[string]mcpTool{}
	if cr.Tools {
		// Tool results can contain local files, so tools are for local models only.
		if a.allowLAN || !cfg.isLocal() || (cfg.Kind != "ollama" && cfg.Kind != "llamacpp" && cfg.Kind != "openai-compatible") {
			emit(map[string]any{"t": "error", "c": "Tools are only available with local models (Ollama, llama.cpp or a local OpenAI-compatible server)."})
			return
		}
		emit(map[string]any{"t": "status", "c": "Starting tool servers…"})
		for _, t := range a.mcp.Tools(r.Context()) {
			ns := nsName(t.Server, t.Name)
			toolMap[ns] = t
			defs = append(defs, ToolDef{Name: ns, Description: t.Description, Schema: t.Schema})
		}
	}
	emit(map[string]any{"t": "status", "c": ""})
	req := &ChatRequest{Model: cr.Model, Messages: msgs, Params: cr.Params, Think: cr.Think, KeepAlive: cr.KeepAlive, Tools: defs}
	st, err := prov.Stream(r.Context(), req, func(d Delta) {
		if len(d.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(d.ToolCalls))
			for _, tc := range d.ToolCalls {
				t := toolMap[tc.Name]
				calls = append(calls, map[string]any{"id": tc.ID, "ns": tc.Name, "server": t.Server, "tool": t.Name, "args": argsOrEmpty(tc.Args), "read_only": t.ReadOnly, "known": t.Name != ""})
			}
			emit(map[string]any{"t": "tool_calls", "calls": calls})
		}
		if d.Content != "" || d.Thinking != "" {
			emit(map[string]any{"t": "delta", "c": d.Content, "th": d.Thinking})
		}
	})
	if err != nil {
		if r.Context().Err() == nil {
			emit(map[string]any{"t": "error", "c": err.Error()})
		}
		return
	}
	emit(map[string]any{"t": "done", "stats": map[string]any{
		"eval_count": st.OutputTokens, "prompt_eval_count": st.InputTokens,
		"tps": st.TPS, "total_ms": st.TotalMs, "load_ms": st.LoadMs,
		"done_reason": st.DoneReason, "provider": cfg.ID, "local": cfg.isLocal(),
	}})
}

// filterParams drops every generation setting the provider's API doesn't
// support, so Ollama-only knobs never reach cloud APIs.
func filterParams(p GenParams, allowed []string) GenParams {
	if !has(allowed, "temperature") {
		p.Temperature = nil
	}
	if !has(allowed, "top_p") {
		p.TopP = nil
	}
	if !has(allowed, "top_k") {
		p.TopK = nil
	}
	if !has(allowed, "repeat_penalty") {
		p.RepeatPenalty = nil
	}
	if !has(allowed, "num_ctx") {
		p.NumCtx = nil
	}
	if !has(allowed, "max_tokens") {
		p.MaxTokens = nil
	}
	if !has(allowed, "seed") {
		p.Seed = nil
	}
	return p
}

var thinkTagRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// complete runs a short non-interactive request (titles, query rewriting) on
// the SAME provider/model the user chose — never a different one.
func complete(ctx context.Context, p Provider, model string, msgs []Message, maxTok int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	temp := 0.2
	req := &ChatRequest{Model: model, Messages: msgs, Params: GenParams{Temperature: &temp, MaxTokens: &maxTok}, Think: boolp(false), Aux: true}
	var sb strings.Builder
	if _, err := p.Stream(ctx, req, func(d Delta) { sb.WriteString(d.Content) }); err != nil {
		return "", err
	}
	s := thinkTagRe.ReplaceAllString(sb.String(), "")
	if i := strings.Index(s, "</think>"); i >= 0 {
		s = s[i+8:]
	}
	return strings.TrimSpace(s), nil
}

func (a *App) rewriteQuery(ctx context.Context, p Provider, model string, msgs []Message) string {
	var convo strings.Builder
	start := len(msgs) - 6
	if start < 0 {
		start = 0
	}
	for _, m := range msgs[start:] {
		if m.Role == "system" {
			continue
		}
		fmt.Fprintf(&convo, "%s: %s\n", m.Role, truncateRunes(m.Content, 1500))
	}
	prompt := "Today is " + time.Now().Format("January 2, 2006") + ". Given this conversation, write the single best web search query to answer the user's latest message. Reply with ONLY the query, no quotes or explanation.\n\n" + convo.String()
	q, err := complete(ctx, p, model, []Message{{Role: "user", Content: prompt}}, 60)
	if err != nil {
		return ""
	}
	return strings.Trim(strings.SplitN(q, "\n", 2)[0], "\"'` ")
}

func (a *App) title(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Text     string `json:"text"`
	}
	if !decodeJSON(w, r, 1<<20, &in) {
		return
	}
	p, _, err := a.reg.Get(providerID(in.Provider))
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	prompt := "Write a concise title (3-6 words) for a conversation that starts with the message below. Reply with ONLY the title — no quotes, no punctuation at the end.\n\nMessage:\n" + truncateRunes(in.Text, 2000)
	t, err := complete(r.Context(), p, in.Model, []Message{{Role: "user", Content: prompt}}, 40)
	if err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	t = cleanTitle(t)
	jsonOut(w, map[string]string{"title": truncateRunes(t, 80)})
}

var titleMarkup = strings.NewReplacer("**", "", "__", "", "`", "", "#", "")

// cleanTitle keeps the first line and strips markdown/quotes models like to add.
func cleanTitle(t string) string {
	t = strings.TrimSpace(strings.SplitN(strings.TrimSpace(t), "\n", 2)[0])
	t = strings.TrimPrefix(strings.TrimPrefix(t, "Title:"), "title:")
	t = titleMarkup.Replace(t)
	return strings.Trim(strings.TrimSpace(t), "\"'*“”. ")
}

func (a *App) searchOnly(w http.ResponseWriter, r *http.Request) {
	var in searchOpts
	if !decodeJSON(w, r, 1<<20, &in) {
		return
	}
	if a.allowLAN {
		in.SearxURL = a.savedSetting("searxUrl") // see lanpin.go
	}
	res, err := webSearch(r.Context(), in.Query, 5, in.Engine, in.SearxURL)
	if err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	jsonOut(w, res)
}

// ---------- Ollama model management ----------

func (a *App) pull(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Model string `json:"model"`
	}
	if !decodeJSON(w, r, 1<<16, &in) {
		return
	}
	bb, _ := json.Marshal(map[string]any{"model": in.Model, "stream": true})
	req, _ := http.NewRequestWithContext(r.Context(), "POST", a.oll.Get()+"/api/pull", bytes.NewReader(bb))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ollamaClient.Do(req)
	if err != nil {
		jsonErr(w, 502, "cannot reach Ollama")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/x-ndjson")
	flusher, _ := w.(http.Flusher)
	if resp.StatusCode != 200 {
		json.NewEncoder(w).Encode(map[string]string{"error": ollamaError(resp)})
		return
	}
	buf := make([]byte, 8192)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func (a *App) deleteModel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Model string `json:"model"`
	}
	if !decodeJSON(w, r, 1<<16, &in) {
		return
	}
	bb, _ := json.Marshal(map[string]string{"model": in.Model})
	req, _ := http.NewRequestWithContext(r.Context(), "DELETE", a.oll.Get()+"/api/delete", bytes.NewReader(bb))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ollamaClient.Do(req)
	if err != nil {
		jsonErr(w, 502, "cannot reach Ollama")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		jsonErr(w, resp.StatusCode, ollamaError(resp))
		return
	}
	w.WriteHeader(204)
}

// ---------- chats ----------

func (a *App) listChats(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.List()
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOut(w, list)
}

func (a *App) getChat(w http.ResponseWriter, r *http.Request) {
	b, err := a.store.Get(r.PathValue("id"))
	if err != nil {
		jsonErr(w, 404, "not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func (a *App) putChat(w http.ResponseWriter, r *http.Request) {
	b, ok := readBody(w, r, 256<<20)
	if !ok {
		return
	}
	if err := a.store.Put(r.PathValue("id"), b); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (a *App) deleteChat(w http.ResponseWriter, r *http.Request) {
	if err := a.store.Delete(r.PathValue("id")); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	w.WriteHeader(204)
}

type exportChat struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
	Created  int64  `json:"created"`
	System   string `json:"system"`
	Messages []struct {
		Role     string `json:"role"`
		Content  string `json:"content"`
		Thinking string `json:"thinking"`
		Model    string `json:"model"`
		Provider string `json:"provider"`
		Created  int64  `json:"created"`
		Sources  []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"sources"`
		Attachments []struct {
			Name string `json:"name"`
		} `json:"attachments"`
	} `json:"messages"`
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9 _.-]+`)

func (a *App) exportChat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, err := a.store.Get(id)
	if err != nil {
		jsonErr(w, 404, "not found")
		return
	}
	var c exportChat
	json.Unmarshal(b, &c)
	name := strings.TrimSpace(unsafeName.ReplaceAllString(c.Title, ""))
	if name == "" {
		name = id
	}
	switch r.URL.Query().Get("format") {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.json"`, name))
		w.Write(b)
	case "txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.txt"`, name))
		for _, m := range c.Messages {
			fmt.Fprintf(w, "%s:\n%s\n\n", strings.ToUpper(m.Role), m.Content)
		}
	default:
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.md"`, name))
		io.WriteString(w, a.chatMarkdown(&c, r.URL.Query().Get("thinking") == "1"))
	}
}

func (a *App) providerName(id string) string {
	if id == "" {
		id = "ollama"
	}
	if c, ok := a.reg.Config(id); ok {
		return c.Name
	}
	return id
}

func (a *App) chatMarkdown(c *exportChat, withThinking bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n\n", c.Title)
	fmt.Fprintf(&sb, "_Model: %s (%s) · Exported %s_\n\n", c.Model, a.providerName(c.Provider), time.Now().Format("Jan 2, 2006 3:04 PM"))
	if strings.TrimSpace(c.System) != "" {
		fmt.Fprintf(&sb, "> **System prompt:** %s\n\n", strings.ReplaceAll(c.System, "\n", "\n> "))
	}
	for _, m := range c.Messages {
		who := "You"
		if m.Role == "assistant" {
			who = "Assistant"
			if m.Model != "" {
				who += " (" + m.Model
				if m.Provider != "" && m.Provider != "ollama" {
					who += " · " + a.providerName(m.Provider)
				}
				who += ")"
			}
		}
		fmt.Fprintf(&sb, "---\n\n### %s\n\n", who)
		for _, at := range m.Attachments {
			fmt.Fprintf(&sb, "📎 `%s`\n\n", at.Name)
		}
		if withThinking && m.Thinking != "" {
			fmt.Fprintf(&sb, "<details><summary>Thinking</summary>\n\n%s\n\n</details>\n\n", m.Thinking)
		}
		sb.WriteString(m.Content + "\n\n")
		if len(m.Sources) > 0 {
			sb.WriteString("**Sources:**\n")
			for i, s := range m.Sources {
				fmt.Fprintf(&sb, "%d. [%s](%s)\n", i+1, s.Title, s.URL)
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func (a *App) exportAll(w http.ResponseWriter, r *http.Request) {
	list, _ := a.store.List()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="Guanaco-export-%s.json"`, time.Now().Format("2006-01-02")))
	io.WriteString(w, `{"app":"Guanaco","version":"`+version+`","chats":[`)
	first := true
	for _, m := range list {
		b, err := a.store.Get(m.ID)
		if err != nil {
			continue
		}
		if !first {
			io.WriteString(w, ",")
		}
		first = false
		w.Write(b)
	}
	io.WriteString(w, "]}")
}

func (a *App) importChats(w http.ResponseWriter, r *http.Request) {
	b, ok := readBody(w, r, 512<<20)
	if !ok {
		return
	}
	var wrapped struct {
		Chats []json.RawMessage `json:"chats"`
	}
	var arr []json.RawMessage
	if json.Unmarshal(b, &wrapped) == nil && wrapped.Chats != nil {
		arr = wrapped.Chats
	} else if json.Unmarshal(b, &arr) != nil {
		var single json.RawMessage
		if json.Unmarshal(b, &single) == nil {
			arr = []json.RawMessage{single}
		}
	}
	// Same rule as restoring in merge mode: never overwrite a copy of a chat
	// that is newer than (or as new as) the one being imported.
	current := map[string]int64{}
	if existing, err := a.store.List(); err == nil {
		for _, m := range existing {
			current[m.ID] = m.Updated
		}
	}
	n, skipped := 0, 0
	for _, raw := range arr {
		var m ChatMeta
		if json.Unmarshal(raw, &m) != nil || m.ID == "" {
			continue
		}
		if cu, ok := current[m.ID]; ok && cu >= m.Updated {
			skipped++
			continue
		}
		if a.store.Put(m.ID, raw) == nil {
			n++
		}
	}
	jsonOut(w, map[string]int{"imported": n, "skipped": skipped})
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// decodeJSON reads one size-limited JSON value into v. Malformed JSON or trailing
// data gets a 400; an empty body is allowed (handlers validate their own fields).
func decodeJSON(w http.ResponseWriter, r *http.Request, max int64, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, max))
	if err := dec.Decode(v); err != nil && err != io.EOF {
		jsonErr(w, 400, "invalid JSON request: "+err.Error())
		return false
	}
	// A second Decode must hit EOF. (dec.More() is for arrays/objects and
	// doesn't reliably flag a stray second top-level value or a lone "}".)
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		jsonErr(w, 400, "unexpected data after JSON body")
		return false
	}
	return true
}

// readBody reads a request body up to max bytes. Unlike io.LimitReader it
// reports an over-limit or broken body instead of silently truncating it.
func readBody(w http.ResponseWriter, r *http.Request, max int64) ([]byte, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			jsonErr(w, 413, "request body is too large")
		} else {
			jsonErr(w, 400, "could not read request body: "+err.Error())
		}
		return nil, false
	}
	return b, true
}
