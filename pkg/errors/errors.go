// Package errors provides error types for arc
package errors

import "fmt"

// APIError represents an API error
type APIError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
}

func (e *APIError) Error() string {
    return fmt.Sprintf("API error %d: %s", e.Code, e.Message)
}
