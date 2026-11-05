package main

import (
	"encoding/json"
	"errors"
	"strings"
)

// In -lan mode anyone on the network can use the API, and a few requests make
// the server itself fetch a URL the client names (a SearXNG instance, a speech-
// to-text server). Letting them pick it would turn Guanaco into a proxy into
// the host's network. There, these URLs always come from the saved settings,
// and saving settings from the network can't change them.

var lanPinnedSettings = []string{"searxUrl", "whisperUrl"}

// savedSetting returns a string value from settings.json, or "".
func (a *App) savedSetting(key string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(a.store.GetSettings(), &m) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(m[key], &s) != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// pinLANSettings keeps the pinned keys of the stored settings when a client
// saves new settings in -lan mode.
func (a *App) pinLANSettings(raw []byte) ([]byte, error) {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(raw, &in); err != nil || in == nil {
		return nil, errors.New("settings must be a JSON object")
	}
	var cur map[string]json.RawMessage
	_ = json.Unmarshal(a.store.GetSettings(), &cur)
	for _, k := range lanPinnedSettings {
		if v, ok := cur[k]; ok {
			in[k] = v
		} else {
			delete(in, k)
		}
	}
	return json.Marshal(in)
}
