package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDecode(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		result     string
		code       string
	}{
		{"success", `{"type":"success","result":{"credit":"1"}}`, 200, `{"credit":"1"}`, ""},
		{"empty result", `{"type":"success","result":{}}`, 200, `{}`, ""},
		{"missing result", `{"type":"success"}`, 200, `{}`, ""},
		{"error", `{"type":"error","code":"invalidToken","message":"Token not valid."}`, 200, "", "invalidToken"},
		{"html page", `<html>nope</html>`, 200, "", "invalidResponse"},
		{"bare 502", ``, 502, "", "http502"},
	}
	for _, tc := range cases {
		res, apiErr := decode(tc.status, []byte(tc.body))
		if tc.code != "" {
			if apiErr == nil || apiErr.Code != tc.code {
				t.Errorf("%s: err = %v, want code %s", tc.name, apiErr, tc.code)
			}
			continue
		}
		if apiErr != nil {
			t.Errorf("%s: unexpected error %v", tc.name, apiErr)
			continue
		}
		if string(res) != tc.result {
			t.Errorf("%s: result = %s, want %s", tc.name, res, tc.result)
		}
	}
}

func TestExitCodes(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{errors.New("plain"), ExitGeneric},
		{&APIError{Code: "invalidToken"}, ExitAuth},
		{&APIError{Status: 403}, ExitAuth},
		{&APIError{Code: "somethingRejected"}, ExitRejected},
		{&APIError{Code: "domainNotFound"}, ExitNotFound},
		{&APIError{Status: 429}, ExitRateLimit},
		{&APIError{Status: 503}, ExitServer},
	}
	for _, tc := range cases {
		if got := ExitCode(tc.err); got != tc.want {
			t.Errorf("%v: exit = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestRetriesOnlyWhenNotProcessed(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"type":"success","result":{"ok":true}}`))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL, Token: "t", MaxRetries: 2})
	c.RetryBase = time.Millisecond
	res, err := c.Do(context.Background(), "listDomains", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(res) != `{"ok":true}` || hits.Load() != 2 {
		t.Errorf("res = %s after %d hits", res, hits.Load())
	}

	// A 500 may mean the server acted, so a create must not be repeated.
	hits.Store(0)
	srv500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv500.Close()
	c = New(Options{BaseURL: srv500.URL, Token: "t", MaxRetries: 3})
	c.RetryBase = time.Millisecond
	if _, err := c.Do(context.Background(), "createUser", nil); err == nil {
		t.Fatal("expected an error")
	}
	if hits.Load() != 1 {
		t.Errorf("500 was retried: %d hits", hits.Load())
	}

	// An API-level rejection is final.
	hits.Store(0)
	srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"type":"error","code":"x","message":"no"}`))
	}))
	defer srvErr.Close()
	c = New(Options{BaseURL: srvErr.URL, Token: "t", MaxRetries: 3})
	_, err = c.Do(context.Background(), "createUser", nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Operation != "createUser" || hits.Load() != 1 {
		t.Errorf("err = %v after %d hits", err, hits.Load())
	}
}

func TestRedact(t *testing.T) {
	if redact("") != "(not set)" || redact("short") != "<redacted>" || redact("abcdefghijkl") != "abcd...ijkl" {
		t.Error("redact")
	}
}
