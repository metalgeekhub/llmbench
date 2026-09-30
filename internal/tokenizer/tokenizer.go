// Package tokenizer counts tokens for requests whose server does not report
// usage. Counts are approximations and must be flagged as estimated.
package tokenizer

import (
	"log/slog"
	"sync"

	"github.com/pkoukk/tiktoken-go"
	tiktoken_loader "github.com/pkoukk/tiktoken-go-loader"
)

// encodingName is used for all models; exact per-model tokenizers are a
// later enhancement (Hugging Face tokenizer files).
const encodingName = "cl100k_base"

// perMessageOverhead approximates chat template tokens added per message.
const perMessageOverhead = 4

var (
	once sync.Once
	enc  *tiktoken.Tiktoken
)

func encoding() *tiktoken.Tiktoken {
	once.Do(func() {
		// Use the embedded BPE files; never download at runtime.
		tiktoken.SetBpeLoader(tiktoken_loader.NewOfflineLoader())
		e, err := tiktoken.GetEncoding(encodingName)
		if err != nil {
			slog.Warn("tokenizer unavailable, falling back to character heuristic", "err", err)
			return
		}
		enc = e
	})
	return enc
}

// Count returns the number of tokens in text.
func Count(text string) int {
	if text == "" {
		return 0
	}
	if e := encoding(); e != nil {
		return len(e.Encode(text, nil, nil))
	}
	// ~4 characters per token for English text.
	return (len([]rune(text)) + 3) / 4
}

// CountMessages estimates prompt tokens for a list of chat message contents.
func CountMessages(contents []string) int {
	n := 0
	for _, c := range contents {
		n += Count(c) + perMessageOverhead
	}
	return n
}
