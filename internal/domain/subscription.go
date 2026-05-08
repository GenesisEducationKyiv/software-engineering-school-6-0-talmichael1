package domain

import "time"

type Repository struct {
	ID          int64      `db:"id"`
	Owner       string     `db:"owner"`
	Name        string     `db:"name"`
	LastSeenTag string     `db:"last_seen_tag"`
	CheckedAt   *time.Time `db:"checked_at"`
	CreatedAt   time.Time  `db:"created_at"`
}

func (r Repository) FullName() string {
	return r.Owner + "/" + r.Name
}

type Subscription struct {
	ID               int64     `db:"id"`
	Email            string    `db:"email"`
	RepositoryID     int64     `db:"repository_id"`
	Confirmed        bool      `db:"confirmed"`
	ConfirmToken     string    `db:"confirm_token"`
	UnsubscribeToken string    `db:"unsubscribe_token"`
	CreatedAt        time.Time `db:"created_at"`
}

type SubscriptionView struct {
	Email       string `json:"email" db:"email"`
	Repo        string `json:"repo" db:"repo"`
	Confirmed   bool   `json:"confirmed" db:"confirmed"`
	LastSeenTag string `json:"last_seen_tag" db:"last_seen_tag"`
}

type Release struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	HTMLURL     string `json:"html_url"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
}

type NotificationJob struct {
	SubscriptionID int64  `json:"subscription_id"`
	Email          string `json:"email"`
	Repo           string `json:"repo"`
	Tag            string `json:"tag"`
	ReleaseName    string `json:"release_name"`
	ReleaseURL     string `json:"release_url"`
	UnsubToken     string `json:"unsub_token"`
	Attempt        int    `json:"attempt"`
}
