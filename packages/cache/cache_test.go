package cache_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/cache"
)

func TestNormalizedTTL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   time.Duration
		want time.Duration
		err  error
	}{
		{name: "persistent", in: 0, want: 0},
		{name: "exact", in: 2 * time.Millisecond, want: 2 * time.Millisecond},
		{name: "round up", in: time.Millisecond + 1, want: 2 * time.Millisecond},
		{name: "negative", in: -1, err: cache.ErrInvalidTTL},
		{name: "rounding overflow", in: time.Duration(1<<63 - 2), err: cache.ErrInvalidTTL},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := cache.NormalizedTTL(test.in)
			require.ErrorIs(t, err, test.err, "NormalizedTTL() error")
			require.Equal(t, test.want, got, "NormalizedTTL()")
		})
	}
}

func TestValidateKey(t *testing.T) {
	t.Parallel()

	require.NoError(t, cache.ValidateKey("school/42:session"))
	for _, key := range []string{"", "line\nbreak", string([]byte{0xff})} {
		require.ErrorIs(t, cache.ValidateKey(key), cache.ErrInvalidKey, "key")
	}
}

func TestValidateNamespace(t *testing.T) {
	t.Parallel()

	require.NoError(t, cache.ValidateNamespace("school-api"))
	require.ErrorIs(t, cache.ValidateNamespace(""), cache.ErrInvalidNamespace, "empty namespace error")
}

func TestBytesCodecCopies(t *testing.T) {
	t.Parallel()

	codec := cache.BytesCodec()
	source := []byte("value")
	encoded, err := codec.Encode(source)
	require.NoError(t, err)
	source[0] = 'X'
	require.Equal(t, "value", string(encoded), "encoded data aliases source")

	decoded, err := codec.Decode(encoded)
	require.NoError(t, err)
	encoded[0] = 'Y'
	require.Equal(t, "value", string(decoded), "decoded data aliases encoded input")
}
