package saga

import (
	"context"
	"errors"
	"testing"
)

func TestRunAllStepsSucceed(t *testing.T) {
	var order []string
	s := New(
		Step{Name: "a", Action: func(context.Context) error { order = append(order, "a"); return nil }},
		Step{Name: "b", Action: func(context.Context) error { order = append(order, "b"); return nil }},
	)
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Fatalf("steps ran out of order: %v", order)
	}
}

func TestRunCompensatesCompletedStepsInReverse(t *testing.T) {
	var compensated []string
	boom := errors.New("boom")
	s := New(
		Step{
			Name:       "create",
			Action:     func(context.Context) error { return nil },
			Compensate: func(context.Context) error { compensated = append(compensated, "create"); return nil },
		},
		Step{
			Name:       "attach",
			Action:     func(context.Context) error { return nil },
			Compensate: func(context.Context) error { compensated = append(compensated, "attach"); return nil },
		},
		Step{
			Name:   "send",
			Action: func(context.Context) error { return boom },
		},
	)
	err := s.Run(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if len(compensated) != 2 || compensated[0] != "attach" || compensated[1] != "create" {
		t.Fatalf("compensation order = %v, want [attach create]", compensated)
	}
}

func TestRunDoesNotCompensateFailedStep(t *testing.T) {
	var compensated []string
	s := New(
		Step{
			Name:       "create",
			Action:     func(context.Context) error { return errors.New("fail") },
			Compensate: func(context.Context) error { compensated = append(compensated, "create"); return nil },
		},
	)
	_ = s.Run(context.Background())
	if len(compensated) != 0 {
		t.Fatalf("failed step must not be compensated, got %v", compensated)
	}
}
