package cache_test

import (
	"errors"
	"testing"
	"time"

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
			if !errors.Is(err, test.err) {
				t.Fatalf("NormalizedTTL() error = %v, want %v", err, test.err)
			}
			if got != test.want {
				t.Fatalf("NormalizedTTL() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestValidateKey(t *testing.T) {
	t.Parallel()

	if err := cache.ValidateKey("school/42:session"); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	for _, key := range []string{"", "line\nbreak", string([]byte{0xff})} {
		if !errors.Is(cache.ValidateKey(key), cache.ErrInvalidKey) {
			t.Fatalf("key %q should be invalid", key)
		}
	}
}

func TestValidateNamespace(t *testing.T) {
	t.Parallel()

	if err := cache.ValidateNamespace("school-api"); err != nil {
		t.Fatalf("valid namespace rejected: %v", err)
	}
	if err := cache.ValidateNamespace(""); !errors.Is(err, cache.ErrInvalidNamespace) {
		t.Fatalf("empty namespace error = %v, want ErrInvalidNamespace", err)
	}
}

func TestBytesCodecCopies(t *testing.T) {
	t.Parallel()

	codec := cache.BytesCodec()
	source := []byte("value")
	encoded, err := codec.Encode(source)
	if err != nil {
		t.Fatal(err)
	}
	source[0] = 'X'
	if string(encoded) != "value" {
		t.Fatal("encoded data aliases source")
	}

	decoded, err := codec.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	encoded[0] = 'Y'
	if string(decoded) != "value" {
		t.Fatal("decoded data aliases encoded input")
	}
}
