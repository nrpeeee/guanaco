package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// ollamaProvider keeps everything Ollama-specific: native options (top_k,
// repeat_penalty, num_ctx…), keep_alive, think, and Ollama's own timing stats.
type ollamaProvider struct {
	id   string
	base string
}

var (
	ollamaCapsMu sync.Mutex
	ollamaCaps   = map[string][]string{}
)

func (p *ollamaProvider) capabilities(ctx context.Context, model string) []string {
	ollamaCapsMu.Lock()
	c, ok := ollamaCaps[p.base+"|"+model]
	ollamaCapsMu.Unlock()
	if ok {
		return c
	}
	bb, _ := json.Marshal(map[string]string{"model": model})
	req, _ := http.NewRequestWithContext(ctx, "POST", p.base+"/api/show", bytes.NewReader(bb))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ollamaClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var s struct {
		Capabilities []string `json:"capabilities"`
	}
	json.NewDecoder(resp.Body).Decode(&s)
	ollamaCapsMu.Lock()
	ollamaCaps[p.base+"|"+model] = s.Capabilities
	ollamaCapsMu.Unlock()
	return s.Capabilities
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (p *ollamaProvider) Models(ctx context.Context) ([]ModelEntry, error) {
	var tags struct {
		Models []struct {
			Name    string          `json:"name"`
			Size    int64           `json:"size"`
			Details json.RawMessage `json:"details"`
		} `json:"models"`
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", p.base+"/api/tags", nil)
	resp, err := ollamaClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Ollama at %s", p.base)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s", ollamaError(resp))
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, err
	}
	out := make([]ModelEntry, 0, len(tags.Models))
	for _, m := range tags.Models {
		out = append(out, ModelEntry{Provider: p.id, ID: m.Name, Name: m.Name, Local: true, Size: m.Size, Details: m.Details})
	}
	return out, nil
}

func (p *ollamaProvider) Stream(ctx context.Context, cr *ChatRequest, onDelta func(Delta)) (*Stats, error) {
	type ofn struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	type otc struct {
		Function ofn `json:"function"`
	}
	type omsg struct {
		Role      string   `json:"role"`
		Content   string   `json:"content"`
		Images    []string `json:"images,omitempty"`
		ToolCalls []otc    `json:"tool_calls,omitempty"`
		ToolName  string   `json:"tool_name,omitempty"`
	}
	msgs := make([]omsg, 0, len(cr.Messages))
	for _, m := range cr.Messages {
		o := omsg{Role: m.Role, Content: m.Content}
		for _, tc := range m.ToolCalls {
			o.ToolCalls = append(o.ToolCalls, otc{Function: ofn{Name: tc.Name, Arguments: argsOrEmpty(tc.Args)}})
		}
		if m.Role == "tool" {
			o.ToolName = m.Name
		}
		for _, im := range m.Images {
			o.Images = append(o.Images, im.Data)
		}
		msgs = append(msgs, o)
	}
	opts := map[string]any{}
	pr := cr.Params
	if pr.Temperature != nil {
		opts["temperature"] = *pr.Temperature
	}
	if pr.TopP != nil {
		opts["top_p"] = *pr.TopP
	}
	if pr.TopK != nil {
		opts["top_k"] = *pr.TopK
	}
	if pr.RepeatPenalty != nil {
		opts["repeat_penalty"] = *pr.RepeatPenalty
	}
	if pr.NumCtx != nil && *pr.NumCtx > 0 {
		opts["num_ctx"] = *pr.NumCtx
	}
	if pr.MaxTokens != nil {
		opts["num_predict"] = *pr.MaxTokens
	}
	if pr.Seed != nil {
		opts["seed"] = *pr.Seed
	}
	body := map[string]any{"model": cr.Model, "messages": msgs, "stream": true}
	if len(opts) > 0 {
		body["options"] = opts
	}
	if cr.Think != nil {
		// Sending think to a model without the capability is an error in Ollama;
		// "off" for a non-thinking model is simply a no-op, so skip it.
		if *cr.Think || has(p.capabilities(ctx, cr.Model), "thinking") {
			body["think"] = *cr.Think
		}
	}
	if cr.KeepAlive != "" {
		body["keep_alive"] = cr.KeepAlive
	}
	if len(cr.Tools) > 0 {
		var tl []map[string]any
		for _, t := range cr.Tools {
			tl = append(tl, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Schema}})
		}
		body["tools"] = tl
	}
	bb, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", p.base+"/api/chat", bytes.NewReader(bb))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ollamaClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Ollama at %s", p.base)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s", ollamaError(resp))
	}
	toolSeq := 0
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var ch struct {
			Message struct {
				Content   string `json:"content"`
				Thinking  string `json:"thinking"`
				ToolCalls []struct {
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			Done            bool   `json:"done"`
			DoneReason      string `json:"done_reason"`
			Error           string `json:"error"`
			TotalDuration   int64  `json:"total_duration"`
			LoadDuration    int64  `json:"load_duration"`
			PromptEvalCount int    `json:"prompt_eval_count"`
			EvalCount       int    `json:"eval_count"`
			EvalDuration    int64  `json:"eval_duration"`
		}
		if json.Unmarshal(sc.Bytes(), &ch) != nil {
			continue
		}
		if ch.Error != "" {
			return nil, fmt.Errorf("%s", ch.Error)
		}
		if ch.Message.Content != "" || ch.Message.Thinking != "" {
			onDelta(Delta{Content: ch.Message.Content, Thinking: ch.Message.Thinking})
		}
		if len(ch.Message.ToolCalls) > 0 {
			var tcs []ToolCall
			for _, tc := range ch.Message.ToolCalls {
				toolSeq++
				tcs = append(tcs, ToolCall{ID: fmt.Sprintf("call_%d", toolSeq), Name: tc.Function.Name, Args: argsOrEmpty(tc.Function.Arguments)})
			}
			onDelta(Delta{ToolCalls: tcs})
		}
		if ch.Done {
			s := &Stats{InputTokens: ch.PromptEvalCount, OutputTokens: ch.EvalCount, TotalMs: ch.TotalDuration / 1e6, LoadMs: ch.LoadDuration / 1e6, DoneReason: ch.DoneReason}
			if ch.EvalDuration > 0 {
				s.TPS = float64(ch.EvalCount) / (float64(ch.EvalDuration) / 1e9)
			}
			return s, nil
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return nil, fmt.Errorf("stream interrupted: %w", err)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return &Stats{}, nil
}
