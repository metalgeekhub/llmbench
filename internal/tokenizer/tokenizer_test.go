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

func TestSynthetic(t *testing.T) {
	if Synthetic(0) != "" {
		t.Error("Synthetic(0) should be empty")
	}
	for _, n := range []int{1, 50, 1000, 8000} {
		got := Count(Synthetic(n))
		// Re-encoding the cut point may merge or split one token.
		if got < n-1 || got > n+1 {
			t.Errorf("Synthetic(%d) has %d tokens", n, got)
		}
	}
}
