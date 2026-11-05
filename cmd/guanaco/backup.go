package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Backup zip layout (flat, fixed names so restore never trusts paths):
//
//	manifest.json        app/version/created/counts
//	settings.json        UI settings
//	providers.json       provider configuration — NO API keys, ever
//	chats/<id>.json      one file per chat
const (
	maxRestoreZip   = 512 << 20 // compressed upload
	maxRestoreEntry = 128 << 20 // one decompressed entry
	maxRestoreTotal = 1 << 30   // all decompressed entries
)

func (a *App) backup(w http.ResponseWriter, r *http.Request) {
	list, _ := a.store.List()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="Guanaco-backup-%s.zip"`, time.Now().Format("2006-01-02")))
	zw := zip.NewWriter(w)
	add := func(name string, b []byte) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = f.Write(b)
		return err
	}
	n := 0
	for _, m := range list {
		b, err := a.store.Get(m.ID)
		if err != nil {
			continue
		}
		if add("chats/"+m.ID+".json", b) == nil {
			n++
		}
	}
	add("settings.json", a.store.GetSettings())
	if b, err := os.ReadFile(a.reg.path); err == nil {
		add("providers.json", b)
	}
	man, _ := json.MarshalIndent(map[string]any{"app": "Guanaco", "version": version, "created": time.Now().UTC().Format(time.RFC3339), "chats": n, "keys_included": false}, "", "  ")
	add("manifest.json", man)
	zw.Close()
}

type restoreReport struct {
	Imported  int  `json:"imported"`
	Skipped   int  `json:"skipped"`
	Removed   int  `json:"removed"`
	Settings  bool `json:"settings"`
	Providers bool `json:"providers"`
}

// restore accepts a backup zip. mode=merge (default) adds missing chats and
// replaces older copies only; mode=replace first deletes every current chat.
// settings=1 / providers=1 opt in to overwriting those files.
func (a *App) restore(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mode := q.Get("mode")
	if mode != "" && mode != "merge" && mode != "replace" {
		jsonErr(w, 400, "mode must be merge or replace")
		return
	}
	wantProviders := q.Get("providers") == "1"
	if wantProviders && a.configLocked(w) {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRestoreZip))
	if err != nil {
		jsonErr(w, 413, "backup file is too large")
		return
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		jsonErr(w, 400, "not a valid backup zip")
		return
	}

	type chatEntry struct {
		id  string
		raw []byte
		upd int64
	}
	var chats []chatEntry
	var settings, providers []byte
	var total int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := f.Name
		isChat := strings.HasPrefix(name, "chats/") && strings.HasSuffix(name, ".json")
		if !isChat && name != "settings.json" && name != "providers.json" {
			continue // manifest and anything unknown is ignored
		}
		if f.UncompressedSize64 > maxRestoreEntry {
			jsonErr(w, 400, "backup contains an oversized file: "+name)
			return
		}
		rc, err := f.Open()
		if err != nil {
			jsonErr(w, 400, "unreadable entry: "+name)
			return
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxRestoreEntry+1))
		rc.Close()
		if err != nil || int64(len(b)) > maxRestoreEntry {
			jsonErr(w, 400, "unreadable or oversized entry: "+name)
			return
		}
		if total += int64(len(b)); total > maxRestoreTotal {
			jsonErr(w, 400, "backup is too large when unpacked")
			return
		}
		switch {
		case name == "settings.json":
			settings = b
		case name == "providers.json":
			providers = b
		default:
			id := strings.TrimSuffix(strings.TrimPrefix(name, "chats/"), ".json")
			var m ChatMeta
			if !idRe.MatchString(id) || json.Unmarshal(b, &m) != nil || m.ID != id {
				continue
			}
			chats = append(chats, chatEntry{id, b, m.Updated})
		}
	}
	if len(chats) == 0 && settings == nil && providers == nil {
		jsonErr(w, 400, "this zip contains nothing restorable (is it a Guanaco backup?)")
		return
	}
	// "Replace" deletes every chat that isn't in the backup. A zip with no
	// usable chats (only settings, or chats that all failed validation) would
	// wipe everything, which is never what anyone means.
	if mode == "replace" && len(chats) == 0 {
		jsonErr(w, 400, "this backup contains no chats, so replace would delete all of yours; use merge instead")
		return
	}

	var rep restoreReport
	if mode == "replace" {
		existing, _ := a.store.List()
		keep := map[string]bool{}
		for _, c := range chats {
			keep[c.id] = true
		}
		for _, m := range existing {
			if !keep[m.ID] && a.store.Delete(m.ID) == nil {
				rep.Removed++
			}
		}
	}
	current := map[string]int64{}
	if existing, err := a.store.List(); err == nil {
		for _, m := range existing {
			current[m.ID] = m.Updated
		}
	}
	for _, c := range chats {
		if cu, ok := current[c.id]; ok && mode != "replace" && cu >= c.upd {
			rep.Skipped++
			continue
		}
		if a.store.Put(c.id, c.raw) == nil {
			rep.Imported++
		}
	}
	if q.Get("settings") == "1" && settings != nil && json.Valid(settings) {
		rep.Settings = a.store.PutSettings(settings) == nil
	}
	if wantProviders && providers != nil {
		if err := a.reg.RestoreConfigs(providers); err != nil {
			jsonErr(w, 400, "providers not restored: "+err.Error())
			return
		}
		rep.Providers = true
	}
	jsonOut(w, rep)
}

// RestoreConfigs merges provider settings from a backup. Built-ins are matched
// by ID; custom OpenAI-compatible endpoints are added or updated. Anything that
// is not local comes back DISABLED, because keys are never in a backup and a
// restore must not quietly turn on a paid cloud provider.
func (r *Registry) RestoreConfigs(raw []byte) error {
	var in []ProviderConfig
	if err := json.Unmarshal(raw, &in); err != nil {
		return errors.New("providers.json is not valid")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range in {
		if !idRe.MatchString(c.ID) {
			continue
		}
		spec, ok := kinds[c.Kind]
		if !ok || (c.Kind != c.ID && c.Kind != "openai-compatible") {
			continue
		}
		if c.BaseURL != "" && validBaseURL(c.BaseURL) != nil {
			c.BaseURL = ""
		}
		if c.Kind == "openai-compatible" && c.BaseURL == "" {
			continue
		}
		if c.Kind != "ollama" && !c.isLocal() {
			c.Enabled = false
		}
		if c.Kind == "ollama" {
			c.BaseURL, c.Enabled = "", true
		}
		c.Name = truncateRunes(strings.TrimSpace(c.Name), 40)
		if c.Name == "" {
			c.Name = spec.Name
		}
		if i := r.find(c.ID); i >= 0 {
			r.cfgs[i] = c
		} else {
			r.cfgs = append(r.cfgs, c)
		}
	}
	r.cacheMu.Lock()
	r.cache = map[string]cachedModels{}
	r.cacheMu.Unlock()
	return r.saveLocked()
}
