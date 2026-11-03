// Package llm is a minimal client for the OpenAI-compatible chat completions API
// (POST /v1/chat/completions). Ollama speaks it, and so do most hosted providers, so the
// agent can switch models by changing base_url and model in config.yaml.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Message struct {
	Role       string     `json:"role"` // system, user, assistant, tool
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON text
}

// Tool describes a function the model may call.
type Tool struct {
	Type     string      `json:"type"` // always "function"
	Function FunctionDef `json:"function"`
}

type FunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema
}

type Request struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Tools          []Tool          `json:"tools,omitempty"`
	Temperature    float64         `json:"temperature"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
}

type ResponseFormat struct {
	Type string `json:"type"` // "json_object"
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type Response struct {
	Message      Message
	FinishReason string
	Usage        Usage
	Latency      time.Duration
}

// Client calls one OpenAI-compatible endpoint.
type Client struct {
	BaseURL string // e.g. http://127.0.0.1:11434/v1
	APIKey  string // optional (hosted providers)
	HTTP    *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, HTTP: &http.Client{Timeout: 10 * time.Minute}}
}

type wireResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat sends one request and returns the first choice.
func (c *Client) Chat(ctx context.Context, req Request) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	start := time.Now()
	resp, err := c.HTTP.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var w wireResponse
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("chat HTTP %d: decode: %w", resp.StatusCode, err)
	}
	if w.Error != nil {
		return nil, fmt.Errorf("chat HTTP %d: %s", resp.StatusCode, w.Error.Message)
	}
	if resp.StatusCode != http.StatusOK || len(w.Choices) == 0 {
		return nil, fmt.Errorf("chat HTTP %d: no choices", resp.StatusCode)
	}
	return &Response{
		Message:      w.Choices[0].Message,
		FinishReason: w.Choices[0].FinishReason,
		Usage:        w.Usage,
		Latency:      time.Since(start),
	}, nil
}
