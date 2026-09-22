package channel

import (
	"errors"
	"fmt"
)

// FieldError is a validation failure that names the parameter it is about.
//
// The message is written for whoever is reading the configuration — a file, a
// log line, an API response. The field is for whatever is *rendering* it: the
// admin form puts the message under the input the field names, which is the
// difference between a paragraph at the bottom of the page reading
//
//	parameter "host" is required
//
// and a line under the box labelled "SMTP server" saying the same thing.
//
// The message is not reworded for the form. It is quoted in the API response
// and written to logs, and a version that reads well under an input would read
// badly everywhere else. The two audiences want different sentences, so this
// carries both.
type FieldError struct {
	// Field is the parameter's schema name — the same string that appears in
	// ParamSpec.Name and in the form's data-field attribute.
	Field   string
	Message string

	// Err is the underlying failure, where there was one. Kept so that wrapping
	// a FieldError does not quietly turn a typed cause into a string.
	Err error
}

func (e *FieldError) Error() string { return e.Message }

// Unwrap exposes the cause, so errors.Is and errors.As still reach it.
func (e *FieldError) Unwrap() error { return e.Err }

// fieldErr builds a FieldError about a named parameter.
func fieldErr(name, format string, args ...any) error {
	return &FieldError{Field: name, Message: fmt.Sprintf(format, args...)}
}

// wrapFieldErr is fieldErr for a failure that has a cause worth keeping.
func wrapFieldErr(name string, cause error, format string, args ...any) error {
	return &FieldError{Field: name, Message: fmt.Sprintf(format, args...), Err: cause}
}

// SplitErrors separates a validation failure into the parts that name a
// parameter and the parts that do not.
//
// The second list is not an afterthought. A configuration can be wrong in ways
// that belong to no single field — an unknown key, or a pair of parameters that
// are only wrong together, like a password with no username — and those have
// nowhere better to go than the top of the form. Dropping them because they do
// not fit the per-field shape would lose the reason the save was refused.
//
// The tree is walked rather than searched with errors.As, because As stops at
// the first match and a configuration with three bad fields should say so about
// all three.
func SplitErrors(err error) (fields []*FieldError, other []string) {
	var walk func(error)

	walk = func(e error) {
		if e == nil {
			return
		}

		// errors.Join and anything else with a slice of causes.
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			for _, child := range joined.Unwrap() {
				walk(child)
			}
			return
		}

		if fe, ok := e.(*FieldError); ok {
			fields = append(fields, fe)
			return
		}

		if next := errors.Unwrap(e); next != nil {
			walk(next)
			return
		}
		other = append(other, e.Error())
	}

	walk(err)
	return fields, other
}
