package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// OllamaRef holds the current Ollama base URL. It can change at runtime when
// auto-detection finds Ollama somewhere else, so every reader goes through Get.
type OllamaRef struct {
	mu  sync.RWMutex
	url string
}

func NewOllamaRef(u string) *OllamaRef { return &OllamaRef{url: strings.TrimRight(u, "/")} }

func (o *OllamaRef) Get() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.url
}

func (o *OllamaRef) Set(u string) {
	o.mu.Lock()
	o.url = strings.TrimRight(u, "/")
	o.mu.Unlock()
}

var probeClient = &http.Client{Timeout: 1500 * time.Millisecond}

// probeOllama reports whether base answers like Ollama (GET /api/version).
func probeOllama(ctx context.Context, base string) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/api/version", nil)
	if err != nil {
		return "", false
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	var v struct {
		Version string `json:"version"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&v) != nil || v.Version == "" {
		return "", false
	}
	return v.Version, true
}

// ollamaCandidates lists the places Ollama normally listens, current URL first.
// It is a fixed list: detection never probes user-supplied or remote hosts.
func ollamaCandidates(current string) []string {
	list := []string{current, "http://127.0.0.1:11434", "http://localhost:11434", "http://[::1]:11434"}
	seen := map[string]bool{}
	var out []string
	for _, u := range list {
		u = strings.TrimRight(u, "/")
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// detectOllama returns the first candidate that answers, or ok=false plus the
// list it tried.
func detectOllama(ctx context.Context, current string) (url, version string, tried []string, ok bool) {
	for _, u := range ollamaCandidates(current) {
		tried = append(tried, u)
		if v, good := probeOllama(ctx, u); good {
			return u, v, tried, true
		}
	}
	return "", "", tried, false
}

// findOllamaBinary looks on PATH and in the usual per-OS install locations.
func findOllamaBinary() string {
	if p, err := exec.LookPath("ollama"); err == nil {
		return p
	}
	var cands []string
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			cands = append(cands, filepath.Join(d, "Programs", "Ollama", "ollama.exe"))
		}
		if d := os.Getenv("ProgramFiles"); d != "" {
			cands = append(cands, filepath.Join(d, "Ollama", "ollama.exe"))
		}
	case "darwin":
		cands = append(cands, "/Applications/Ollama.app/Contents/Resources/ollama", "/usr/local/bin/ollama", "/opt/homebrew/bin/ollama")
	default:
		cands = append(cands, "/usr/local/bin/ollama", "/usr/bin/ollama")
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// startOllama launches `ollama serve` detached and waits briefly for it to answer.
func startOllama(ctx context.Context, base string) (string, error) {
	bin := findOllamaBinary()
	if bin == "" {
		return "", errors.New("no ollama executable found: is Ollama installed?")
	}
	cmd := exec.Command(bin, "serve")
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	go cmd.Wait() // reap; Ollama keeps running on its own
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := probeOllama(ctx, base); ok {
			return v, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
	return "", errors.New("started Ollama but it did not answer within 10 seconds")
}

// autodetect runs once at startup when the user did not pin an Ollama URL.
func (a *App) autodetect() {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cur := a.oll.Get()
	if _, ok := probeOllama(ctx, cur); ok {
		return
	}
	if u, v, _, ok := detectOllama(ctx, cur); ok {
		a.oll.Set(u)
		log.Printf("Ollama %s found at %s (was %s)", v, u, cur)
	}
}

// ---------- HTTP handlers ----------

func (a *App) detectOllamaHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	cur := a.oll.Get()
	u, v, tried, ok := detectOllama(ctx, cur)
	if !ok {
		jsonOut(w, map[string]any{"ok": false, "tried": tried, "installed": findOllamaBinary() != ""})
		return
	}
	changed := u != cur
	if changed {
		a.oll.Set(u)
	}
	jsonOut(w, map[string]any{"ok": true, "url": u, "version": v, "changed": changed})
}

func (a *App) startOllamaHandler(w http.ResponseWriter, r *http.Request) {
	if a.allowLAN {
		jsonErr(w, 403, "starting Ollama is disabled in -lan mode")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 14*time.Second)
	defer cancel()
	cur := a.oll.Get()
	if v, ok := probeOllama(ctx, cur); ok {
		jsonOut(w, map[string]any{"ok": true, "url": cur, "version": v, "already": true})
		return
	}
	if u, v, _, ok := detectOllama(ctx, cur); ok {
		a.oll.Set(u)
		jsonOut(w, map[string]any{"ok": true, "url": u, "version": v, "already": true})
		return
	}
	v, err := startOllama(ctx, cur)
	if err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	jsonOut(w, map[string]any{"ok": true, "url": cur, "version": v})
}

// unloadModel frees a model's memory (keep_alive 0) without deleting it.
func (a *App) unloadModel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Model string `json:"model"`
	}
	if !decodeJSON(w, r, 1<<16, &in) {
		return
	}
	if strings.TrimSpace(in.Model) == "" {
		jsonErr(w, 400, "model is required")
		return
	}
	bb, _ := json.Marshal(map[string]any{"model": in.Model, "keep_alive": 0, "prompt": "", "stream": false})
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", a.oll.Get()+"/api/generate", strings.NewReader(string(bb)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ollamaClient.Do(req)
	if err != nil {
		jsonErr(w, 502, "cannot reach Ollama")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		jsonErr(w, 502, ollamaError(resp))
		return
	}
	jsonOut(w, map[string]bool{"ok": true})
}
