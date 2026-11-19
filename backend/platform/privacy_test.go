package platform

import (
	"strings"
	"testing"
)

func TestSealRoundTripAndTamper(t *testing.T) {
	t.Setenv("DOCUMENT_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err := LoadEncryptionKey(); err != nil {
		t.Fatal(err)
	}

	a, _ := Seal([]byte("private letter"))
	b, _ := Seal([]byte("private letter"))
	if a == b || strings.Contains(a, "private") {
		t.Fatalf("not encrypted properly: %q %q", a, b)
	}
	if got, err := Open(a); err != nil || string(got) != "private letter" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	damaged := a[:len(a)-6] + "AAAAA="
	if _, err := Open(damaged); err == nil {
		t.Fatal("damaged data must not open")
	}
	if got, err := Open("written before encryption"); err != nil || string(got) != "written before encryption" {
		t.Fatalf("old row: %q, %v", got, err)
	}

	js, _ := SealJSON([]byte(`[["a"]]`))
	if got, err := OpenJSON([]byte(js)); err != nil || string(got) != `[["a"]]` {
		t.Fatalf("OpenJSON = %q, %v", got, err)
	}
	if got, err := OpenJSON([]byte(`[["old"]]`)); err != nil || string(got) != `[["old"]]` {
		t.Fatalf("old jsonb row: %q, %v", got, err)
	}
}

func TestLoadEncryptionKeyRejectsBadKeys(t *testing.T) {
	for _, key := range []string{"", "short", "not base64 !!"} {
		t.Setenv("DOCUMENT_ENCRYPTION_KEY", key)
		if LoadEncryptionKey() == nil {
			t.Errorf("key %q was accepted", key)
		}
	}
}
