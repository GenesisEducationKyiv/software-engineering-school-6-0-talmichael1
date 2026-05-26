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

func (Templates) ReleaseNotification(to, repo, tag, releaseURL, unsubURL string) Message {
	subject := fmt.Sprintf("New release of %s: %s", repo, tag)
	text := fmt.Sprintf(
		"A new release has been published for %s.\n\n"+
			"Version: %s\n"+
			"Details: %s\n\n"+
			"To unsubscribe from these notifications:\n%s",
		repo, tag, releaseURL, unsubURL)
	html := fmt.Sprintf(`<h2>New Release: %s</h2>
<p>A new release has been published for <strong>%s</strong>.</p>
<p><strong>Version:</strong> %s</p>
<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#2ea44f;color:#fff;text-decoration:none;border-radius:6px">View Release</a></p>
<hr style="border:none;border-top:1px solid #e1e4e8;margin:24px 0">
<p style="color:#666;font-size:0.85em"><a href="%s">Unsubscribe</a> from %s release notifications.</p>`,
		tag, repo, tag, releaseURL, unsubURL, repo)
	return Message{To: to, Subject: subject, Text: text, HTML: html}
}
