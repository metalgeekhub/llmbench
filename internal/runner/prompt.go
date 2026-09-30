package runner

import (
	"fmt"
	"math/rand/v2"

	"github.com/metalgeekhub/llmbench/internal/providers"
	"github.com/metalgeekhub/llmbench/internal/store"
	"github.com/metalgeekhub/llmbench/internal/tokenizer"
)

// resolvedTarget is a target with its source/model filled in and, for
// profiles, the profile's parameters.
type resolvedTarget struct {
	SourceID    string
	SourceName  string
	Model       string
	ProfileID   string
	ProfileName string
	Params      store.ChatParams // zero for plain targets
}

// promptBuilder produces the request for each virtual-user iteration of
// one target.
type promptBuilder struct {
	system      string
	user        string
	cacheBust   bool
	maxTokens   *int
	temperature *float64
	extraBody   map[string]any
	thinking    *providers.Thinking
	params      map[string]any // recorded on each request
}

// userPrompt builds the user message for a cell. contextTokens > 0 asks for
// a synthetic prompt of that length; otherwise the run's prompt is used.
func userPrompt(c *Config, contextTokens int) string {
	if contextTokens == 0 {
		if c.Prompt.Mode != PromptSynthetic {
			return c.Prompt.Text
		}
		contextTokens = c.Prompt.InputTokens
	}
	// Fill the requested length including the instruction.
	return tokenizer.SyntheticPrompt(contextTokens, "")
}

// newPromptBuilder prepares requests for one target. thinkingLevel, when
// set (thinking sweeps), overrides the target's own thinking setting; the
// style comes from the target's profile if it has one, else from the run.
func newPromptBuilder(c *Config, user string, t resolvedTarget, thinkingLevel string) (*promptBuilder, error) {
	p := t.Params
	if thinkingLevel != "" {
		p.Thinking = thinkingLevel
		if p.ThinkingStyle == "" {
			p.ThinkingStyle = c.ThinkingStyle
		}
	}
	thinking, err := p.ThinkingControl()
	if err != nil {
		return nil, err
	}
	b := &promptBuilder{
		system:      p.SystemPrompt,
		user:        user,
		cacheBust:   c.CacheBust,
		maxTokens:   p.MaxTokens,
		temperature: p.Temperature,
		thinking:    thinking,
	}
	// The run's settings take precedence over the profile's.
	if c.Prompt.SystemPrompt != "" {
		b.system = c.Prompt.SystemPrompt
	}
	if c.MaxTokens != nil {
		b.maxTokens = c.MaxTokens
	}
	if c.Temperature != nil {
		b.temperature = c.Temperature
	}
	b.extraBody = providers.MergeJSON(providers.MergeJSON(nil, p.ExtraBody), c.ExtraBody)
	if c.IgnoreEOS {
		// vLLM/SGLang: generate exactly max_tokens.
		b.extraBody["ignore_eos"] = true
	}

	b.params = map[string]any{}
	if b.maxTokens != nil {
		b.params["max_tokens"] = *b.maxTokens
	}
	if b.temperature != nil {
		b.params["temperature"] = *b.temperature
	}
	if thinking != nil {
		b.params["thinking"] = thinking.Level
		b.params["thinking_style"] = thinking.Style
	}
	if t.ProfileName != "" {
		b.params["profile"] = t.ProfileName
	}
	return b, nil
}

func (b *promptBuilder) request(model string) providers.ChatRequest {
	var msgs []providers.Message
	if b.system != "" {
		msgs = append(msgs, providers.Message{Role: "system", Content: b.system})
	}
	msgs = append(msgs, providers.Message{Role: "user", Content: b.user})
	if b.cacheBust {
		// A unique marker at the very start defeats prefix caching for the
		// whole prompt, including a shared system prompt.
		msgs[0].Content = fmt.Sprintf("[%016x] %s", rand.Uint64(), msgs[0].Content)
	}
	return providers.ChatRequest{
		Model:       model,
		Messages:    msgs,
		Temperature: b.temperature,
		MaxTokens:   b.maxTokens,
		ExtraBody:   b.extraBody,
		Thinking:    b.thinking,
	}
}

// requestParams returns the parameters recorded on a request of this cell.
func (b *promptBuilder) requestParams(concurrency int) map[string]any {
	out := make(map[string]any, len(b.params)+1)
	for k, v := range b.params {
		out[k] = v
	}
	out["concurrency"] = concurrency
	return out
}
