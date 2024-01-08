package utils

import (
	"crypto/sha256"
	"fmt"
	"reflect"
)

func PointerTo[V any](v V) *V {
	return &v
}

func NotNil(v any) bool {
	if v == nil {
		return false
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer {
		return true
	}

	return !rv.IsNil()
}

func ShaSum(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return fmt.Sprintf("%x", h.Sum(nil))
}
