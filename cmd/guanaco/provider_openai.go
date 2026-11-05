package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// openAIProvider talks the Chat Completions protocol. With compat=true it is a
// generic "OpenAI-compatible" endpoint (LM Studio, llama.cpp server, vLLM,
// OpenRouter, Groq, …) and only sends the most widely supported fields.
type openAIProvider struct {
	id     string
	base   string
	key    string
	compat bool
	local  bool
	llama  bool // llama.cpp server: also accepts top_k / repeat_penalty
}

func (p *openAIProvider) headers() map[string]string {
	h := map[string]string{"Accept": "text/event-stream"}
	if p.key != "" {
		h["Authorization"] = "Bearer " + p.key
	}
	return h
}

func (p *openAIProvider) Models(ctx context.Context) ([]ModelEntry, error) {
	var out struct {
		Data []struct {
			ID   string `json:"id"`
			Meta struct {
				NCtxTrain int `json:"n_ctx_train"`
			} `json:"meta"`
		} `json:"data"`
	}
	if err := getJSON(ctx, p.base+"/models", p.headers(), &out); err != nil {
		return nil, err
	}
	var ms []ModelEntry
	for _, m := range out.Data {
		id := m.ID
		if p.compat {
			if strings.Contains(strings.ToLower(id), "embed") {
				continue
			}
			ms = append(ms, ModelEntry{Provider: p.id, ID: id, Name: id, Local: p.local, Context: m.Meta.NCtxTrain})
			continue
		}
		if !reOpenAIChat.MatchString(id) || reOpenAISkip.MatchString(id) {
			continue
		}
		reasoning := reOpenAIReasoning.MatchString(id)
		vision := strings.HasPrefix(id, "gpt-4o") || strings.HasPrefix(id, "gpt-4.1") || strings.HasPrefix(id, "gpt-5") || (reasoning && !strings.HasPrefix(id, "o1-mini") && !strings.HasPrefix(id, "o3-mini"))
		ms = append(ms, ModelEntry{Provider: p.id, ID: id, Name: id, Vision: boolp(vision), Thinking: boolp(reasoning)})
	}
	return ms, nil
}

// openAIBody builds the request; exported to tests via the function itself.
func (p *openAIProvider) body(cr *ChatRequest) map[string]any {
	type part struct {
		Type     string            `json:"type"`
		Text     string            `json:"text,omitempty"`
		ImageURL map[string]string `json:"image_url,omitempty"`
	}
	msgs := make([]map[string]any, 0, len(cr.Messages))
	for _, m := range cr.Messages {
		if m.Role == "tool" {
			msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": m.ToolCallID, "content": m.Content})
			continue
		}
		if len(m.ToolCalls) > 0 {
			var tcs []map[string]any
			for _, tc := range m.ToolCalls {
				tcs = append(tcs, map[string]any{"id": tc.ID, "type": "function", "function": map[string]any{"name": tc.Name, "arguments": string(argsOrEmpty(tc.Args))}})
			}
			msgs = append(msgs, map[string]any{"role": "assistant", "content": m.Content, "tool_calls": tcs})
			continue
		}
		if len(m.Images) == 0 || m.Role != "user" {
			msgs = append(msgs, map[string]any{"role": m.Role, "content": m.Content})
			continue
		}
		parts := []part{}
		if m.Content != "" {
			parts = append(parts, part{Type: "text", Text: m.Content})
		}
		for _, im := range m.Images {
			parts = append(parts, part{Type: "image_url", ImageURL: map[string]string{"url": "data:" + im.MimeType() + ";base64," + im.Data}})
		}
		msgs = append(msgs, map[string]any{"role": m.Role, "content": parts})
	}
	b := map[string]any{"model": cr.Model, "messages": msgs, "stream": true}
	reasoning := !p.compat && reOpenAIReasoning.MatchString(cr.Model)
	pr := cr.Params
	if !reasoning { // o-series / gpt-5 reject sampling params
		if pr.Temperature != nil {
			b["temperature"] = *pr.Temperature
		}
		if pr.TopP != nil {
			b["top_p"] = *pr.TopP
		}
	}
	maxTok := pr.MaxTokens
	if reasoning && cr.Aux { // reasoning tokens count against the cap
		n := 2048
		maxTok = &n
	}
	if maxTok != nil && *maxTok > 0 {
		if p.compat {
			b["max_tokens"] = *maxTok
		} else {
			b["max_completion_tokens"] = *maxTok
		}
	}
	if pr.Seed != nil {
		b["seed"] = *pr.Seed
	}
	if p.llama {
		if pr.TopK != nil {
			b["top_k"] = *pr.TopK
		}
		if pr.RepeatPenalty != nil {
			b["repeat_penalty"] = *pr.RepeatPenalty
		}
	}
	if reasoning && cr.Think != nil {
		if *cr.Think {
			b["reasoning_effort"] = "high"
		} else {
			b["reasoning_effort"] = "low"
		}
	}
	if !p.compat {
		b["stream_options"] = map[string]bool{"include_usage": true}
	}
	if len(cr.Tools) > 0 {
		var tl []map[string]any
		for _, t := range cr.Tools {
			tl = append(tl, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Schema}})
		}
		b["tools"] = tl
	}
	return b
}

func (p *openAIProvider) Stream(ctx context.Context, cr *ChatRequest, onDelta func(Delta)) (*Stats, error) {
	t := newTimer()
	resp, err := postJSON(ctx, p.base+"/chat/completions", p.body(cr), p.headers())
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", p.base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, apiError(resp)
	}
	st := &Stats{}
	var streamErr error
	type toolAcc struct {
		id, name string
		args     strings.Builder
	}
	acc := map[int]*toolAcc{}
	var order []int
	err = readSSE(resp.Body, func(_, data string) error {
		if data == "[DONE]" {
			return errStopSSE
		}
		var ch struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"` // DeepSeek / vLLM
					Reasoning        string `json:"reasoning"`         // OpenRouter / LM Studio
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				Prompt     int `json:"prompt_tokens"`
				Completion int `json:"completion_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &ch) != nil {
			return nil
		}
		if ch.Error != nil {
			streamErr = fmt.Errorf("%s", ch.Error.Message)
			return errStopSSE
		}
		for _, c := range ch.Choices {
			for _, tc := range c.Delta.ToolCalls { // arguments arrive in fragments, keyed by index
				a := acc[tc.Index]
				if a == nil {
					a = &toolAcc{}
					acc[tc.Index] = a
					order = append(order, tc.Index)
				}
				if tc.ID != "" {
					a.id = tc.ID
				}
				if tc.Function.Name != "" {
					a.name = tc.Function.Name
				}
				a.args.WriteString(tc.Function.Arguments)
			}
			th := c.Delta.ReasoningContent + c.Delta.Reasoning
			if c.Delta.Content != "" || th != "" {
				t.tick()
				onDelta(Delta{Content: c.Delta.Content, Thinking: th})
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				st.DoneReason = *c.FinishReason
			}
		}
		if ch.Usage != nil {
			st.InputTokens, st.OutputTokens = ch.Usage.Prompt, ch.Usage.Completion
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
	if len(order) > 0 {
		var tcs []ToolCall
		for i, idx := range order {
			a := acc[idx]
			id := a.id
			if id == "" {
				id = fmt.Sprintf("call_%d", i+1)
			}
			args := json.RawMessage(strings.TrimSpace(a.args.String()))
			if !json.Valid(args) {
				args = json.RawMessage("{}")
			}
			tcs = append(tcs, ToolCall{ID: id, Name: a.name, Args: argsOrEmpty(args)})
		}
		onDelta(Delta{ToolCalls: tcs})
	}
	if st.DoneReason != "" && st.DoneReason != "length" {
		st.DoneReason = "stop"
	}
	t.finish(st)
	return st, nil
}
