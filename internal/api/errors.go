package api

import (
	"errors"
	"fmt"
	"strings"
)

// APIError is Purelymail's error envelope. The API answers HTTP 200 for a
// rejected call and says so in the body:
//
//	{"type":"error","code":"invalidToken","message":"Token not valid."}
//
// so the HTTP status alone says nothing. Status is set only when the transport
// itself failed (a 5xx from the load balancer, a 429, an HTML error page).
type APIError struct {
	Status    int    `json:"-"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Operation string `json:"-"`
}

// Error codes worth reacting to by name.
const (
	CodeInvalidToken = "invalidToken"
)

func (e *APIError) Error() string {
	var b strings.Builder
	b.WriteString("purelymail api error")
	var parts []string
	if e.Operation != "" {
		parts = append(parts, e.Operation)
	}
	if e.Code != "" {
		parts = append(parts, "code "+e.Code)
	}
	if e.Status != 0 && e.Status != 200 {
		parts = append(parts, fmt.Sprintf("http %d", e.Status))
	}
	if len(parts) > 0 {
		b.WriteString(" (" + strings.Join(parts, ", ") + ")")
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if hint := e.Hint(); hint != "" {
		b.WriteString("\n  ")
		b.WriteString(hint)
	}
	return b.String()
}

// Hint turns the common failures into something actionable.
func (e *APIError) Hint() string {
	switch {
	case e.Code == CodeInvalidToken:
		return "the API token was refused; check PURELYMAIL_API_TOKEN (Purelymail: Settings -> API tokens)"
	case e.isAuth():
		return "the token is not allowed to do this"
	case e.Status == 0 || e.Status == 200:
		return "`purelymail describe <resource> <op>` lists the fields this operation accepts"
	}
	return ""
}

func (e *APIError) isAuth() bool {
	c := strings.ToLower(e.Code)
	return e.Code == CodeInvalidToken || strings.Contains(c, "token") ||
		strings.Contains(c, "unauthori") || strings.Contains(c, "permission") ||
		strings.Contains(c, "forbidden") || e.Status == 401 || e.Status == 403
}

// Retryable reports whether re-sending could plausibly succeed. Only answers
// that mean "not processed" qualify: every operation is a POST, and a create
// that timed out after the server acted on it must not run twice.
func (e *APIError) Retryable() bool {
	switch e.Status {
	case 429, 502, 503, 504:
		return true
	}
	return false
}

// Exit codes, so scripts can branch:
//
//	0  success
//	1  generic failure, usage error, or bad local config (e.g. no token)
//	2  credential refused by the API
//	3  request rejected by the API (validation, conflict, unknown name)
//	4  rate limited
//	5  server-side or transient failure
//	6  not found
const (
	ExitGeneric   = 1
	ExitAuth      = 2
	ExitRejected  = 3
	ExitRateLimit = 4
	ExitServer    = 5
	ExitNotFound  = 6
)

// ExitCode maps an error onto a shell exit status.
func ExitCode(err error) int {
	var e *APIError
	if !errors.As(err, &e) {
		return ExitGeneric
	}
	lower := strings.ToLower(e.Code)
	switch {
	case e.isAuth():
		return ExitAuth
	case e.Status == 429 || strings.Contains(lower, "ratelimit") || strings.Contains(lower, "toomany"):
		return ExitRateLimit
	case e.Status == 404 || strings.Contains(lower, "notfound") || strings.Contains(lower, "nosuch"):
		return ExitNotFound
	case e.Status >= 500:
		return ExitServer
	}
	return ExitRejected
}
