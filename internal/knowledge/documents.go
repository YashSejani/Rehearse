package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Chunk is one piece of a document, small enough to embed and to show in a prompt.
type Chunk struct {
	Kind   string // runbook, readme, incident
	Source string
	Title  string
	Text   string
}

// Doc is a search result.
type Doc struct {
	Kind     string  `json:"kind"`
	Source   string  `json:"source"`
	Title    string  `json:"title"`
	Text     string  `json:"text"`
	Distance float64 `json:"distance"` // cosine distance: 0 = same meaning, 2 = opposite
}

const maxChunk = 900 // characters; ~225 tokens

// ChunkMarkdown splits a markdown file at headings (## …), and long sections at blank
// lines, so each chunk is about one topic. The title is "<doc title> / <section>".
func ChunkMarkdown(kind, source, text string) []Chunk {
	docTitle := filepath.Base(source)
	var chunks []Chunk
	section, body := "", &strings.Builder{}
	flush := func() {
		t := strings.TrimSpace(body.String())
		body.Reset()
		if t == "" {
			return
		}
		title := docTitle
		if section != "" {
			title += " / " + section
		}
		for _, part := range splitLong(t, maxChunk) {
			chunks = append(chunks, Chunk{Kind: kind, Source: source, Title: title, Text: part})
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "# ") {
			flush()
			docTitle = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			continue
		}
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") {
			flush()
			section = strings.TrimSpace(strings.TrimLeft(line, "# "))
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	flush()
	return chunks
}

func splitLong(s string, n int) []string {
	if len(s) <= n {
		return []string{s}
	}
	var out []string
	cur := &strings.Builder{}
	for _, para := range strings.Split(s, "\n\n") {
		if cur.Len()+len(para) > n && cur.Len() > 0 {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
		for len(para) > n { // one huge paragraph: hard cut
			out = append(out, para[:n])
			para = para[n:]
		}
		cur.WriteString(para)
		cur.WriteString("\n\n")
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, strings.TrimSpace(cur.String()))
	}
	return out
}

// LoadSources reads markdown files (or every .md under a directory). Files under a
// "runbooks" directory are kind runbook; others are readme.
func LoadSources(paths []string) ([]Chunk, error) {
	var chunks []Chunk
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("source %s: %w", p, err)
		}
		files := []string{p}
		if info.IsDir() {
			files, _ = filepath.Glob(filepath.Join(p, "*.md"))
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			kind := "readme"
			if strings.Contains(filepath.ToSlash(f), "runbooks/") {
				kind = "runbook"
			}
			chunks = append(chunks, ChunkMarkdown(kind, filepath.ToSlash(f), string(data))...)
		}
	}
	return chunks, nil
}

func chunkHash(c Chunk) string {
	h := sha256.Sum256([]byte(c.Source + "\x00" + c.Title + "\x00" + c.Text))
	return hex.EncodeToString(h[:])
}

// Ingest embeds and stores chunks; chunks already stored (same hash) are skipped, and old
// chunks of the same sources that no longer exist are deleted. Returns (added, removed).
func (s *Store) Ingest(ctx context.Context, e Embedder, chunks []Chunk) (int, int, error) {
	if len(chunks) == 0 {
		return 0, 0, nil
	}
	keep := map[string]bool{}
	sources := map[string]bool{}
	var todo []Chunk
	for _, c := range chunks {
		h := chunkHash(c)
		keep[h] = true
		sources[c.Source] = true
		var exists bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM documents WHERE hash=$1)`, h).Scan(&exists); err != nil {
			return 0, 0, err
		}
		if !exists {
			todo = append(todo, c)
		}
	}
	if len(todo) > 0 {
		texts := make([]string, len(todo))
		for i, c := range todo {
			texts[i] = DocPrefix + c.Title + "\n" + c.Text
		}
		vecs, err := e.Embed(ctx, texts)
		if err != nil {
			return 0, 0, err
		}
		for i, c := range todo {
			if _, err := s.Pool.Exec(ctx, `INSERT INTO documents(kind, source, title, chunk, hash, embedding)
				VALUES ($1,$2,$3,$4,$5,$6::vector) ON CONFLICT (hash) DO NOTHING`,
				c.Kind, c.Source, c.Title, c.Text, chunkHash(c), vectorLiteral(vecs[i])); err != nil {
				return 0, 0, fmt.Errorf("insert chunk: %w", err)
			}
		}
	}
	removed := 0
	for src := range sources {
		rows, err := s.Pool.Query(ctx, `SELECT hash FROM documents WHERE source=$1`, src)
		if err != nil {
			return 0, 0, err
		}
		var stale []string
		for rows.Next() {
			var h string
			_ = rows.Scan(&h)
			if !keep[h] {
				stale = append(stale, h)
			}
		}
		rows.Close()
		for _, h := range stale {
			if _, err := s.Pool.Exec(ctx, `DELETE FROM documents WHERE hash=$1`, h); err == nil {
				removed++
			}
		}
	}
	return len(todo), removed, nil
}

// Search returns the k chunks closest in meaning to the query.
func (s *Store) Search(ctx context.Context, e Embedder, query string, k int) ([]Doc, error) {
	vecs, err := e.Embed(ctx, []string{QueryPrefix + query})
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT kind, source, title, chunk, embedding <=> $1::vector AS d
		FROM documents ORDER BY d LIMIT $2`, vectorLiteral(vecs[0]), k)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()
	var out []Doc
	for rows.Next() {
		var d Doc
		if err := rows.Scan(&d.Kind, &d.Source, &d.Title, &d.Text, &d.Distance); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
