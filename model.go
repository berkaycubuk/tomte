package tomte

// A minimal client for an OpenAI-compatible chat completions API with tool
// calling. OpenRouter, z.ai, Groq, Ollama and most others serve this API;
// a provider is its base URL, a model name and a key.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type toolSpec struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// Usage is the tokens one or more model calls took.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

func (u *Usage) add(o Usage) {
	u.PromptTokens += o.PromptTokens
	u.CompletionTokens += o.CompletionTokens
}

type model struct {
	baseURL string
	name    string
	key     string
	http    *http.Client
}

type chatRequest struct {
	Model    string     `json:"model"`
	Messages []message  `json:"messages"`
	Tools    []toolSpec `json:"tools,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message      message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// chat sends the conversation and returns the model's next message.
// Network errors, 429 and 5xx are retried with backoff.
func (m *model) chat(ctx context.Context, msgs []message, tools []toolSpec) (message, Usage, error) {
	body, err := json.Marshal(chatRequest{Model: m.name, Messages: msgs, Tools: tools})
	if err != nil {
		return message{}, Usage{}, err
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			case <-ctx.Done():
				return message{}, Usage{}, ctx.Err()
			}
		}
		msg, usage, retry, err := m.post(ctx, body)
		if err == nil {
			return msg, usage, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return message{}, Usage{}, lastErr
}

func (m *model) post(ctx context.Context, body []byte) (message, Usage, bool, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(m.baseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return message{}, Usage{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.key != "" {
		req.Header.Set("Authorization", "Bearer "+m.key)
	}

	resp, err := m.http.Do(req)
	if err != nil {
		return message{}, Usage{}, ctx.Err() == nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return message{}, Usage{}, true, err
	}
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return message{}, Usage{}, true, fmt.Errorf("model: %s: %s", resp.Status, snippet(raw))
	}

	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return message{}, Usage{}, false, fmt.Errorf("model: %s: decode: %v: %s", resp.Status, err, snippet(raw))
	}
	if cr.Error != nil {
		return message{}, Usage{}, false, fmt.Errorf("model: %s: %s", resp.Status, cr.Error.Message)
	}
	if resp.StatusCode != 200 {
		return message{}, Usage{}, false, fmt.Errorf("model: %s: %s", resp.Status, snippet(raw))
	}
	if len(cr.Choices) == 0 {
		return message{}, Usage{}, false, errors.New("model: no choices in response")
	}
	return cr.Choices[0].Message, cr.Usage, false, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = headBytes(s, 300) + "…"
	}
	return s
}
