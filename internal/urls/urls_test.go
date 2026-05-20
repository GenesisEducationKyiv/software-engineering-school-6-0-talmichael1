package urls

import "testing"

func TestBuilder(t *testing.T) {
	b := Builder{BaseURL: "https://example.com"}
	if got := b.Confirm("abc"); got != "https://example.com/api/confirm/abc" {
		t.Errorf("Confirm: got %q", got)
	}
	if got := b.Unsubscribe("xyz"); got != "https://example.com/api/unsubscribe/xyz" {
		t.Errorf("Unsubscribe: got %q", got)
	}
}
