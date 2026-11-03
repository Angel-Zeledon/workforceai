package docindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Embedder turns texts into vectors. Implementations must be safe for
// concurrent use. tokens is what the provider billed (0 if unknown).
type Embedder interface {
	Embed(ctx context.Context, texts []string) (vecs [][]float32, tokens int, err error)
	Provider() string
	Model() string
}

// QueryEmbedder is implemented by embedders whose model embeds a search query
// differently from a document (Gemini: prompt prefixes). Callers embedding a
// query use EmbedQuery when available, Embed otherwise.
type QueryEmbedder interface {
	EmbedQuery(ctx context.Context, texts []string) (vecs [][]float32, tokens int, err error)
}

// embedQuery embeds search queries with the query form when the embedder has one.
func embedQuery(ctx context.Context, e Embedder, texts []string) ([][]float32, int, error) {
	if q, ok := e.(QueryEmbedder); ok {
		return q.EmbedQuery(ctx, texts)
	}
	return e.Embed(ctx, texts)
}

// PricePerM is the default USD price per million tokens of a provider's
// embeddings (EMBEDDINGS_PRICE_PER_M overrides it for every provider).
func PricePerM(provider string) float64 {
	if provider == "gemini" {
		return DefaultGeminiEmbedPrice
	}
	return DefaultOpenAIEmbedPrice
}

// EmbedChoice is the embedder an organization may use right now, or why it
// stays keyword only (Reason is one of the Reason* codes; Note is plain English).
type EmbedChoice struct {
	Embedder Embedder
	Reason   string
	Note     string
}

// EmbedResolver picks the embedder of an organization (data policy and keys).
type EmbedResolver func(ctx context.Context, org string) EmbedChoice

// EmbedConfig is the platform embeddings provider (environment).
type EmbedConfig struct {
	Provider string // "" (off) | openai | custom | gemini
	APIKey   string
	Model    string
	BaseURL  string
	// Dimensions is the output size asked from Gemini (128-3072, default 1536);
	// OpenAI-compatible models keep their native size.
	Dimensions int
}

// DefaultEmbedModel is the default model of the OpenAI-compatible provider.
const DefaultEmbedModel = "text-embedding-3-small"

// EmbedConfigFromEnv reads EMBEDDINGS_PROVIDER, EMBEDDINGS_API_KEY,
// EMBEDDINGS_MODEL, EMBEDDINGS_BASE_URL and EMBEDDINGS_DIMENSIONS. A provider
// without a key is off. With EMBEDDINGS_PROVIDER=gemini the key defaults to
// GEMINI_API_KEY (the same key as the chat models), the model to
// gemini-embedding-2 and the dimensions to 1536.
func EmbedConfigFromEnv() EmbedConfig {
	c := EmbedConfig{Provider: strings.ToLower(strings.TrimSpace(os.Getenv("EMBEDDINGS_PROVIDER"))), APIKey: strings.TrimSpace(os.Getenv("EMBEDDINGS_API_KEY")),
		Model: strings.TrimSpace(os.Getenv("EMBEDDINGS_MODEL")), BaseURL: strings.TrimSpace(os.Getenv("EMBEDDINGS_BASE_URL"))}
	if c.Provider == "" && c.APIKey != "" {
		c.Provider = "openai"
	}
	if c.Provider == "gemini" {
		if c.APIKey == "" {
			c.APIKey = strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
		}
		if c.Model == "" {
			c.Model = DefaultGeminiEmbedModel
		}
		c.Dimensions = DefaultGeminiDims
		if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("EMBEDDINGS_DIMENSIONS"))); err == nil {
			c.Dimensions = normDims(v)
		}
	} else if c.Model == "" {
		c.Model = DefaultEmbedModel
	}
	if c.Provider != "openai" && c.Provider != "custom" && c.Provider != "gemini" || c.APIKey == "" {
		c.Provider = ""
	}
	return c
}

// newEmbedder builds the embedder of a provider ("gemini" or OpenAI-compatible).
func newEmbedder(provider, key, model, baseURL, orgRef string, dims int, cl *http.Client) Embedder {
	if provider == "gemini" {
		return &GeminiEmbedder{Key: key, ModelName: model, Dims: dims, BaseURL: baseURL, HTTP: cl}
	}
	return &OpenAIEmbedder{ProviderID: provider, Key: key, ModelName: model, BaseURL: baseURL, OrgRef: orgRef, HTTP: cl}
}

// OpenAIEmbedder calls an OpenAI-compatible POST {base}/embeddings.
type OpenAIEmbedder struct {
	ProviderID string // "openai" | "custom" (the data policy name)
	Key        string
	ModelName  string
	BaseURL    string // default https://api.openai.com/v1
	OrgRef     string // optional OpenAI organization
	HTTP       *http.Client
}

// MaxBatch is the number of texts per embeddings call.
const MaxBatch = 64

func (e *OpenAIEmbedder) Provider() string { return e.ProviderID }
func (e *OpenAIEmbedder) Model() string    { return e.ModelName }

// String never prints the key.
func (e *OpenAIEmbedder) String() string {
	return fmt.Sprintf("OpenAIEmbedder{provider:%s model:%s key:[REDACTED]}", e.ProviderID, e.ModelName)
}

// ErrEmbed is returned for any provider failure (the message never carries the key or the texts).
var ErrEmbed = errors.New("embeddings provider error")

func (e *OpenAIEmbedder) endpoint() (string, error) {
	base := strings.TrimRight(e.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("%w: invalid base URL", ErrEmbed)
	}
	return base + "/embeddings", nil
}

// Embed implements Embedder (batches of MaxBatch, bounded response).
func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, int, error) {
	ep, err := e.endpoint()
	if err != nil {
		return nil, 0, err
	}
	cl := e.HTTP
	if cl == nil {
		cl = &http.Client{Timeout: 30 * time.Second}
	}
	out := make([][]float32, 0, len(texts))
	tokens := 0
	for len(texts) > 0 {
		n := min(MaxBatch, len(texts))
		batch := texts[:n]
		texts = texts[n:]
		body, _ := json.Marshal(map[string]any{"model": e.ModelName, "input": batch})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep, bytes.NewReader(body))
		if err != nil {
			return nil, 0, fmt.Errorf("%w: %v", ErrEmbed, err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+e.Key)
		if e.OrgRef != "" {
			req.Header.Set("OpenAI-Organization", e.OrgRef)
		}
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
			Data []struct {
				Index     int       `json:"index"`
				Embedding []float32 `json:"embedding"`
			} `json:"data"`
			Usage struct {
				PromptTokens int `json:"prompt_tokens"`
				TotalTokens  int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(raw, &r); err != nil || len(r.Data) != len(batch) {
			return nil, 0, fmt.Errorf("%w: malformed response", ErrEmbed)
		}
		vs := make([][]float32, len(batch))
		for _, d := range r.Data {
			if d.Index < 0 || d.Index >= len(batch) || len(d.Embedding) == 0 || len(d.Embedding) > 8192 {
				return nil, 0, fmt.Errorf("%w: malformed vector", ErrEmbed)
			}
			vs[d.Index] = d.Embedding
		}
		out = append(out, vs...)
		tokens += max(r.Usage.TotalTokens, r.Usage.PromptTokens)
	}
	return out, tokens, nil
}

// ResolverConfig builds the standard EmbedResolver.
type ResolverConfig struct {
	Platform EmbedConfig
	// Allowed reports whether the organization's data policy lets its text go
	// to provider (allowed_providers within the server ceiling). nil = allowed.
	Allowed func(ctx context.Context, org, provider string) bool
	// OrgCredential returns the organization's own active model credential
	// (bring-your-own-model). byo=false when the organization uses the platform.
	OrgCredential func(ctx context.Context, org string) (provider, key, model, baseURL, orgRef string, byo bool)
	HTTP          *http.Client
}

// embedCapable are the BYO providers whose API serves embeddings.
var embedCapable = []string{"openai", "custom", "gemini"}

// NewResolver applies the rules of docs/architecture/agent-search.md:
//  1. an organization on its own key (byo_only) never uses the platform's
//     key: its openai/custom/gemini credential embeds, any other provider
//     means keyword only;
//  2. otherwise the platform provider (EMBEDDINGS_*) when the data policy allows it;
//  3. otherwise keyword only.
func NewResolver(c ResolverConfig) EmbedResolver {
	return func(ctx context.Context, org string) EmbedChoice {
		allowed := func(p string) bool { return c.Allowed == nil || c.Allowed(ctx, org, p) }
		if c.OrgCredential != nil {
			if prov, key, model, base, ref, byo := c.OrgCredential(ctx, org); byo {
				if !slices.Contains(embedCapable, prov) || key == "" {
					return EmbedChoice{Reason: ReasonEmbedPolicy, Note: "the organization uses its own " + prov + " key, which has no embeddings: search is keyword only"}
				}
				if !allowed(prov) {
					return EmbedChoice{Reason: ReasonEmbedPolicy, Note: "the data policy does not allow sending text to " + prov + ": search is keyword only"}
				}
				_ = model // the chat model of the credential is not an embeddings model
				if prov == "gemini" {
					// Its own Gemini key, never the platform's; the platform's model and size
					// only when the platform also embeds with Gemini.
					m, dims, gbase := DefaultGeminiEmbedModel, DefaultGeminiDims, ""
					if c.Platform.Provider == "gemini" {
						m, dims, gbase = c.Platform.Model, c.Platform.Dimensions, c.Platform.BaseURL
					}
					return EmbedChoice{Embedder: newEmbedder(prov, key, m, gbase, "", dims, c.HTTP)}
				}
				m := c.Platform.Model
				if m == "" || c.Platform.Provider == "gemini" {
					m = DefaultEmbedModel
				}
				return EmbedChoice{Embedder: newEmbedder(prov, key, m, base, ref, 0, c.HTTP)}
			}
		}
		if c.Platform.Provider == "" {
			return EmbedChoice{Reason: ReasonEmbedOff, Note: "no embeddings provider is configured: search is keyword only"}
		}
		if !allowed(c.Platform.Provider) {
			return EmbedChoice{Reason: ReasonEmbedPolicy, Note: "the data policy does not allow sending text to " + c.Platform.Provider + ": search is keyword only"}
		}
		return EmbedChoice{Embedder: newEmbedder(c.Platform.Provider, c.Platform.APIKey, c.Platform.Model, c.Platform.BaseURL, "", c.Platform.Dimensions, c.HTTP)}
	}
}

// envInt reads a positive integer environment variable.
func envInt(name string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && v > 0 {
		return v
	}
	return def
}

func envFloat(name string, def float64) float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64); err == nil && v >= 0 {
		return v
	}
	return def
}
