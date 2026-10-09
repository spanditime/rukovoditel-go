package api

import (
	"encoding/json"
	"fmt"
)

// Response is the common API envelope; Data keeps the action-specific JSON.
type Response struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
}

// HTTPError reports a non-2xx response and retains its raw body.
type HTTPError struct {
	StatusCode int
	Body       []byte
}

// Error returns a message without including the response body.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("api: unexpected HTTP status %d", e.StatusCode)
}

// APIError reports an unsuccessful API status and retains the raw response.
type APIError struct {
	Status string
	Data   json.RawMessage
	Body   []byte
}

// Error returns a message without including response data.
func (e *APIError) Error() string {
	return "rukovoditel api: unsuccessful status"
}

type safeCause struct {
	op    string
	cause error
}

func (e *safeCause) Error() string {
	return "api: " + e.op + " failed"
}

func (e *safeCause) Unwrap() error { return e.cause }

func (c *Client) safeError(op string, err error) error {
	return &safeCause{op: op, cause: err}
}
