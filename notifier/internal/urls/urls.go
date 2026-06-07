package urls

import "fmt"

type Builder struct {
	BaseURL string
}

func (b Builder) Unsubscribe(token string) string {
	return fmt.Sprintf("%s/api/unsubscribe/%s", b.BaseURL, token)
}
