// Package api is the HTTP layer for the Purelymail API.
//
// Every operation is `POST <base>/api/v0/<operation>` with a JSON body and the
// token in the Purelymail-Api-Token header. Success and failure both come back
// as HTTP 200 with an envelope:
//
//	{"type":"success","result":{...}}
//	{"type":"error","code":"invalidToken","message":"Token not valid."}
//
// Do unwraps the first and turns the second into an *APIError.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is Purelymail's production API host.
const DefaultBaseURL = "https://purelymail.com"

// AuthHeader carries the API token.
const AuthHeader = "Purelymail-Api-Token"

// Client talks to the API with one token.
type Client struct {
	HTTP    *http.Client
	Token   string
	BaseURL string

	// MaxRetries bounds how many times a retryable failure is re-issued.
	MaxRetries int
	// RetryBase is the first backoff interval; each retry doubles it.
	RetryBase time.Duration
	// RetryCap bounds a single backoff interval.
	RetryCap time.Duration

	// DryRun makes every call return a description of the request it would
	// have sent, without sending it.
	DryRun bool
	// Trace, when set, receives a line per request and retry.
	Trace io.Writer

	rand *rand.Rand
}

// Options configure a new client.
type Options struct {
	Token      string
	BaseURL    string
	MaxRetries int
	Timeout    time.Duration
	DryRun     bool
	Trace      io.Writer
}

// New builds a client, filling in defaults.
func New(o Options) *Client {
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	if o.Timeout <= 0 {
		o.Timeout = 60 * time.Second
	}
	if o.MaxRetries < 0 {
		o.MaxRetries = 0
	}
	return &Client{
		HTTP:       &http.Client{Timeout: o.Timeout},
		Token:      o.Token,
		BaseURL:    strings.TrimRight(o.BaseURL, "/"),
		MaxRetries: o.MaxRetries,
		RetryBase:  time.Second,
		RetryCap:   16 * time.Second,
		DryRun:     o.DryRun,
		Trace:      o.Trace,
		rand:       rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// URL is the endpoint for an operation.
func (c *Client) URL(operation string) string {
	return c.BaseURL + "/api/v0/" + url.PathEscape(strings.TrimLeft(operation, "/"))
}

// Do calls an operation and returns its unwrapped `result`.
//
// When DryRun is set nothing is sent; the returned document describes the
// request that would have gone out, with the token redacted.
func (c *Client) Do(ctx context.Context, operation string, body map[string]any) (json.RawMessage, error) {
	if body == nil {
		body = map[string]any{}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request body: %w", err)
	}
	endpoint := c.URL(operation)

	if c.DryRun {
		doc := map[string]any{
			"dry_run": true,
			"method":  http.MethodPost,
			"url":     endpoint,
			"headers": map[string]string{
				AuthHeader:     redact(c.Token),
				"Content-Type": "application/json",
			},
			"body": json.RawMessage(payload),
		}
		raw, _ := json.Marshal(doc)
		return raw, nil
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set(AuthHeader, c.Token)
		c.tracef("POST %s", endpoint)

		resp, err := c.HTTP.Do(req)
		if err != nil {
			// A failed connection never reached the server, so it is safe to
			// retry; anything later (a timeout mid-request) is not.
			if attempt < c.MaxRetries && ctx.Err() == nil && isDialError(err) {
				if werr := c.backoff(ctx, attempt, 0); werr != nil {
					return nil, werr
				}
				continue
			}
			return nil, fmt.Errorf("request failed: %w", err)
		}
		raw, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read response: %w", readErr)
		}

		result, apiErr := decode(resp.StatusCode, raw)
		if apiErr == nil {
			return result, nil
		}
		apiErr.Operation = operation
		if attempt >= c.MaxRetries || !apiErr.Retryable() {
			return nil, apiErr
		}
		c.tracef("retryable failure (http %d)", apiErr.Status)
		if werr := c.backoff(ctx, attempt, retryAfter(resp.Header)); werr != nil {
			return nil, werr
		}
	}
}

// decode unwraps the response envelope.
func decode(status int, raw []byte) (json.RawMessage, *APIError) {
	var env struct {
		Type    string          `json:"type"`
		Result  json.RawMessage `json:"result"`
		Code    string          `json:"code"`
		Message string          `json:"message"`
	}
	err := json.Unmarshal(raw, &env)
	if err == nil && env.Type == "" && status == http.StatusOK && looksLikeObject(raw) {
		// A bare JSON object without an envelope: pass it through rather
		// than lose it.
		return json.RawMessage(raw), nil
	}
	if err != nil || env.Type == "" {
		msg := strings.TrimSpace(string(raw))
		if msg == "" || strings.HasPrefix(msg, "<") {
			// Unknown operations come back as the site's HTML page.
			msg = http.StatusText(status)
			if status == http.StatusOK {
				msg = "response was not a Purelymail API envelope (unknown operation?)"
			}
		}
		if len(msg) > 500 {
			msg = msg[:500] + "..."
		}
		return nil, &APIError{Status: status, Code: statusCode(status), Message: msg}
	}
	if env.Type == "success" {
		if len(env.Result) == 0 || string(env.Result) == "null" {
			return json.RawMessage(`{}`), nil
		}
		return env.Result, nil
	}
	return nil, &APIError{Status: status, Code: env.Code, Message: env.Message}
}

func looksLikeObject(raw []byte) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '{'
}

func statusCode(status int) string {
	if status == http.StatusOK {
		return "invalidResponse"
	}
	return "http" + strconv.Itoa(status)
}

func isDialError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return strings.Contains(urlErr.Error(), "dial") || strings.Contains(urlErr.Error(), "connection refused")
	}
	return false
}

func (c *Client) backoff(ctx context.Context, attempt int, server time.Duration) error {
	wait := server
	if wait <= 0 {
		wait = c.RetryBase << attempt
		if wait > c.RetryCap || wait <= 0 {
			wait = c.RetryCap
		}
		wait = wait/2 + time.Duration(c.rand.Int63n(int64(wait/2)+1))
	}
	c.tracef("waiting %s before retry %d", wait.Round(time.Millisecond), attempt+1)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryAfter(h http.Header) time.Duration {
	if secs, err := strconv.Atoi(h.Get("Retry-After")); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

func (c *Client) tracef(format string, args ...any) {
	if c.Trace != nil {
		fmt.Fprintf(c.Trace, "purelymail: "+format+"\n", args...)
	}
}

func redact(s string) string {
	switch {
	case s == "":
		return "(not set)"
	case len(s) <= 8:
		return "<redacted>"
	}
	return s[:4] + "..." + s[len(s)-4:]
}
