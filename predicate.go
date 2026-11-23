// predicate.go holds evalPredicate.

package main

import (
	"errors"
	"strings"
	"text/template"
)

// evalPredicate renders a when=/until= predicate and reads it as a boolean. A
// field path that leads nowhere reads as false. That covers a missing key, a
// missing parent, and a value with no fields, such as a plain-text body.
func evalPredicate(tmpl string, data map[string]any) (bool, error) {
	out, err := renderString(tmpl, data)
	if err != nil {
		if isPathMiss(err) {
			logVerbose("predicate %q: %v; reads as false", tmpl, err)
			return false, nil
		}
		return false, err
	}
	return isTruthy(out), nil
}

// isPathMiss reports whether err is text/template's refusal to read a field
// through a value that has no fields. Any other error is a broken config.
func isPathMiss(err error) bool {
	var ee template.ExecError
	if !errors.As(err, &ee) {
		return false
	}
	msg := ee.Err.Error()
	return strings.Contains(msg, "can't evaluate field ") || strings.Contains(msg, "nil pointer evaluating ")
}
