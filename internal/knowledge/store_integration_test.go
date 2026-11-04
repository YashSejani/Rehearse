//go:build integration

package knowledge

import (
	"context"
	"hash/fnv"
	"os"
	"strings"
	"testing"
)

// bagEmbedder is a deterministic stand-in for nomic-embed-text: a bag of hashed words,
// so texts sharing words are close. Enough to test SQL and ranking without Ollama.
type bagEmbedder struct{}

func (bagEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 768)
		for _, w := range strings.Fields(strings.ToLower(t)) {
			h := fnv.New32a()
			_, _ = h.Write([]byte(w))
			v[h.Sum32()%768] += 1
		}
		out[i] = v
	}
	return out, nil
}

// Run with: REHEARSE_TEST_DSN=postgres://... go test -tags integration ./internal/knowledge
func TestStoreIngestSearch(t *testing.T) {
	dsn := os.Getenv("REHEARSE_TEST_DSN")
	if dsn == "" {
		t.Skip("REHEARSE_TEST_DSN not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	chunks := []Chunk{
		{Kind: "runbook", Source: "test/a.md", Title: "oom", Text: "container killed out of memory raise memory limit"},
		{Kind: "runbook", Source: "test/b.md", Title: "auth", Text: "database login failed not authorized grant roles"},
	}
	if _, _, err := st.Ingest(ctx, bagEmbedder{}, chunks); err != nil {
		t.Fatal(err)
	}
	added, _, _ := st.Ingest(ctx, bagEmbedder{}, chunks)
	if added != 0 {
		t.Errorf("re-ingest should add nothing, added %d", added)
	}
	docs, err := st.Search(ctx, bagEmbedder{}, "database login failed", 1)
	if err != nil || len(docs) != 1 || docs[0].Title != "auth" {
		t.Fatalf("search: %+v %v", docs, err)
	}
	_, _ = st.Pool.Exec(ctx, `DELETE FROM documents WHERE source LIKE 'test/%'`)
}
