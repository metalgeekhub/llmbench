package secrets

import (
	"errors"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	b, err := New("passphrase")
	if err != nil {
		t.Fatal(err)
	}
	ct, err := b.Encrypt("sk-secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ct, "sk-secret") || !strings.HasPrefix(ct, prefix) {
		t.Errorf("ciphertext leaks plaintext or lacks prefix: %q", ct)
	}
	ct2, _ := b.Encrypt("sk-secret")
	if ct == ct2 {
		t.Error("encryption must use a random nonce")
	}
	pt, err := b.Decrypt(ct)
	if err != nil || pt != "sk-secret" {
		t.Errorf("Decrypt = %q, %v", pt, err)
	}
	if e, _ := b.Encrypt(""); e != "" {
		t.Error("empty plaintext should encrypt to empty string")
	}
	if d, err := b.Decrypt(""); d != "" || err != nil {
		t.Error("empty ciphertext should decrypt to empty string")
	}

	other, _ := New("different")
	if _, err := other.Decrypt(ct); !errors.Is(err, ErrDecrypt) {
		t.Errorf("wrong key err = %v", err)
	}
	if _, err := b.Decrypt("garbage"); !errors.Is(err, ErrDecrypt) {
		t.Errorf("garbage err = %v", err)
	}
}

func TestLoadGeneratesAndReusesKey(t *testing.T) {
	dir := t.TempDir()
	b1, generated, err := Load("", dir)
	if err != nil || !generated {
		t.Fatalf("first Load: generated=%v err=%v", generated, err)
	}
	ct, _ := b1.Encrypt("value")

	b2, generated, err := Load("", dir)
	if err != nil || generated {
		t.Fatalf("second Load: generated=%v err=%v", generated, err)
	}
	if pt, err := b2.Decrypt(ct); err != nil || pt != "value" {
		t.Errorf("reloaded key cannot decrypt: %q %v", pt, err)
	}

	b3, generated, err := Load("explicit", dir)
	if err != nil || generated {
		t.Fatal(err)
	}
	if _, err := b3.Decrypt(ct); err == nil {
		t.Error("explicit passphrase should take precedence over key file")
	}
}
