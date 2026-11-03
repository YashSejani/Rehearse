package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChat(t *testing.T) {
	var got Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("path %s auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[
			{"id":"c1","type":"function","function":{"name":"get_logs","arguments":"{\"service\":\"geo\"}"}}]},
			"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":12}}`))
	}))
	defer srv.Close()

	c := New(srv.URL+"/v1/", "k")
	resp, err := c.Chat(context.Background(), Request{
		Model:    "m",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools:    []Tool{{Type: "function", Function: FunctionDef{Name: "get_logs"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "m" || len(got.Tools) != 1 || got.Temperature != 0 {
		t.Errorf("request sent: %+v", got)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Function.Name != "get_logs" {
		t.Errorf("tool calls %+v", resp.Message.ToolCalls)
	}
	if resp.Usage.PromptTokens != 100 || resp.FinishReason != "tool_calls" {
		t.Errorf("usage %+v finish %s", resp.Usage, resp.FinishReason)
	}
}

func TestChatError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"model \"x\" not found"}}`))
	}))
	defer srv.Close()
	_, err := New(srv.URL, "").Chat(context.Background(), Request{Model: "x"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("want not-found error, got %v", err)
	}
}
