package domain

// NotificationJob is the wire contract shared with the producer (the API +
// scanner monolith) over the Redis queue. Its JSON shape must stay in sync
// with the producer's copy — see ADR-0005.
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
