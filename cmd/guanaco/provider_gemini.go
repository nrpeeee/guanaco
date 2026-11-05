package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

type geminiProvider struct {
	id   string
	base string
	key  string
}

var reGeminiThinks = regexp.MustCompile(`gemini-(2\.5|[3-9])`)

func (p *geminiProvider) headers() map[string]string {
	return map[string]string{"x-goog-api-key": p.key}
}

func (p *geminiProvider) Models(ctx context.Context) ([]ModelEntry, error) {
	var out struct {
		Models []struct {
			Name        string   `json:"name"`
			DisplayName string   `json:"displayName"`
			Methods     []string `json:"supportedGenerationMethods"`
			InputLimit  int      `json:"inputTokenLimit"`
			Thinking    *bool    `json:"thinking"`
		} `json:"models"`
	}
	if err := getJSON(ctx, p.base+"/v1beta/models?pageSize=1000", p.headers(), &out); err != nil {
		return nil, err
	}
	var ms []ModelEntry
	for _, m := range out.Models {
		id := strings.TrimPrefix(m.Name, "models/")
		if !has(m.Methods, "generateContent") || strings.Contains(id, "embedding") || strings.Contains(id, "imagen") || strings.Contains(id, "-tts") {
			continue
		}
		th := m.Thinking
		if th == nil {
			th = boolp(reGeminiThinks.MatchString(id))
		}
		name := m.DisplayName
		if name == "" {
			name = id
		}
		ms = append(ms, ModelEntry{Provider: p.id, ID: id, Name: name, Vision: boolp(true), Thinking: th, Context: m.InputLimit})
	}
	return ms, nil
}

func (p *geminiProvider) body(cr *ChatRequest) map[string]any {
	system, rest := splitSystem(cr.Messages)
	type gmsg struct {
		Role  string           `json:"role"`
		Parts []map[string]any `json:"parts"`
	}
	var contents []gmsg
	for _, m := range rest {
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		var parts []map[string]any
		if m.Content != "" {
			parts = append(parts, map[string]any{"text": m.Content})
		}
		if role == "user" {
			for _, im := range m.Images {
				parts = append(parts, map[string]any{"inline_data": map[string]string{"mime_type": im.MimeType(), "data": im.Data}})
			}
		}
		if len(parts) == 0 {
			continue
		}
		if n := len(contents); n > 0 && contents[n-1].Role == role {
			contents[n-1].Parts = append(contents[n-1].Parts, parts...)
			continue
		}
		contents = append(contents, gmsg{Role: role, Parts: parts})
	}
	gc := map[string]any{}
	pr := cr.Params
	if pr.Temperature != nil {
		gc["temperature"] = *pr.Temperature
	}
	if pr.TopP != nil {
		gc["topP"] = *pr.TopP
	}
	if pr.TopK != nil {
		gc["topK"] = *pr.TopK
	}
	if pr.MaxTokens != nil && *pr.MaxTokens > 0 {
		gc["maxOutputTokens"] = *pr.MaxTokens
	}
	if pr.Seed != nil {
		gc["seed"] = *pr.Seed
	}
	thinks := reGeminiThinks.MatchString(cr.Model)
	canDisable := strings.Contains(cr.Model, "2.5-flash")
	switch {
	case cr.Aux && thinks && canDisable:
		gc["thinkingConfig"] = map[string]any{"thinkingBudget": 0}
	case cr.Aux && thinks:
		gc["maxOutputTokens"] = 2048 // thinking tokens count toward the cap
	case cr.Think != nil && *cr.Think && thinks:
		gc["thinkingConfig"] = map[string]any{"includeThoughts": true}
	case cr.Think != nil && !*cr.Think && canDisable:
		gc["thinkingConfig"] = map[string]any{"thinkingBudget": 0}
	}
	b := map[string]any{"contents": contents}
	if system != "" {
		b["systemInstruction"] = map[string]any{"parts": []map[string]string{{"text": system}}}
	}
	if len(gc) > 0 {
		b["generationConfig"] = gc
	}
	return b
}

func (p *geminiProvider) Stream(ctx context.Context, cr *ChatRequest, onDelta func(Delta)) (*Stats, error) {
	t := newTimer()
	u := p.base + "/v1beta/models/" + url.PathEscape(strings.TrimPrefix(cr.Model, "models/")) + ":streamGenerateContent?alt=sse"
	resp, err := postJSON(ctx, u, p.body(cr), p.headers())
	if err != nil {
		return nil, fmt.Errorf("cannot reach Gemini: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, apiError(resp)
	}
	st := &Stats{}
	var streamErr error
	err = readSSE(resp.Body, func(_, data string) error {
		var ch struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text    string `json:"text"`
						Thought bool   `json:"thought"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			Usage *struct {
				Prompt   int `json:"promptTokenCount"`
				Output   int `json:"candidatesTokenCount"`
				Thoughts int `json:"thoughtsTokenCount"`
			} `json:"usageMetadata"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			PromptFeedback *struct {
				BlockReason string `json:"blockReason"`
			} `json:"promptFeedback"`
		}
		if json.Unmarshal([]byte(data), &ch) != nil {
			return nil
		}
		if ch.Error != nil {
			streamErr = fmt.Errorf("%s", ch.Error.Message)
			return errStopSSE
		}
		if ch.PromptFeedback != nil && ch.PromptFeedback.BlockReason != "" {
			streamErr = fmt.Errorf("prompt blocked by Gemini (%s)", ch.PromptFeedback.BlockReason)
			return errStopSSE
		}
		for _, c := range ch.Candidates {
			for _, part := range c.Content.Parts {
				if part.Text == "" {
					continue
				}
				t.tick()
				if part.Thought {
					onDelta(Delta{Thinking: part.Text})
				} else {
					onDelta(Delta{Content: part.Text})
				}
			}
			switch c.FinishReason {
			case "":
			case "MAX_TOKENS":
				st.DoneReason = "length"
			default:
				st.DoneReason = "stop"
			}
		}
		if ch.Usage != nil {
			st.InputTokens = ch.Usage.Prompt
			st.OutputTokens = ch.Usage.Output + ch.Usage.Thoughts
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
