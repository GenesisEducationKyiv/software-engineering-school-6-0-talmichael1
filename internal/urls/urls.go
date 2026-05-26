package urls

import "fmt"

type Builder struct {
	BaseURL string
}

func (b Builder) Confirm(token string) string {
	return fmt.Sprintf("%s/api/confirm/%s", b.BaseURL, token)
}

func (b Builder) Unsubscribe(token string) string {
	return fmt.Sprintf("%s/api/unsubscribe/%s", b.BaseURL, token)
}
