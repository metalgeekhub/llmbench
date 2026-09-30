// Package tokenizer counts tokens for requests whose server does not report
// usage. Counts are approximations and must be flagged as estimated.
package tokenizer

import (
	"log/slog"
	"strings"
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

// corpus is neutral filler text for synthetic prompts.
const corpus = `The harbor town woke slowly under a thin layer of fog. Fishermen checked their nets while the bakery on the corner pulled the first loaves from the oven. A cyclist rang her bell twice as she passed the old clock tower, which had been running four minutes late for as long as anyone could remember. Down by the water, a group of students measured the tide with a wooden pole and wrote the numbers in a notebook. The librarian unlocked the heavy front door, switched on the lamps, and began sorting the returned books into neat piles by subject. Outside the town hall, workers were setting up a small stage for the evening concert, testing cables and arguing gently about where the speakers should go. A delivery truck stopped in front of the market, and two men carried crates of apples, pears, and late summer plums inside. In the park, an elderly man fed the pigeons from a paper bag while telling a stranger about the storms of his childhood. `

// Synthetic returns filler text of approximately n tokens (exact for the
// local tokenizer; servers with other tokenizers will count slightly
// differently).
func Synthetic(n int) string {
	if n <= 0 {
		return ""
	}
	e := encoding()
	if e == nil {
		return repeatTo(corpus, n*4)
	}
	per := len(e.Encode(corpus, nil, nil))
	text := strings.Repeat(corpus, n/per+1)
	tokens := e.Encode(text, nil, nil)
	if len(tokens) > n {
		tokens = tokens[:n]
	}
	return e.Decode(tokens)
}

// DefaultInstruction ends synthetic prompts when no instruction is given.
const DefaultInstruction = "Continue the story above in as much detail as you can."

// SyntheticPrompt returns filler text followed by instruction, about n
// tokens in total.
func SyntheticPrompt(n int, instruction string) string {
	if instruction == "" {
		instruction = DefaultInstruction
	}
	filler := max(n-Count(instruction)-2, 1)
	return Synthetic(filler) + "\n\n" + instruction
}

func repeatTo(s string, chars int) string {
	var b strings.Builder
	for b.Len() < chars {
		b.WriteString(s)
	}
	return b.String()[:chars]
}

// CountMessages estimates prompt tokens for a list of chat message contents.
func CountMessages(contents []string) int {
	n := 0
	for _, c := range contents {
		n += Count(c) + perMessageOverhead
	}
	return n
}
