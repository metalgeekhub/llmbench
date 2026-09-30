package tokenizer

import "testing"

func TestCount(t *testing.T) {
	if Count("") != 0 {
		t.Error("empty text should be 0 tokens")
	}
	// "hello world" is 2 tokens in cl100k_base.
	if got := Count("hello world"); got != 2 {
		t.Errorf("Count(hello world) = %d, want 2", got)
	}
	if got := CountMessages([]string{"hello world", "hi"}); got != 2+1+2*perMessageOverhead {
		t.Errorf("CountMessages = %d", got)
	}
}
