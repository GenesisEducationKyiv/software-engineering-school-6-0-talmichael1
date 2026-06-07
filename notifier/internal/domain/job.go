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

	// W3C trace context, set by the producer so this service can continue the
	// scanner's trace across the queue boundary (see ADR-0005).
	Traceparent string `json:"traceparent,omitempty"`
	Tracestate  string `json:"tracestate,omitempty"`
}
