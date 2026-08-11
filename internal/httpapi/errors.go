package httpapi

import (
	"encoding/json"
	"net/http"
)

// Error codes returned by the HTTP API.
const (
	CodeInvalidParams       = "INVALID_PARAMS"
	CodeUnauthorized        = "UNAUTHORIZED"
	CodeNotFound            = "NOT_FOUND"
	CodeInternal            = "INTERNAL"
	CodeDirectoryInvalid    = "DIRECTORY_INVALID"
	CodeModelNotFound       = "MODEL_NOT_FOUND"
	CodeModelNotConfigured  = "MODEL_NOT_CONFIGURED"
	CodeModeNotFound        = "MODE_NOT_FOUND"
	CodeQueueFull           = "QUEUE_FULL"
	CodePermissionExpired   = "PERMISSION_EXPIRED"
	CodeEventCursorGone     = "EVENT_CURSOR_GONE"
	CodeUnknownAuthMethod   = "UNKNOWN_AUTH_METHOD"
	CodeSessionNotFound     = "SESSION_NOT_FOUND"
)

// ErrorDetail is the wire shape of a single API error.
type ErrorDetail struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"requestId"`
}

// ErrorBody is the envelope returned for all error responses.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// APIError is an HTTP error with a stable code and HTTP status.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *APIError) Error() string {
	return e.Code + ": " + e.Message
}

// InvalidParams builds a 400 invalid params error.
func InvalidParams(message string) *APIError {
	return &APIError{Status: http.StatusBadRequest, Code: CodeInvalidParams, Message: message}
}

// Internal builds a 500 internal error.
func Internal(message string) *APIError {
	return &APIError{Status: http.StatusInternalServerError, Code: CodeInternal, Message: message}
}

// NotFound builds a 404 not found error.
func NotFound(code, message string) *APIError {
	return &APIError{Status: http.StatusNotFound, Code: code, Message: message}
}

// WriteError writes the unified error envelope.
func WriteError(w http.ResponseWriter, r *http.Request, err *APIError) {
	requestID, _ := r.Context().Value(requestIDKey).(string)
	body := ErrorBody{Error: ErrorDetail{
		Code:      err.Code,
		Message:   err.Message,
		Details:   err.Details,
		RequestID: requestID,
	}}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(err.Status)
	_ = json.NewEncoder(w).Encode(body)
}
