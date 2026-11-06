package httpapi

import (
	"encoding/json"
	"net/http"
)

// errorBody is the JSON shape of every API error:
//
//	{"error": {"code": "not_found", "message": "resource not found"}}
//
// code is stable and meant for the frontend; message is for humans.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Fields maps invalid input fields to reasons (validation_failed only).
	Fields map[string]string `json:"fields,omitempty"`
}

// writeJSON writes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Headers are already sent; all we can do is record it.
		logFrom(r.Context()).WarnContext(r.Context(), "write response", "err", err)
	}
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, r, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}
