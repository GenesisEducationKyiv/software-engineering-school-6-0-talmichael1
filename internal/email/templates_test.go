package email

import (
	"strings"
	"testing"
)

func TestTemplates_Confirmation(t *testing.T) {
	msg := Templates{}.Confirmation("user@example.com", "golang/go", "https://x/c/abc")
	if msg.To != "user@example.com" {
		t.Errorf("To = %q", msg.To)
	}
	if !strings.Contains(msg.Subject, "golang/go") {
		t.Errorf("Subject missing repo: %q", msg.Subject)
	}
	if !strings.Contains(msg.Text, "https://x/c/abc") || !strings.Contains(msg.HTML, "https://x/c/abc") {
		t.Errorf("confirm URL missing from body")
	}
}

func TestTemplates_ReleaseNotification(t *testing.T) {
	msg := Templates{}.ReleaseNotification(
		"user@example.com", "golang/go", "v1.22",
		"https://x/r/v1.22", "https://x/u/tok",
	)
	if msg.To != "user@example.com" {
		t.Errorf("To = %q", msg.To)
	}
	if !strings.Contains(msg.Subject, "v1.22") {
		t.Errorf("Subject missing tag: %q", msg.Subject)
	}
	if !strings.Contains(msg.Text, "https://x/u/tok") || !strings.Contains(msg.HTML, "https://x/u/tok") {
		t.Errorf("unsub URL missing from body")
	}
}
