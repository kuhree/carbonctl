package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadTelegramChatIDAcceptsOneCanonicalPositiveNumericID(t *testing.T) {
	path := writeCredential(t, "123456789\n")
	got, err := readTelegramChatID(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "123456789" {
		t.Fatalf("chat ID = %q, want canonical value", got)
	}
}

func TestReadTelegramChatIDRejectsNonCanonicalOrMultipleValues(t *testing.T) {
	tests := map[string]string{
		"empty":             "",
		"zero":              "0",
		"leading zero":      "0123",
		"positive sign":     "+123",
		"negative sign":     "-123",
		"surrounding space": " 123 ",
		"internal space":    "12 3",
		"username":          "@owner",
		"comma list":        "123,456",
		"multiple lines":    "123\n456",
		"int64 overflow":    "9223372036854775808",
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := readTelegramChatID(writeCredential(t, payload))
			if !errors.Is(err, errInvalidTelegramChatID) {
				t.Fatalf("error = %v, want invalid Telegram chat ID", err)
			}
		})
	}
}

func writeCredential(t *testing.T, payload string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
