package domain

import "time"

type SagaState string

const (
	SagaPending     SagaState = "pending"
	SagaCompleted   SagaState = "completed"
	SagaCompensated SagaState = "compensated"
	SagaFailed      SagaState = "failed"
)

type SubscriptionSaga struct {
	ID             string    `db:"id"`
	State          SagaState `db:"state"`
	Email          string    `db:"email"`
	RepoFullName   string    `db:"repo_full_name"`
	ConfirmToken   string    `db:"confirm_token"`
	SubscriptionID *int64    `db:"subscription_id"`
	Attempts       int       `db:"attempts"`
	LastError      *string   `db:"last_error"`
	CreatedAt      time.Time `db:"created_at"`
	UpdatedAt      time.Time `db:"updated_at"`
}
