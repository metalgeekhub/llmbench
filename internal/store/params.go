package store

import (
	"errors"

	"github.com/metalgeekhub/llmbench/internal/providers"
)

// Validate checks parameter ranges and the thinking control.
func (p ChatParams) Validate() error {
	if p.Temperature != nil && (*p.Temperature < 0 || *p.Temperature > 2) {
		return errors.New("temperature must be between 0 and 2")
	}
	if p.MaxTokens != nil && *p.MaxTokens < 1 {
		return errors.New("max tokens must be at least 1")
	}
	_, err := p.ThinkingControl()
	return err
}

// ThinkingControl returns the adapter-level thinking setting, or nil for
// "server default".
func (p ChatParams) ThinkingControl() (*providers.Thinking, error) {
	return providers.ParseThinking(p.Thinking, p.ThinkingStyle)
}

// Summary returns the parameters worth recording on a request.
func (p ChatParams) Summary() map[string]any {
	out := map[string]any{}
	if p.Temperature != nil {
		out["temperature"] = *p.Temperature
	}
	if p.MaxTokens != nil {
		out["max_tokens"] = *p.MaxTokens
	}
	if p.SystemPrompt != "" {
		out["system_prompt"] = p.SystemPrompt
	}
	if len(p.ExtraBody) > 0 {
		out["extra_body"] = p.ExtraBody
	}
	if p.Thinking != "" {
		out["thinking"] = p.Thinking
		out["thinking_style"] = p.ThinkingStyle
	}
	return out
}
