package email

import (
	"context"
	"fmt"

	"github.com/mailgun/mailgun-go/v4"
)

type MailgunSender struct {
	mg   *mailgun.MailgunImpl
	from string
}

func NewMailgunSender(domain, apiKey, from, apiBase string) *MailgunSender {
	mg := mailgun.NewMailgun(domain, apiKey)
	if apiBase != "" {
		mg.SetAPIBase(apiBase)
	}
	return &MailgunSender{mg: mg, from: from}
}

func (s *MailgunSender) Send(ctx context.Context, msg Message) error {
	m := mailgun.NewMessage(s.from, msg.Subject, msg.Text, msg.To)
	m.SetHTML(msg.HTML)

	_, _, err := s.mg.Send(ctx, m)
	if err != nil {
		return fmt.Errorf("sending email to %s: %w", msg.To, err)
	}
	return nil
}
