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
	Provider string // "" (off) | openai | custom
	APIKey   string
	Model    string
	BaseURL  string
}

// DefaultEmbedModel is the default model of the OpenAI-compatible provider.
const DefaultEmbedModel = "text-embedding-3-small"

// EmbedConfigFromEnv reads EMBEDDINGS_PROVIDER, EMBEDDINGS_API_KEY,
// EMBEDDINGS_MODEL and EMBEDDINGS_BASE_URL. A provider without a key is off.
func EmbedConfigFromEnv() EmbedConfig {
	c := EmbedConfig{Provider: strings.ToLower(strings.TrimSpace(os.Getenv("EMBEDDINGS_PROVIDER"))), APIKey: strings.TrimSpace(os.Getenv("EMBEDDINGS_API_KEY")),
		Model: strings.TrimSpace(os.Getenv("EMBEDDINGS_MODEL")), BaseURL: strings.TrimSpace(os.Getenv("EMBEDDINGS_BASE_URL"))}
	if c.Model == "" {
		c.Model = DefaultEmbedModel
	}
	if c.Provider == "" && c.APIKey != "" {
		c.Provider = "openai"
	}
	if c.Provider != "openai" && c.Provider != "custom" || c.APIKey == "" {
		c.Provider = ""
	}
	return c
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

// embedCapable are the BYO providers whose API serves /embeddings.
var embedCapable = []string{"openai", "custom"}

// NewResolver applies the rules of docs/architecture/agent-search.md:
//  1. an organization on its own key (byo_only) never uses the platform's
//     key: its openai/custom credential embeds, any other provider means
//     keyword only;
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
				m := c.Platform.Model
				if m == "" {
					m = DefaultEmbedModel
				}
				_ = model // the chat model of the credential is not an embeddings model
				return EmbedChoice{Embedder: &OpenAIEmbedder{ProviderID: prov, Key: key, ModelName: m, BaseURL: base, OrgRef: ref, HTTP: c.HTTP}}
			}
		}
		if c.Platform.Provider == "" {
			return EmbedChoice{Reason: ReasonEmbedOff, Note: "no embeddings provider is configured: search is keyword only"}
		}
		if !allowed(c.Platform.Provider) {
			return EmbedChoice{Reason: ReasonEmbedPolicy, Note: "the data policy does not allow sending text to " + c.Platform.Provider + ": search is keyword only"}
		}
		return EmbedChoice{Embedder: &OpenAIEmbedder{ProviderID: c.Platform.Provider, Key: c.Platform.APIKey, ModelName: c.Platform.Model,
			BaseURL: c.Platform.BaseURL, HTTP: c.HTTP}}
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
