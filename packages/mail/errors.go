package mail

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrInvalidMessage indicates that a message is incomplete or internally
	// inconsistent.
	ErrInvalidMessage = errors.New("mail: invalid message")
	// ErrInvalidAddress indicates that a mailbox is malformed or unsupported.
	ErrInvalidAddress = errors.New("mail: invalid address")
	// ErrInvalidHeader indicates that a header name or value is unsafe.
	ErrInvalidHeader = errors.New("mail: invalid header")
	// ErrMessageTooLarge indicates that the encoded message exceeds the
	// configured transport limit.
	ErrMessageTooLarge = errors.New("mail: message too large")
	// ErrConnection indicates that an SMTP connection could not be established
	// or was interrupted.
	ErrConnection = errors.New("mail: connection failed")
	// ErrTLS indicates that the requested TLS policy could not be satisfied.
	ErrTLS = errors.New("mail: tls failed")
	// ErrAuthentication indicates that SMTP authentication failed or no
	// compatible mechanism was offered.
	ErrAuthentication = errors.New("mail: authentication failed")
	// ErrRejected indicates that an SMTP server rejected the envelope or data.
	ErrRejected = errors.New("mail: rejected")
	// ErrUnsupported indicates that a sender cannot provide a requested
	// feature.
	ErrUnsupported = errors.New("mail: unsupported")
)

// Outcome is the portable retry significance of a failed delivery. It lets an
// application make policy decisions without interpreting transport-specific
// status codes or network errors.
type Outcome string

const (
	// OutcomeUnknown means the error did not come from a conforming sender and
	// has no portable delivery classification.
	OutcomeUnknown Outcome = "unknown"
	// OutcomeTemporary means the transport definitely did not accept the
	// message and a later attempt may succeed.
	OutcomeTemporary Outcome = "temporary"
	// OutcomePermanent means retrying the same message without an external
	// change cannot succeed.
	OutcomePermanent Outcome = "permanent"
	// OutcomeAcceptanceUncertain means the connection failed after transmission
	// began, so the remote transport may already have accepted the message.
	OutcomeAcceptanceUncertain Outcome = "acceptance_uncertain"
)

type outcomeError struct {
	outcome Outcome
	err     error
}

func (e *outcomeError) Error() string { return e.err.Error() }

func (e *outcomeError) Unwrap() error { return e.err }

// WithOutcome annotates err with a portable delivery outcome while preserving
// errors.Is and errors.As behavior for err. A nil error remains nil. Unknown or
// unrecognized outcomes leave err unannotated.
func WithOutcome(outcome Outcome, err error) error {
	if err == nil {
		return nil
	}
	switch outcome {
	case OutcomeTemporary, OutcomePermanent, OutcomeAcceptanceUncertain:
		return &outcomeError{outcome: outcome, err: err}
	default:
		return err
	}
}

// Classify returns the portable outcome of err. Existing portable mail and
// context errors receive conservative default classifications so callers and
// sender implementations remain compatible; an explicit transport outcome
// takes precedence when protocol state provides more information.
func Classify(err error) Outcome {
	if err == nil {
		return OutcomeUnknown
	}
	var classified *outcomeError
	if errors.As(err, &classified) {
		return classified.outcome
	}

	switch {
	case errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, ErrConnection):
		return OutcomeTemporary
	case errors.Is(err, ErrInvalidMessage),
		errors.Is(err, ErrInvalidAddress),
		errors.Is(err, ErrInvalidHeader),
		errors.Is(err, ErrMessageTooLarge),
		errors.Is(err, ErrTLS),
		errors.Is(err, ErrAuthentication),
		errors.Is(err, ErrRejected),
		errors.Is(err, ErrUnsupported):
		return OutcomePermanent
	default:
		return OutcomeUnknown
	}
}

// OpError adds operation context while preserving errors.Is and errors.As
// behavior for the underlying error.
type OpError struct {
	Op  string
	Err error
}

func (e *OpError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("mail %s: %v", e.Op, e.Err)
}

func (e *OpError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Error annotates err with a mail operation. A nil error remains nil.
func Error(op string, err error) error {
	if err == nil {
		return nil
	}
	return &OpError{Op: op, Err: err}
}
