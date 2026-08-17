// Package llm is the provider-agnostic seam for model calls (ADR-0010, MOD-01):
// no business logic names a provider. Models are tiered (cheap classify vs strong
// generate/verify — MOD-02) and pinned per env, never "latest" (MOD-06); the
// verifier is a different model (MOD-03, ADR-0007). Provider outage or a non-2xx
// response fails to human review via ErrUnavailable (MOD-05), never to a partial
// or lower-quality autonomous answer. The concrete client speaks a
// Messages-API-compatible wire protocol against a configurable base URL, so any
// EU-resident, no-training provider (ADR-0028) plugs in through env — no secrets
// in code.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// ErrUnavailable signals the provider could not produce an answer (outage,
// degradation, non-2xx). Callers must route to human review (MOD-05).
var ErrUnavailable = errors.New("llm: provider unavailable")

// Message is one turn in a completion request.
type Message struct {
	Role    string
	Content string
}

// Request is a single model completion request. Model is chosen per call so the
// caller controls tiering and verifier selection (MOD-01/02/03).
type Request struct {
	Model     string
	System    string
	Messages  []Message
	MaxTokens int
}

// Response is a completed model reply.
type Response struct {
	Model      string
	Text       string
	StopReason string
}

// Provider is the only model-call seam. Implementations must map any failure that
// yields no valid answer to ErrUnavailable.
type Provider interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

// Models are the pinned model ids per tier (MOD-02, MOD-06).
type Models struct {
	Classify string // cheap: classification / screening / routing
	Generate string // strong: grounded generation
	Verify   string // strong, independent: verification (MOD-03)
}

// Config is the provider configuration, all from env — no secrets in code.
type Config struct {
	APIBase string
	APIKey  string
	Models  Models
}

// Validate fails closed on missing values and enforces MOD-03 (verifier != generator).
func (c Config) Validate() error {
	var missing []string
	for _, f := range []struct{ key, val string }{
		{"LLM_API_BASE", c.APIBase},
		{"LLM_API_KEY", c.APIKey},
		{"LLM_MODEL_CLASSIFY", c.Models.Classify},
		{"LLM_MODEL_GENERATE", c.Models.Generate},
		{"LLM_MODEL_VERIFY", c.Models.Verify},
	} {
		if strings.TrimSpace(f.val) == "" {
			missing = append(missing, f.key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("llm: missing required config: %s", strings.Join(missing, ", "))
	}
	if c.Models.Verify == c.Models.Generate {
		return errors.New("llm: LLM_MODEL_VERIFY must differ from LLM_MODEL_GENERATE (MOD-03: independent verifier)")
	}
	return nil
}

// LoadConfig reads the provider config from the environment and validates it.
func LoadConfig() (Config, error) {
	c := Config{
		APIBase: os.Getenv("LLM_API_BASE"),
		APIKey:  os.Getenv("LLM_API_KEY"),
		Models: Models{
			Classify: os.Getenv("LLM_MODEL_CLASSIFY"),
			Generate: os.Getenv("LLM_MODEL_GENERATE"),
			Verify:   os.Getenv("LLM_MODEL_VERIFY"),
		},
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// apiVersion pins the Messages API wire version. Model ids are pinned per env
// (MOD-06); the protocol version is pinned here.
const apiVersion = "2023-06-01"

// HTTPProvider is a Messages-API-compatible Provider over a configurable base URL.
type HTTPProvider struct {
	base   string
	apiKey string
	http   *http.Client
}

// NewHTTPProvider builds a provider against base (the API root). A nil client
// falls back to http.DefaultClient.
func NewHTTPProvider(base, apiKey string, client *http.Client) HTTPProvider {
	if client == nil {
		client = http.DefaultClient
	}
	return HTTPProvider{base: strings.TrimRight(base, "/"), apiKey: apiKey, http: client}
}

// FromConfig builds an HTTPProvider from a validated Config.
func FromConfig(c Config, client *http.Client) HTTPProvider {
	return NewHTTPProvider(c.APIBase, c.APIKey, client)
}

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type wireRequest struct {
	Model     string        `json:"model"`
	System    string        `json:"system,omitempty"`
	Messages  []wireMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
}

type wireResponse struct {
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// Complete performs one completion. Any transport error or non-2xx status is
// wrapped as ErrUnavailable so callers fail to human (MOD-05).
func (p HTTPProvider) Complete(ctx context.Context, req Request) (Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	wmsgs := make([]wireMessage, len(req.Messages))
	for i, m := range req.Messages {
		wmsgs[i] = wireMessage{Role: m.Role, Content: m.Content}
	}
	body, err := json.Marshal(wireRequest{
		Model:     req.Model,
		System:    req.System,
		Messages:  wmsgs,
		MaxTokens: maxTokens,
	})
	if err != nil {
		return Response{}, fmt.Errorf("%w: encode: %v", ErrUnavailable, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/messages", bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("%w: request: %v", ErrUnavailable, err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("%w: transport: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Response{}, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("%w: read body: %v", ErrUnavailable, err)
	}
	var wr wireResponse
	if err := json.Unmarshal(raw, &wr); err != nil {
		return Response{}, fmt.Errorf("%w: decode: %v", ErrUnavailable, err)
	}
	var text strings.Builder
	for _, b := range wr.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	return Response{Model: wr.Model, Text: text.String(), StopReason: wr.StopReason}, nil
}
