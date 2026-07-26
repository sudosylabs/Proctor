package cache

import (
	"encoding/json"
	"errors"
)

// Codec converts typed values to a backend-neutral byte representation.
// Implementations must be safe for concurrent use.
type Codec[V any] interface {
	Encode(value V) ([]byte, error)
	Decode(data []byte) (V, error)
}

// CodecFuncs adapts functions to Codec.
type CodecFuncs[V any] struct {
	EncodeFunc func(value V) ([]byte, error)
	DecodeFunc func(data []byte) (V, error)
}

func (c CodecFuncs[V]) Encode(value V) ([]byte, error) {
	if c.EncodeFunc == nil {
		return nil, errors.New("cache: nil encode function")
	}
	return c.EncodeFunc(value)
}

func (c CodecFuncs[V]) Decode(data []byte) (V, error) {
	if c.DecodeFunc == nil {
		var zero V
		return zero, errors.New("cache: nil decode function")
	}
	return c.DecodeFunc(data)
}

// JSONCodec returns a codec backed by encoding/json.
func JSONCodec[V any]() Codec[V] {
	return CodecFuncs[V]{
		EncodeFunc: func(value V) ([]byte, error) {
			return json.Marshal(value)
		},
		DecodeFunc: func(data []byte) (V, error) {
			var value V
			err := json.Unmarshal(data, &value)
			return value, err
		},
	}
}

// StringCodec returns a lossless UTF-8 string codec. It does not reject
// arbitrary bytes received from a backend because Go strings may contain them.
func StringCodec() Codec[string] {
	return CodecFuncs[string]{
		EncodeFunc: func(value string) ([]byte, error) {
			return []byte(value), nil
		},
		DecodeFunc: func(data []byte) (string, error) {
			return string(data), nil
		},
	}
}

// BytesCodec returns a codec that copies on encode and decode so callers
// cannot mutate cached state through a retained slice.
func BytesCodec() Codec[[]byte] {
	return CodecFuncs[[]byte]{
		EncodeFunc: func(value []byte) ([]byte, error) {
			return append([]byte(nil), value...), nil
		},
		DecodeFunc: func(data []byte) ([]byte, error) {
			return append([]byte(nil), data...), nil
		},
	}
}
