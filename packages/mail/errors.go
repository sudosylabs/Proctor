package mail

import (
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
