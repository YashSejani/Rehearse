package knowledge

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

// Embedder turns texts into vectors. Ollama serves nomic-embed-text on the
// OpenAI-compatible /v1/embeddings endpoint.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type OpenAIEmbedder struct {
	BaseURL string // e.g. http://127.0.0.1:11434/v1
	Model   string
	HTTP    *http.Client
}

func NewEmbedder(baseURL, model string) *OpenAIEmbedder {
	return &OpenAIEmbedder{BaseURL: strings.TrimRight(baseURL, "/"), Model: model, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

// nomic-embed-text was trained with task prefixes; using them improves search quality.
const (
	DocPrefix   = "search_document: "
	QueryPrefix = "search_query: "
)

// Embed sends texts in batches (CPU embedding is slow; one request per text would be slower).
func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	const batch = 16
	out := make([][]float32, 0, len(texts))
	for i := 0; i < len(texts); i += batch {
		j := min(i+batch, len(texts))
		body, _ := json.Marshal(map[string]any{"model": e.Model, "input": texts[i:j]})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.BaseURL+"/embeddings", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := e.HTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("embeddings: %w", err)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		var r struct {
			Data []struct {
				Index     int       `json:"index"`
				Embedding []float32 `json:"embedding"`
			} `json:"data"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("embeddings HTTP %d: %w", resp.StatusCode, err)
		}
		if r.Error != nil || len(r.Data) != j-i {
			msg := "wrong number of vectors"
			if r.Error != nil {
				msg = r.Error.Message
			}
			return nil, fmt.Errorf("embeddings HTTP %d: %s", resp.StatusCode, msg)
		}
		vecs := make([][]float32, j-i)
		for _, d := range r.Data {
			if d.Index >= 0 && d.Index < len(vecs) {
				vecs[d.Index] = d.Embedding
			}
		}
		out = append(out, vecs...)
	}
	return out, nil
}
