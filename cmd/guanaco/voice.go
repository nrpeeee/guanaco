package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Dictation through a LOCAL speech-to-text server (whisper.cpp's server or any
// OpenAI-compatible /audio/transcriptions endpoint). The browser sends 16 kHz
// mono WAV here and we forward it; the target must be on this machine or LAN so
// audio can never be sent to a remote service by accident.

func privateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func requireLocalURL(raw string) (*url.URL, error) {
	if err := validBaseURL(raw); err != nil || raw == "" {
		if err == nil {
			err = errors.New("no speech-to-text URL set")
		}
		return nil, err
	}
	u, _ := url.Parse(raw)
	host := u.Hostname()
	if host == "localhost" {
		return u, nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if !privateIP(ip) {
			return nil, errors.New("speech-to-text must run on this computer or your local network")
		}
		return u, nil
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("can't resolve %s", host)
	}
	for _, ip := range ips {
		if !privateIP(ip) {
			return nil, errors.New("speech-to-text must run on this computer or your local network")
		}
	}
	return u, nil
}

// transcribe: POST /api/transcribe?url=…&style=inference|openai&model=…&lang=…  body = audio/wav
func (a *App) transcribe(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	raw := strings.TrimSpace(q.Get("url"))
	if a.allowLAN { // see lanpin.go
		if raw = a.savedSetting("whisperUrl"); raw == "" {
			raw = "http://127.0.0.1:8081" // the UI's default
		}
	}
	base, err := requireLocalURL(raw)
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	audio, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 48<<20))
	if err != nil || len(audio) < 64 {
		jsonErr(w, 400, "no audio received")
		return
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "audio.wav")
	fw.Write(audio)
	target := strings.TrimRight(base.String(), "/")
	if q.Get("style") == "openai" {
		if strings.HasSuffix(target, "/v1") {
			target += "/audio/transcriptions"
		} else {
			target += "/v1/audio/transcriptions"
		}
		model := q.Get("model")
		if model == "" {
			model = "whisper-1"
		}
		mw.WriteField("model", model)
		mw.WriteField("response_format", "json")
	} else {
		if !strings.HasSuffix(target, "/inference") {
			target += "/inference"
		}
		mw.WriteField("response_format", "json")
		mw.WriteField("temperature", "0.0")
	}
	if lang := strings.TrimSpace(q.Get("lang")); lang != "" && len(lang) <= 12 {
		mw.WriteField("language", lang)
	}
	mw.Close()

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", target, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := ollamaClient.Do(req)
	if err != nil {
		jsonErr(w, 502, "can't reach the speech-to-text server: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		jsonErr(w, 502, fmt.Sprintf("speech-to-text server returned %d: %s", resp.StatusCode, truncateRunes(strings.TrimSpace(string(body)), 200)))
		return
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		jsonErr(w, 502, "unexpected reply from the speech-to-text server")
		return
	}
	jsonOut(w, map[string]string{"text": strings.TrimSpace(out.Text)})
}
