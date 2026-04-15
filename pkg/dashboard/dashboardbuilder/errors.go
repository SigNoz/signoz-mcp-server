package dashboardbuilder

import (
	"fmt"
	"strings"
)

// FieldError represents a single validation error on a specific field.
// Field uses JSONPath-style notation (e.g., "widgets[0].query.queryType").
type FieldError struct {
	Field   string
	Message string
}

func (e FieldError) String() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidationError accumulates multiple field-level validation errors.
type ValidationError struct {
	Errors []FieldError
}

func (e *ValidationError) Error() string {
	if len(e.Errors) == 0 {
		return "validation passed"
	}
	msgs := make([]string, len(e.Errors))
	for i, fe := range e.Errors {
		msgs[i] = fe.String()
	}
	return fmt.Sprintf("validation failed with %d error(s):\n  %s", len(e.Errors), strings.Join(msgs, "\n  "))
}

// HasErrors returns true if any validation errors have been accumulated.
func (e *ValidationError) HasErrors() bool {
	return len(e.Errors) > 0
}

// Add appends a field error.
func (e *ValidationError) Add(field, message string) {
	e.Errors = append(e.Errors, FieldError{Field: field, Message: message})
}

// Addf appends a formatted field error.
func (e *ValidationError) Addf(field, format string, args ...interface{}) {
	e.Errors = append(e.Errors, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
}
