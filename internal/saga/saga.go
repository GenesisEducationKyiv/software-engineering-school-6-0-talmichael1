// Package saga runs a synchronous orchestrated saga: an ordered list of steps,
// each with an optional compensation. If a step fails, the compensations of the
// already-completed steps run in reverse order (ADR-0007).
package saga

import (
	"context"
	"log/slog"
)

type Step struct {
	Name       string
	Action     func(ctx context.Context) error
	Compensate func(ctx context.Context) error
}

type Saga struct {
	steps []Step
}

func New(steps ...Step) *Saga {
	return &Saga{steps: steps}
}

// Run executes each step in order. On the first failure it compensates the
// completed steps in reverse and returns that step's error. Compensation is
// best-effort: a failing compensation is logged, not returned, so the caller
// still sees the original cause.
func (s *Saga) Run(ctx context.Context) error {
	var done []Step
	for _, step := range s.steps {
		if err := step.Action(ctx); err != nil {
			s.compensate(ctx, done)
			return err
		}
		done = append(done, step)
	}
	return nil
}

func (s *Saga) compensate(ctx context.Context, done []Step) {
	for i := len(done) - 1; i >= 0; i-- {
		if done[i].Compensate == nil {
			continue
		}
		if err := done[i].Compensate(ctx); err != nil {
			slog.ErrorContext(ctx, "saga: compensation failed", "step", done[i].Name, "error", err)
		}
	}
}
