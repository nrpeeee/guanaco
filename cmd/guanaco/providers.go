package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ───────────────────────── normalized types ─────────────────────────

// Image accepts either a bare base64 string (legacy / Ollama style) or {"mime","data"}.
type Image struct {
	Mime string `json:"mime,omitempty"`
	Data string `json:"data"`
}

func (im *Image) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &im.Data)
	}
	type raw Image
	return json.Unmarshal(b, (*raw)(im))
}

// MimeType returns the declared type, or sniffs it from the bytes.
func (im Image) MimeType() string {
	if strings.HasPrefix(im.Mime, "image/") {
		return im.Mime
	}
	head := im.Data
	if len(head) > 64 {
		head = head[:64]
	}
	b, _ := base64.StdEncoding.DecodeString(head[:len(head)/4*4])
	if t := http.DetectContentType(b); strings.HasPrefix(t, "image/") {
		return t
	}
	return "image/png"
}

type Message struct {
	Role       string     `json:"role"` // system | user | assistant | tool
	Content    string     `json:"content"`
	Images     []Image    `json:"images,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // assistant asked to call tools
	ToolCallID string     `json:"tool_call_id,omitempty"` // role "tool": which call this answers
	Name       string     `json:"name,omitempty"`         // role "tool": the tool's (namespaced) name
}

// ToolCall is a model's request to run a tool. Args is a JSON object.
type ToolCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type ToolDef struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

func argsOrEmpty(a json.RawMessage) json.RawMessage {
	if len(a) == 0 || string(a) == "null" {
		return json.RawMessage("{}")
	}
	return a
}

// GenParams is the union of generation settings. Nil means "not set";
// each adapter forwards only the fields its API supports.
type GenParams struct {
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"top_p,omitempty"`
	TopK          *int     `json:"top_k,omitempty"`
	RepeatPenalty *float64 `json:"repeat_penalty,omitempty"`
	NumCtx        *int     `json:"num_ctx,omitempty"`
	MaxTokens     *int     `json:"max_tokens,omitempty"`
	Seed          *int     `json:"seed,omitempty"`
}

type ChatRequest struct {
	Model     string
	Messages  []Message
	Params    GenParams
	Think     *bool
	KeepAlive string // Ollama only
	Aux       bool   // helper request (title / query rewrite): keep it cheap and short
	Tools     []ToolDef
}

type Delta struct {
	Content   string
	Thinking  string
	ToolCalls []ToolCall
}

type Stats struct {
	InputTokens  int
	OutputTokens int
	TPS          float64
	TotalMs      int64
	LoadMs       int64
	DoneReason   string
}

type ModelEntry struct {
	Provider string          `json:"provider"`
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Local    bool            `json:"local"`
	Size     int64           `json:"size,omitempty"`
	Details  json.RawMessage `json:"details,omitempty"`
	Vision   *bool           `json:"vision,omitempty"`
	Thinking *bool           `json:"thinking,omitempty"`
	Context  int             `json:"context,omitempty"`
}

type Provider interface {
	Models(ctx context.Context) ([]ModelEntry, error)
	Stream(ctx context.Context, req *ChatRequest, onDelta func(Delta)) (*Stats, error)
}

// ───────────────────────── provider kinds ─────────────────────────

type kindSpec struct {
	Name        string
	Local       bool
	NeedsKey    bool
	DefaultBase string
	EnvKeys     []string
	Params      []string // generation params this API honors (others are dropped)
}

var kinds = map[string]kindSpec{
	"ollama":            {Name: "Ollama", Local: true, Params: []string{"temperature", "top_p", "top_k", "repeat_penalty", "num_ctx", "max_tokens", "seed", "keep_alive"}},
	"openai":            {Name: "OpenAI", NeedsKey: true, DefaultBase: "https://api.openai.com/v1", EnvKeys: []string{"OPENAI_API_KEY"}, Params: []string{"temperature", "top_p", "max_tokens", "seed"}},
	"anthropic":         {Name: "Anthropic", NeedsKey: true, DefaultBase: "https://api.anthropic.com", EnvKeys: []string{"ANTHROPIC_API_KEY"}, Params: []string{"temperature", "max_tokens"}},
	"gemini":            {Name: "Google Gemini", NeedsKey: true, DefaultBase: "https://generativelanguage.googleapis.com", EnvKeys: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}, Params: []string{"temperature", "top_p", "top_k", "max_tokens", "seed"}},
	"llamacpp":          {Name: "llama.cpp", Local: true, DefaultBase: "http://127.0.0.1:8080/v1", Params: []string{"temperature", "top_p", "top_k", "repeat_penalty", "max_tokens", "seed"}},
	"openai-compatible": {Name: "OpenAI-compatible", Params: []string{"temperature", "top_p", "max_tokens", "seed"}},
}

// ProviderConfig is the non-secret configuration, persisted to providers.json.
type ProviderConfig struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url,omitempty"` // empty = kind default
	Enabled bool   `json:"enabled"`
	Local   *bool  `json:"local,omitempty"` // compatible endpoints: user says whether it runs on their machine/LAN
}

func (c ProviderConfig) spec() kindSpec { return kinds[c.Kind] }
func (c ProviderConfig) isLocal() bool {
	if c.Kind == "openai-compatible" && c.Local != nil {
		return *c.Local
	}
	return c.spec().Local
}
func (c ProviderConfig) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return c.spec().DefaultBase
}

// ───────────────────────── registry ─────────────────────────

var errNoProvider = errors.New("provider not found")

type Registry struct {
	mu       sync.RWMutex
	path     string
	cfgs     []ProviderConfig
	creds    *CredStore
	oll      *OllamaRef
	lan      bool
	lanCloud bool

	cacheMu sync.Mutex
	cache   map[string]cachedModels
}

type cachedModels struct {
	at     time.Time
	models []ModelEntry
}

func NewRegistry(dataDir string, oll *OllamaRef, creds *CredStore, lan, lanCloud bool) (*Registry, error) {
	r := &Registry{path: filepath.Join(dataDir, "providers.json"), creds: creds, oll: oll, lan: lan, lanCloud: lanCloud, cache: map[string]cachedModels{}}
	if b, err := os.ReadFile(r.path); err == nil {
		if err := json.Unmarshal(b, &r.cfgs); err != nil {
			return nil, fmt.Errorf("providers.json: %w", err)
		}
	}
	// make sure built-ins exist, in a stable order
	for _, id := range []string{"ollama", "llamacpp", "openai", "anthropic", "gemini"} {
		if r.find(id) < 0 {
			r.cfgs = append(r.cfgs, ProviderConfig{ID: id, Kind: id, Name: kinds[id].Name, Enabled: id == "ollama"})
		}
	}
	return r, nil
}

func (r *Registry) find(id string) int {
	for i, c := range r.cfgs {
		if c.ID == id {
			return i
		}
	}
	return -1
}

func (r *Registry) saveLocked() error {
	b, _ := json.MarshalIndent(r.cfgs, "", "  ")
	return writeAtomic(r.path, b)
}

func (r *Registry) Config(id string) (ProviderConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if i := r.find(id); i >= 0 {
		return r.cfgs[i], true
	}
	return ProviderConfig{}, false
}

func (r *Registry) Configs() []ProviderConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]ProviderConfig(nil), r.cfgs...)
}

func (r *Registry) Update(id string, fn func(*ProviderConfig) error) (ProviderConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.find(id)
	if i < 0 {
		return ProviderConfig{}, errNoProvider
	}
	c := r.cfgs[i]
	if err := fn(&c); err != nil {
		return ProviderConfig{}, err
	}
	prev := r.cfgs[i]
	r.cfgs[i] = c
	if err := r.saveLocked(); err != nil {
		r.cfgs[i] = prev
		return ProviderConfig{}, err
	}
	r.invalidate(id)
	return c, nil
}

func (r *Registry) Add(c ProviderConfig) (ProviderConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c.ID = "c-" + randID()
	r.cfgs = append(r.cfgs, c)
	if err := r.saveLocked(); err != nil {
		r.cfgs = r.cfgs[:len(r.cfgs)-1]
		return ProviderConfig{}, err
	}
	return c, nil
}

func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.find(id)
	if i < 0 {
		return errNoProvider
	}
	if r.cfgs[i].Kind != "openai-compatible" {
		return errors.New("built-in providers can't be removed (disable them instead)")
	}
	old := append([]ProviderConfig(nil), r.cfgs...)
	r.cfgs = append(r.cfgs[:i], r.cfgs[i+1:]...)
	if err := r.saveLocked(); err != nil {
		r.cfgs = old
		return err
	}
	r.invalidate(id)
	_ = r.creds.Delete(id)
	return nil
}

func (r *Registry) invalidate(id string) {
	r.cacheMu.Lock()
	delete(r.cache, id)
	r.cacheMu.Unlock()
}

// KeyFor returns the API key and where it came from ("stored", "env:NAME" or "").
func (r *Registry) KeyFor(c ProviderConfig) (string, string) {
	if k, ok := r.creds.Get(c.ID); ok && k != "" {
		return k, "stored"
	}
	for _, e := range c.spec().EnvKeys {
		if v := strings.TrimSpace(os.Getenv(e)); v != "" {
			return v, "env:" + e
		}
	}
	return "", ""
}

// Get resolves a provider strictly: no defaults, no fallbacks. A disabled,
// unknown or unconfigured provider is an error — the caller never silently
// lands on a different (possibly paid) backend.
func (r *Registry) Get(id string) (Provider, ProviderConfig, error) {
	c, ok := r.Config(id)
	if !ok {
		return nil, c, fmt.Errorf("unknown provider %q", id)
	}
	return r.build(c, true)
}

// build constructs the adapter. requireEnabled=false is used by "Test connection".
func (r *Registry) build(c ProviderConfig, requireEnabled bool) (Provider, ProviderConfig, error) {
	if requireEnabled && !c.Enabled {
		return nil, c, fmt.Errorf("%s is disabled — enable it under Providers", c.Name)
	}
	if r.lan && !r.lanCloud && !c.isLocal() {
		return nil, c, fmt.Errorf("%s is blocked while Guanaco runs with -lan (anyone on the network could spend your API credits); restart with -lan-cloud to allow it", c.Name)
	}
	key, _ := r.KeyFor(c)
	if c.spec().NeedsKey && key == "" {
		return nil, c, fmt.Errorf("%s has no API key — add one under Providers", c.Name)
	}
	switch c.Kind {
	case "ollama":
		return &ollamaProvider{id: c.ID, base: r.oll.Get()}, c, nil
	case "openai":
		return &openAIProvider{id: c.ID, base: c.base(), key: key}, c, nil
	case "openai-compatible":
		if c.base() == "" {
			return nil, c, fmt.Errorf("%s has no base URL", c.Name)
		}
		return &openAIProvider{id: c.ID, base: c.base(), key: key, compat: true, local: c.isLocal()}, c, nil
	case "llamacpp":
		return &openAIProvider{id: c.ID, base: c.base(), key: key, compat: true, local: true, llama: true}, c, nil
	case "anthropic":
		return &anthropicProvider{id: c.ID, base: c.base(), key: key}, c, nil
	case "gemini":
		return &geminiProvider{id: c.ID, base: c.base(), key: key}, c, nil
	}
	return nil, c, fmt.Errorf("unsupported provider kind %q", c.Kind)
}

// Models lists a provider's models, caching remote catalogs for a few minutes.
func (r *Registry) Models(ctx context.Context, c ProviderConfig, fresh bool) ([]ModelEntry, error) {
	ttl := 10 * time.Minute
	if c.isLocal() {
		ttl = 0
	}
	if !fresh && ttl > 0 {
		r.cacheMu.Lock()
		cm, ok := r.cache[c.ID]
		r.cacheMu.Unlock()
		if ok && time.Since(cm.at) < ttl {
			// Hand out a copy: callers annotate entries (catalog sets Local),
			// and mutating the shared cache races with concurrent readers.
			return append([]ModelEntry(nil), cm.models...), nil
		}
	}
	p, _, err := r.build(c, true)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ms, err := p.Models(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].ID < ms[j].ID })
	if ttl > 0 {
		r.cacheMu.Lock()
		r.cache[c.ID] = cachedModels{at: time.Now(), models: ms}
		r.cacheMu.Unlock()
	}
	return ms, nil
}

func validBaseURL(s string) error {
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errors.New("base URL must be an http(s) URL")
	}
	if u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return errors.New("base URL cannot contain credentials, a query string or a fragment")
	}
	return nil
}

// ───────────────────────── shared HTTP helpers ─────────────────────────

var cloudClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 120 * time.Second,
	IdleConnTimeout:       90 * time.Second,
}}

func postJSON(ctx context.Context, u string, body any, hdr map[string]string) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return cloudClient.Do(req)
}

func getJSON(ctx context.Context, u string, hdr map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := cloudClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return apiError(resp)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

// apiError turns any provider's error body into a readable message.
func apiError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var e struct {
		Error json.RawMessage `json:"error"`
		Msg   string          `json:"message"`
	}
	msg := ""
	if json.Unmarshal(b, &e) == nil {
		var obj struct {
			Message string `json:"message"`
		}
		var s string
		switch {
		case json.Unmarshal(e.Error, &obj) == nil && obj.Message != "":
			msg = obj.Message
		case json.Unmarshal(e.Error, &s) == nil && s != "":
			msg = s
		case e.Msg != "":
			msg = e.Msg
		}
	}
	if msg == "" {
		msg = strings.TrimSpace(string(b))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
	}
	switch resp.StatusCode {
	case 401, 403:
		return fmt.Errorf("authentication failed (%d): %s", resp.StatusCode, msg)
	case 429:
		return fmt.Errorf("rate limited or out of credits (429): %s", msg)
	}
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("%s (%d)", msg, resp.StatusCode)
}

// readSSE calls fn for each server-sent event. Returning errStopSSE ends cleanly.
var errStopSSE = errors.New("stop")

func readSSE(r io.Reader, fn func(event, data string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	var event string
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			event = ""
			return nil
		}
		d := strings.TrimSuffix(data.String(), "\n")
		data.Reset()
		ev := event
		event = ""
		return fn(ev, d)
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				if err == errStopSSE {
					return nil
				}
				return err
			}
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimPrefix(line[5:], " "))
			data.WriteByte('\n')
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil && err != errStopSSE {
		return err
	}
	return nil
}

// splitSystem hoists every system message into one string (Anthropic, Gemini).
func splitSystem(msgs []Message) (string, []Message) {
	var sys []string
	var rest []Message
	for _, m := range msgs {
		if m.Role == "system" {
			if s := strings.TrimSpace(m.Content); s != "" {
				sys = append(sys, s)
			}
			continue
		}
		rest = append(rest, m)
	}
	return strings.Join(sys, "\n\n"), rest
}

// streamTimer measures output speed for providers that don't report durations.
type streamTimer struct{ start, first time.Time }

func newTimer() *streamTimer { return &streamTimer{start: time.Now()} }
func (t *streamTimer) tick() {
	if t.first.IsZero() {
		t.first = time.Now()
	}
}
func (t *streamTimer) finish(s *Stats) {
	s.TotalMs = time.Since(t.start).Milliseconds()
	if !t.first.IsZero() {
		s.LoadMs = t.first.Sub(t.start).Milliseconds() // time to first token
		if d := time.Since(t.first).Seconds(); d > 0.05 && s.OutputTokens > 0 {
			s.TPS = float64(s.OutputTokens) / d
		}
	}
}

var (
	reOpenAIReasoning = regexp.MustCompile(`^(o\d|gpt-5)`)
	reOpenAIChat      = regexp.MustCompile(`^(gpt-|o\d|chatgpt-)`)
	reOpenAISkip      = regexp.MustCompile(`(audio|realtime|tts|transcribe|image|search|embedding|instruct|moderation|dall-e|whisper|davinci|babbage|codex)`)
)

func boolp(b bool) *bool { return &b }

func domainOf(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return u
	}
	return strings.TrimPrefix(p.Hostname(), "www.")
}
