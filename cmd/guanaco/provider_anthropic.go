package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type anthropicProvider struct {
	id   string
	base string
	key  string
}

// Claude models without extended thinking.
var reClaudeNoThink = regexp.MustCompile(`claude-(2|instant|3-(5-)?(opus|sonnet|haiku)|3-5)`)

func claudeThinks(model string) bool { return !reClaudeNoThink.MatchString(model) }

func (p *anthropicProvider) headers() map[string]string {
	return map[string]string{"x-api-key": p.key, "anthropic-version": "2023-06-01", "Accept": "text/event-stream"}
}

func (p *anthropicProvider) Models(ctx context.Context) ([]ModelEntry, error) {
	var out struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := getJSON(ctx, p.base+"/v1/models?limit=1000", p.headers(), &out); err != nil {
		return nil, err
	}
	ms := make([]ModelEntry, 0, len(out.Data))
	for _, m := range out.Data {
		name := m.DisplayName
		if name == "" {
			name = m.ID
		}
		ms = append(ms, ModelEntry{Provider: p.id, ID: m.ID, Name: name, Vision: boolp(true), Thinking: boolp(claudeThinks(m.ID))})
	}
	return ms, nil
}

func (p *anthropicProvider) body(cr *ChatRequest) map[string]any {
	system, rest := splitSystem(cr.Messages)
	type block map[string]any
	type amsg struct {
		Role    string  `json:"role"`
		Content []block `json:"content"`
	}
	var msgs []amsg
	for _, m := range rest {
		var blocks []block
		if m.Role == "user" {
			for _, im := range m.Images { // images first, as Anthropic recommends
				blocks = append(blocks, block{"type": "image", "source": map[string]string{"type": "base64", "media_type": im.MimeType(), "data": im.Data}})
			}
		}
		if strings.TrimSpace(m.Content) != "" {
			blocks = append(blocks, block{"type": "text", "text": m.Content})
		}
		if len(blocks) == 0 {
			continue
		}
		// The Messages API requires alternating roles: merge consecutive turns.
		if n := len(msgs); n > 0 && msgs[n-1].Role == m.Role {
			msgs[n-1].Content = append(msgs[n-1].Content, blocks...)
			continue
		}
		msgs = append(msgs, amsg{Role: m.Role, Content: blocks})
	}
	if len(msgs) > 0 && msgs[0].Role != "user" {
		msgs = append([]amsg{{Role: "user", Content: []block{{"type": "text", "text": "(conversation continues)"}}}}, msgs...)
	}

	maxTok := 8192
	if strings.Contains(cr.Model, "claude-3-") {
		maxTok = 4096
	}
	if cr.Params.MaxTokens != nil && *cr.Params.MaxTokens > 0 {
		maxTok = *cr.Params.MaxTokens // the caller's cap is a cap, never a floor
	}
	b := map[string]any{"model": cr.Model, "messages": msgs, "stream": true}
	if system != "" {
		b["system"] = system
	}
	thinking := cr.Think != nil && *cr.Think && claudeThinks(cr.Model) && !cr.Aux
	if thinking {
		budget := 8000
		if cr.Params.MaxTokens != nil && *cr.Params.MaxTokens > 0 {
			// Thinking tokens come out of max_tokens; leave room for the answer.
			budget = maxTok - 1024
		}
		if budget < 1024 {
			// Extended thinking needs at least a 1024-token budget, so a cap
			// that small can't have it. Answer without thinking rather than
			// quietly raising the cap above what was asked for.
			thinking = false
		} else {
			b["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
		}
		// temperature/top_p/top_k are not allowed with extended thinking
	}
	if !thinking && cr.Params.Temperature != nil {
		t := *cr.Params.Temperature
		if t > 1 { // Anthropic's range is 0–1
			t = 1
		}
		b["temperature"] = t
	}
	b["max_tokens"] = maxTok
	return b
}

func (p *anthropicProvider) Stream(ctx context.Context, cr *ChatRequest, onDelta func(Delta)) (*Stats, error) {
	t := newTimer()
	resp, err := postJSON(ctx, p.base+"/v1/messages", p.body(cr), p.headers())
	if err != nil {
		return nil, fmt.Errorf("cannot reach Anthropic: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, apiError(resp)
	}
	st := &Stats{}
	var streamErr error
	err = readSSE(resp.Body, func(event, data string) error {
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Usage struct {
					Input int `json:"input_tokens"`
					Cache int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Delta struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				Thinking   string `json:"thinking"`
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				Output int `json:"output_tokens"`
			} `json:"usage"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			return nil
		}
		switch ev.Type {
		case "message_start":
			st.InputTokens = ev.Message.Usage.Input + ev.Message.Usage.Cache
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				t.tick()
				onDelta(Delta{Content: ev.Delta.Text})
			case "thinking_delta":
				t.tick()
				onDelta(Delta{Thinking: ev.Delta.Thinking})
			}
		case "message_delta":
			if ev.Usage.Output > 0 {
				st.OutputTokens = ev.Usage.Output
			}
			switch ev.Delta.StopReason {
			case "max_tokens":
				st.DoneReason = "length"
			case "":
			default:
				st.DoneReason = "stop"
			}
		case "message_stop":
			return errStopSSE
		case "error":
			streamErr = fmt.Errorf("%s: %s", ev.Error.Type, ev.Error.Message)
			return errStopSSE
		}
		return nil
	})
	if streamErr != nil {
		return nil, streamErr
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("stream interrupted: %w", err)
	}
	t.finish(st)
	return st, nil
}
