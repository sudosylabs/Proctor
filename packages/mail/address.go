package mail

import (
	"fmt"
	"net/mail"
	"strings"
	"unicode"
)

// Address is a mailbox with an optional human-readable display name. Address
// must contain only the bare addr-spec; put display text in Name.
type Address struct {
	Name    string
	Address string
}

func (a Address) String() string {
	return (&mail.Address{Name: a.Name, Address: a.Address}).String()
}

// Validate checks the portable SMTP address contract. Envelope addresses are
// ASCII-only because SMTPUTF8 negotiation is intentionally outside v1.
func (a Address) Validate() error {
	if unsafeHeaderValue(a.Name) {
		return fmt.Errorf("%w: display name contains a control character", ErrInvalidAddress)
	}
	if len(a.Name) > 256 {
		return fmt.Errorf("%w: display name exceeds 256 bytes", ErrInvalidAddress)
	}
	if a.Address == "" || strings.TrimSpace(a.Address) != a.Address {
		return fmt.Errorf("%w: mailbox must not be empty or padded", ErrInvalidAddress)
	}
	if len(a.Address) > 320 {
		return fmt.Errorf("%w: mailbox exceeds 320 bytes", ErrInvalidAddress)
	}
	if !isASCII(a.Address) || unsafeHeaderValue(a.Address) {
		return fmt.Errorf("%w: mailbox must be safe ASCII", ErrInvalidAddress)
	}
	if strings.ContainsAny(a.Address, "<>") {
		return fmt.Errorf("%w: mailbox must not include a display name", ErrInvalidAddress)
	}
	parsed, err := mail.ParseAddress(a.Address)
	if err != nil || parsed.Address != a.Address || !strings.Contains(a.Address, "@") {
		return fmt.Errorf("%w: %q", ErrInvalidAddress, a.Address)
	}
	return nil
}

func unsafeHeaderValue(value string) bool {
	for _, r := range value {
		if r == '\r' || r == '\n' || r == 0 || (unicode.IsControl(r) && r != '\t') {
			return true
		}
	}
	return false
}

func isASCII(value string) bool {
	for _, r := range value {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}
