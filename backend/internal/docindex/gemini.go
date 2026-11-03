package docindex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Gemini embeddings (docs/architecture/agent-search.md, archivos-agentes.md §4).
//
// API (https://ai.google.dev/gemini-api/docs/embeddings, checked 2026-11-02):
// POST {base}/models/{model}:batchEmbedContents with the key in x-goog-api-key and
// {"requests":[{"model":"models/{model}","content":{"parts":[{"text":...}]},"outputDimensionality":N}]}.
// One request per text: several parts in one content would be aggregated into a
// single vector. The answer is {"embeddings":[{"values":[...]}],"usageMetadata":{"promptTokenCount":N}}.
//
// gemini-embedding-2 has no task_type: asymmetric retrieval is asked for with
// prompt prefixes, "title: none | text: ..." for documents and
// "task: search result | query: ..." for queries. Changing the prefixes, the model
// or the dimensions changes the vectors, so Model() carries model@dims and a
// change re-indexes (doc_index_state.embed_model, memory_docs.embed_model).

// Gemini defaults.
const (
	DefaultGeminiEmbedModel = "gemini-embedding-2"
	DefaultGeminiDims       = 1536
	DefaultGeminiBaseURL    = "https://generativelanguage.googleapis.com/v1beta"
	// Default prices in USD per million tokens (EMBEDDINGS_PRICE_PER_M overrides both).
	DefaultOpenAIEmbedPrice = 0.02
	DefaultGeminiEmbedPrice = 0.20
)

const (
	geminiDocPrefix   = "title: none | text: "
	geminiQueryPrefix = "task: search result | query: "
)

// GeminiEmbedder calls the Gemini API batchEmbedContents.
type GeminiEmbedder struct {
	Key       string
	ModelName string // default gemini-embedding-2
	Dims      int    // outputDimensionality, 128-3072 (default 1536)
	BaseURL   string // default https://generativelanguage.googleapis.com/v1beta
	HTTP      *http.Client
}

var (
	_ Embedder      = (*GeminiEmbedder)(nil)
	_ QueryEmbedder = (*GeminiEmbedder)(nil)
)

func (e *GeminiEmbedder) model() string {
	if m := strings.TrimPrefix(strings.TrimSpace(e.ModelName), "models/"); m != "" {
		return m
	}
	return DefaultGeminiEmbedModel
}

func (e *GeminiEmbedder) dims() int { return normDims(e.Dims) }

func normDims(d int) int {
	if d < 128 || d > 3072 {
		return DefaultGeminiDims
	}
	return d
}

// Provider is the data-policy name.
func (e *GeminiEmbedder) Provider() string { return "gemini" }

// Model is model@dims: vectors of another model or size are never compared.
func (e *GeminiEmbedder) Model() string { return e.model() + "@" + strconv.Itoa(e.dims()) }

// String never prints the key.
func (e *GeminiEmbedder) String() string {
	return fmt.Sprintf("GeminiEmbedder{model:%s key:[REDACTED]}", e.Model())
}

func (e *GeminiEmbedder) endpoint() (string, error) {
	base := strings.TrimRight(strings.TrimSpace(e.BaseURL), "/")
	if base == "" {
		base = DefaultGeminiBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("%w: invalid base URL", ErrEmbed)
	}
	return base + "/models/" + url.PathEscape(e.model()) + ":batchEmbedContents", nil
}

// Embed implements Embedder (documents).
func (e *GeminiEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, int, error) {
	return e.embed(ctx, texts, geminiDocPrefix)
}

// EmbedQuery implements QueryEmbedder (search queries).
func (e *GeminiEmbedder) EmbedQuery(ctx context.Context, texts []string) ([][]float32, int, error) {
	return e.embed(ctx, texts, geminiQueryPrefix)
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiReq struct {
	Model   string `json:"model"`
	Content struct {
		Parts []geminiPart `json:"parts"`
	} `json:"content"`
	OutputDimensionality int `json:"outputDimensionality"`
}

func (e *GeminiEmbedder) embed(ctx context.Context, texts []string, prefix string) ([][]float32, int, error) {
	ep, err := e.endpoint()
	if err != nil {
		return nil, 0, err
	}
	if e.Key == "" {
		return nil, 0, fmt.Errorf("%w: no key", ErrEmbed)
	}
	cl := e.HTTP
	if cl == nil {
		cl = &http.Client{Timeout: 30 * time.Second}
	}
	dims, model := e.dims(), "models/"+e.model()
	out := make([][]float32, 0, len(texts))
	tokens := 0
	for len(texts) > 0 {
		n := min(MaxBatch, len(texts))
		batch := texts[:n]
		texts = texts[n:]
		reqs := make([]geminiReq, len(batch))
		for i, t := range batch {
			reqs[i].Model, reqs[i].OutputDimensionality = model, dims
			reqs[i].Content.Parts = []geminiPart{{Text: prefix + t}}
		}
		body, _ := json.Marshal(map[string]any{"requests": reqs})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep, bytes.NewReader(body))
		if err != nil {
			return nil, 0, fmt.Errorf("%w: request", ErrEmbed)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", e.Key)
		res, err := cl.Do(req)
		if err != nil {
			return nil, 0, fmt.Errorf("%w: network", ErrEmbed)
		}
		raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
		res.Body.Close()
		if err != nil {
			return nil, 0, fmt.Errorf("%w: read", ErrEmbed)
		}
		if res.StatusCode != http.StatusOK {
			return nil, 0, fmt.Errorf("%w: status %d", ErrEmbed, res.StatusCode)
		}
		var r struct {
			Embeddings []struct {
				Values []float32 `json:"values"`
			} `json:"embeddings"`
			UsageMetadata struct {
				PromptTokenCount int `json:"promptTokenCount"`
			} `json:"usageMetadata"`
		}
		if err := json.Unmarshal(raw, &r); err != nil || len(r.Embeddings) != len(batch) {
			return nil, 0, fmt.Errorf("%w: malformed response", ErrEmbed)
		}
		for _, em := range r.Embeddings {
			if len(em.Values) != dims {
				return nil, 0, fmt.Errorf("%w: malformed vector (%d dimensions, want %d)", ErrEmbed, len(em.Values), dims)
			}
			v, ok := l2normalize(em.Values)
			if !ok {
				return nil, 0, fmt.Errorf("%w: malformed vector", ErrEmbed)
			}
			out = append(out, v)
		}
		tokens += r.UsageMetadata.PromptTokenCount
	}
	return out, tokens, nil
}

// l2normalize scales v to unit length (false for a zero or non-finite vector).
func l2normalize(v []float32) ([]float32, bool) {
	var s float64
	for _, x := range v {
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, false
		}
		s += f * f
	}
	if s == 0 {
		return nil, false
	}
	n := math.Sqrt(s)
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / n)
	}
	return out, true
}
