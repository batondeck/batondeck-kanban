package service

import (
	"bytes"
	"encoding/json"
	"reflect"
)

// Optional is a request field that is absent or a value, for a field whose zero value means
// something (conductor's z.T().optional()): Set says it was sent, V is what was sent. JSON null is
// refused, as Zod refuses it. Its schema is T's, registered per instantiation in internal/service/dtoschema.
type Optional[T any] struct {
	Set bool
	V   T
}

// Nullable is a request field that is absent, null or a value (conductor's .nullable().optional()):
// Set says it was sent, Null that it was sent as null, V the value otherwise. Its schema is T's or
// null, registered per instantiation in internal/service/dtoschema.
type Nullable[T any] struct {
	Set  bool
	Null bool
	V    T
}

// Some is an Optional that was sent as v.
func Some[T any](v T) Optional[T] { return Optional[T]{Set: true, V: v} }

// Value is a Nullable that was sent as v.
func Value[T any](v T) Nullable[T] { return Nullable[T]{Set: true, V: v} }

// Null is a Nullable that was sent as null.
func Null[T any]() Nullable[T] { return Nullable[T]{Set: true, Null: true} }

// IsZero is true when the field was absent, which is what omitzero leaves out.
func (o Optional[T]) IsZero() bool { return !o.Set }

func (o Optional[T]) MarshalJSON() ([]byte, error) { return json.Marshal(o.V) }

func (o *Optional[T]) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		// An UnmarshalTypeError, so encoding/json names the field and a caller maps it as any type error.
		return &json.UnmarshalTypeError{Value: "null", Type: reflect.TypeFor[T]()}
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*o = Optional[T]{Set: true, V: v}
	return nil
}

// IsZero is true when the field was absent, which is what omitzero leaves out.
func (n Nullable[T]) IsZero() bool { return !n.Set }

func (n Nullable[T]) MarshalJSON() ([]byte, error) {
	if n.Null {
		return []byte("null"), nil
	}
	return json.Marshal(n.V)
}

func (n *Nullable[T]) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		*n = Nullable[T]{Set: true, Null: true}
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*n = Nullable[T]{Set: true, V: v}
	return nil
}

// Ptr is the value as a pointer, nil when absent or null.
func (n Nullable[T]) Ptr() *T {
	if !n.Set || n.Null {
		return nil
	}
	v := n.V
	return &v
}
