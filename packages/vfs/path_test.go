package vfs_test

import (
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/packages/vfs"
)

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
		err   error
	}{
		{input: "one/two.txt", want: "one/two.txt"},
		{input: "one/./two.txt", want: "one/two.txt"},
		{input: "one//two.txt", want: "one/two.txt"},
		{input: "", err: vfs.ErrInvalidPath},
		{input: ".", err: vfs.ErrInvalidPath},
		{input: "/absolute", err: vfs.ErrInvalidPath},
		{input: "../escape", err: vfs.ErrInvalidPath},
		{input: `one\two`, err: vfs.ErrInvalidPath},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := vfs.NormalizePath(test.input)
			if test.err != nil {
				if !errors.Is(err, test.err) {
					t.Fatalf("expected %v, got %v", test.err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizePrefix(t *testing.T) {
	tests := map[string]string{
		"":        "",
		"school":  "school",
		"school/": "school/",
	}
	for input, expected := range tests {
		got, err := vfs.NormalizePrefix(input)
		if err != nil {
			t.Fatalf("normalize %q: %v", input, err)
		}
		if got != expected {
			t.Fatalf("normalize %q: got %q, expected %q", input, got, expected)
		}
	}
}
