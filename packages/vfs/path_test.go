package vfs_test

import (
	"testing"

	"github.com/stretchr/testify/require"

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
				require.ErrorIs(t, err, test.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, got)
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
		require.NoError(t, err, "normalize %q", input)
		require.Equal(t, expected, got, "normalize %q", input)
	}
}
