package api

import (
	"encoding/json"
	"net/http"
)

// Envelope is the standard JSON response wrapper for all API responses.
type Envelope struct {
	Data  any       `json:"data,omitempty"`
	Error *APIError `json:"error,omitempty"`
	Meta  Meta      `json:"meta"`
}

type Meta struct {
	RequestID string `json:"request_id,omitempty"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func WriteJSON(w http.ResponseWriter, status int, data any, requestID string) {
	env := Envelope{
		Data: data,
		Meta: Meta{RequestID: requestID},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(env) //nolint:errcheck
}

func WriteError(w http.ResponseWriter, status int, code, message, requestID string) {
	env := Envelope{
		Error: &APIError{Code: code, Message: message},
		Meta:  Meta{RequestID: requestID},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(env) //nolint:errcheck
}
