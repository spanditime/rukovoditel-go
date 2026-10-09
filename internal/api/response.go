package api

import (
	"encoding/json"
	"fmt"
)

type Response struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
}

type APIError struct {
	Status string
	Data   json.RawMessage
}

func (e *APIError) Error() string {
	if len(e.Data) == 0 || string(e.Data) == "null" {
		return fmt.Sprintf("rukovoditel api: status %q", e.Status)
	}

	return fmt.Sprintf(
		"rukovoditel api: status %q: %s",
		e.Status,
		e.Data,
	)
}
