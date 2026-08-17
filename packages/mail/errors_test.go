package mail_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/sudosylabs/proctor/packages/mail"
)

func TestClassifyReturnsPortableOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want mail.Outcome
	}{
		{name: "nil", err: nil, want: mail.OutcomeUnknown},
		{name: "unknown", err: errors.New("third-party failure"), want: mail.OutcomeUnknown},
		{name: "cancelled", err: context.Canceled, want: mail.OutcomeTemporary},
		{name: "deadline", err: context.DeadlineExceeded, want: mail.OutcomeTemporary},
		{name: "connection", err: mail.ErrConnection, want: mail.OutcomeTemporary},
		{name: "invalid message", err: mail.ErrInvalidMessage, want: mail.OutcomePermanent},
		{name: "invalid address", err: mail.ErrInvalidAddress, want: mail.OutcomePermanent},
		{name: "invalid header", err: mail.ErrInvalidHeader, want: mail.OutcomePermanent},
		{name: "message too large", err: mail.ErrMessageTooLarge, want: mail.OutcomePermanent},
		{name: "tls", err: mail.ErrTLS, want: mail.OutcomePermanent},
		{name: "authentication", err: mail.ErrAuthentication, want: mail.OutcomePermanent},
		{name: "rejected without transport detail", err: mail.ErrRejected, want: mail.OutcomePermanent},
		{name: "unsupported", err: mail.ErrUnsupported, want: mail.OutcomePermanent},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.err
			if err != nil {
				err = fmt.Errorf("outer: %w", err)
			}
			if got := mail.Classify(err); got != test.want {
				t.Fatalf("Classify() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestWithOutcomePreservesPortableErrorMatching(t *testing.T) {
	t.Parallel()

	for _, outcome := range []mail.Outcome{
		mail.OutcomeTemporary,
		mail.OutcomePermanent,
		mail.OutcomeAcceptanceUncertain,
	} {
		outcome := outcome
		t.Run(string(outcome), func(t *testing.T) {
			t.Parallel()
			cause := fmt.Errorf("%w: transport detail", mail.ErrRejected)
			err := mail.Error("commit", mail.WithOutcome(outcome, cause))
			if got := mail.Classify(err); got != outcome {
				t.Fatalf("Classify() = %q, want %q", got, outcome)
			}
			if !errors.Is(err, mail.ErrRejected) {
				t.Fatalf("errors.Is(error, ErrRejected) = false: %v", err)
			}
		})
	}
}

func TestWithOutcomeLeavesNilErrorNil(t *testing.T) {
	t.Parallel()

	if err := mail.WithOutcome(mail.OutcomeTemporary, nil); err != nil {
		t.Fatalf("WithOutcome(..., nil) = %v, want nil", err)
	}
}
