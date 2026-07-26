package mail_test

import (
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/packages/mail"
)

func TestAddressValidate(t *testing.T) {
	t.Parallel()

	valid := []mail.Address{
		{Address: "student@example.test"},
		{Name: "École Proctor", Address: "noreply@example.test"},
	}
	for _, address := range valid {
		if err := address.Validate(); err != nil {
			t.Fatalf("valid address %q rejected: %v", address.Address, err)
		}
	}

	invalid := []mail.Address{
		{},
		{Address: "Student <student@example.test>"},
		{Address: "student@example.test\r\nBcc: attacker@example.test"},
		{Address: "élève@example.test"},
		{Name: "unsafe\nname", Address: "student@example.test"},
	}
	for _, address := range invalid {
		if err := address.Validate(); !errors.Is(err, mail.ErrInvalidAddress) {
			t.Fatalf("address %#v error = %v, want ErrInvalidAddress", address, err)
		}
	}
}
