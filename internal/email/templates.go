package email

import "fmt"

type Templates struct{}

func (Templates) Confirmation(to, repo, confirmURL string) Message {
	subject := fmt.Sprintf("Confirm your subscription to %s releases", repo)
	text := fmt.Sprintf(
		"You have requested to receive release notifications for %s.\n\n"+
			"Please confirm your subscription by visiting the following link:\n%s\n\n"+
			"If you did not request this, you can safely ignore this email.",
		repo, confirmURL)
	html := fmt.Sprintf(`<h2>Confirm your subscription</h2>
<p>You have requested to receive release notifications for <strong>%s</strong>. The link expires in an hour.</p>
<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#2ea44f;color:#fff;text-decoration:none;border-radius:6px">Confirm Subscription</a></p>
<p style="color:#666;font-size:0.9em">If you did not request this, you can safely ignore this email.</p>`,
		repo, confirmURL)
	return Message{To: to, Subject: subject, Text: text, HTML: html}
}
