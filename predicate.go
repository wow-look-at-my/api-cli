// predicate.go holds evalPredicate.

package main

import (
	"errors"
	"strings"
	"text/template"
)

// evalPredicate renders a when=/until= predicate and reads it as a boolean. A
// field path through a value that is not an object, such as ".data.items" on
// a plain-text body, reads as missing and therefore false. That is the same
// answer a missing key gives.
func evalPredicate(tmpl string, data map[string]any) (bool, error) {
	out, err := renderString(tmpl, data)
	if err != nil {
		if isFieldOnScalar(err) {
			logVerbose("predicate %q: %v; reads as false", tmpl, err)
			return false, nil
		}
		return false, err
	}
	return isTruthy(out), nil
}

// isFieldOnScalar reports whether err is text/template's refusal to read a
// field from a value that has no fields: a string, a number. A bool or a list.
func isFieldOnScalar(err error) bool {
	var ee template.ExecError
	if !errors.As(err, &ee) {
		return false
	}
	return strings.Contains(ee.Err.Error(), "can't evaluate field ")
}
