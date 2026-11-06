package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// decodeJSON reads a JSON request body into dst strictly:
//
//   - Content-Type must be application/json (415 otherwise). Browsers cannot
//     send that type cross-site without a CORS preflight, which adds another
//     layer against CSRF.
//   - Unknown fields are rejected, so typos in field names do not silently
//     do nothing.
//   - Exactly one JSON value; trailing data is an error.
//   - Bodies over MaxBodyBytes (see limitBody) give 413.
//
// On failure it writes the error response and returns false.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		status, msg := http.StatusBadRequest, jsonErrorMessage(err)
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			status, msg = http.StatusRequestEntityTooLarge, fmt.Sprintf("request body must not exceed %d bytes", maxErr.Limit)
		}
		code := "invalid_json"
		if status == http.StatusRequestEntityTooLarge {
			code = "body_too_large"
		}
		writeError(w, r, status, code, msg)
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "body must contain a single JSON object")
		return false
	}
	return true
}

// jsonErrorMessage turns decoder errors into messages that help the client
// without echoing internals.
func jsonErrorMessage(err error) string {
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntaxErr):
		return fmt.Sprintf("malformed JSON at offset %d", syntaxErr.Offset)
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return "body is empty or truncated"
	case errors.As(err, &typeErr):
		if typeErr.Field != "" {
			return fmt.Sprintf("field %q has the wrong type", typeErr.Field)
		}
		return "body has the wrong type"
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return "unknown field " + strings.TrimPrefix(err.Error(), "json: unknown field ")
	default:
		// e.g. time.Time parse errors: "parsing time ... as RFC3339"
		return "invalid value: timestamps must be RFC 3339, e.g. 2026-11-10T18:00:00Z"
	}
}
