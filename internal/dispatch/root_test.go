package dispatch

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/KudcraftsHQ/purelymail-cli/internal/api"
)

// call is one request the stub API received.
type call struct {
	Method string
	Path   string
	Token  string
	CType  string
	Body   map[string]any
}

// stubAPI records every request and answers each operation from a table, which
// is how the parts that only exist once flags, config, dispatch and the client
// are wired together get tested end to end.
type stubAPI struct {
	*httptest.Server
	mu    sync.Mutex
	calls []call
}

func newStub(t *testing.T, answers map[string]string) *stubAPI {
	t.Helper()
	s := &stubAPI{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.calls = append(s.calls, call{r.Method, r.URL.Path, r.Header.Get(api.AuthHeader), r.Header.Get("Content-Type"), body})
		s.mu.Unlock()

		op := strings.TrimPrefix(r.URL.Path, "/api/v0/")
		answer, ok := answers[op]
		if !ok {
			answer = `{"type":"success","result":{}}`
		}
		if strings.HasPrefix(answer, "HTTP ") {
			var code int
			_, _ = sscanStatus(answer, &code)
			w.WriteHeader(code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(s.Close)
	return s
}

func sscanStatus(s string, code *int) (int, error) {
	n := 0
	for _, c := range strings.TrimPrefix(s, "HTTP ") {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	*code = n
	return n, nil
}

func (s *stubAPI) Calls() []call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]call(nil), s.calls...)
}

type result struct {
	out, errOut string
	err         error
	code        int
}

// run executes the CLI with a token in the environment and no config file.
func run(t *testing.T, srv *stubAPI, args ...string) result {
	t.Helper()
	t.Setenv("PURELYMAIL_API_TOKEN", "test-token-value")
	return runEnv(t, srv, args...)
}

func runEnv(t *testing.T, srv *stubAPI, args ...string) result {
	t.Helper()
	if os.Getenv("PURELYMAIL_CONFIG") == "" {
		t.Setenv("PURELYMAIL_CONFIG", filepath.Join(t.TempDir(), "absent.toml"))
	}
	t.Setenv("PURELYMAIL_PROFILE", "")
	if srv != nil {
		t.Setenv("PURELYMAIL_API_URL", srv.URL)
	} else {
		// Nothing should be sent; make sure nothing could be.
		t.Setenv("PURELYMAIL_API_URL", "http://127.0.0.1:1")
	}
	var out, errOut bytes.Buffer
	root := NewRoot(&out, &errOut)
	root.SetArgs(args)
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(""))
	err := root.Execute()
	code := 0
	if err != nil {
		code = api.ExitCode(err)
	}
	return result{out.String(), errOut.String(), err, code}
}

func decodeJSON(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, s)
	}
	return v
}

func TestRequestShape(t *testing.T) {
	srv := newStub(t, nil)
	r := run(t, srv, "routing", "create", "--domain-name", "example.com", "--prefix=false",
		"--match-user", "hello", "--target-addresses", "a@x.com", "--target-addresses", "b@x.com,c@x.com")
	if r.err != nil {
		t.Fatal(r.err)
	}
	calls := srv.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	c := calls[0]
	if c.Method != "POST" || c.Path != "/api/v0/createRoutingRule" {
		t.Errorf("called %s %s", c.Method, c.Path)
	}
	if c.Token != "test-token-value" {
		t.Errorf("auth header = %q", c.Token)
	}
	if c.CType != "application/json" {
		t.Errorf("content type = %q", c.CType)
	}
	want := map[string]any{
		"domainName": "example.com", "prefix": false, "matchUser": "hello",
		"targetAddresses": []any{"a@x.com", "b@x.com", "c@x.com"},
	}
	if !jsonEqual(c.Body, want) {
		t.Errorf("body = %v, want %v", c.Body, want)
	}
	// Unset optional booleans must not be sent: false is a setting.
	if _, sent := c.Body["catchall"]; sent {
		t.Error("catchall was sent although not given")
	}
}

func TestIntegerFieldAndPositional(t *testing.T) {
	srv := newStub(t, nil)
	r := run(t, srv, "routing", "delete", "42", "--yes")
	if r.err != nil {
		t.Fatal(r.err)
	}
	body := srv.Calls()[0].Body
	if body["routingRuleId"] != float64(42) {
		t.Errorf("routingRuleId = %#v, want the number 42", body["routingRuleId"])
	}
}

func TestPositionalAndFlagConflict(t *testing.T) {
	r := run(t, nil, "domain", "add", "a.com", "--domain-name", "b.com")
	if r.err == nil || !strings.Contains(r.err.Error(), "both") {
		t.Fatalf("err = %v", r.err)
	}
}

func TestBodyMergesWithFlagsWinning(t *testing.T) {
	srv := newStub(t, nil)
	dir := t.TempDir()
	file := filepath.Join(dir, "u.json")
	os.WriteFile(file, []byte(`{"userName":"old@x.com","newPassword":"p","enableSearchIndexing":true}`), 0o600)

	r := run(t, srv, "user", "modify", "-f", file, "--user-name", "new@x.com", "--enable-search-indexing=false")
	if r.err != nil {
		t.Fatal(r.err)
	}
	want := map[string]any{"userName": "new@x.com", "newPassword": "p", "enableSearchIndexing": false}
	if got := srv.Calls()[0].Body; !jsonEqual(got, want) {
		t.Errorf("body = %v, want %v", got, want)
	}

	r = run(t, srv, "domain", "list", "--body", `{"includeShared":true}`)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := srv.Calls()[1].Body; got["includeShared"] != true {
		t.Errorf("body = %v", got)
	}
}

func TestMissingRequiredFieldsAreNamed(t *testing.T) {
	r := run(t, nil, "user", "create", "--user-name", "a")
	if r.err == nil {
		t.Fatal("expected an error")
	}
	for _, flag := range []string{"--domain-name", "--password"} {
		if !strings.Contains(r.err.Error(), flag) {
			t.Errorf("error does not name %s: %v", flag, r.err)
		}
	}
	if r.code != 1 {
		t.Errorf("exit = %d, want 1", r.code)
	}
}

func TestResultIsUnwrapped(t *testing.T) {
	srv := newStub(t, map[string]string{
		"listDomains": `{"type":"success","result":{"domains":[{"name":"example.com","isShared":false}]}}`,
	})
	r := run(t, srv, "domain", "list")
	if r.err != nil {
		t.Fatal(r.err)
	}
	v := decodeJSON(t, r.out).(map[string]any)
	if _, ok := v["type"]; ok {
		t.Errorf("envelope leaked into output: %s", r.out)
	}
	if len(v["domains"].([]any)) != 1 {
		t.Errorf("out = %s", r.out)
	}

	r = run(t, srv, "domain", "list", "-o", "table")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !strings.Contains(r.out, "NAME") || !strings.Contains(r.out, "example.com") {
		t.Errorf("table = %q", r.out)
	}
}

func TestErrorEnvelopeMapsToExitCodes(t *testing.T) {
	cases := []struct {
		answer string
		code   int
	}{
		{`{"type":"error","code":"invalidToken","message":"Token not valid."}`, api.ExitAuth},
		{`{"type":"error","code":"domainAlreadyExists","message":"nope"}`, api.ExitRejected},
		{`{"type":"error","code":"userNotFound","message":"no such user"}`, api.ExitNotFound},
		{"HTTP 500", api.ExitServer},
	}
	for _, tc := range cases {
		srv := newStub(t, map[string]string{"getUser": tc.answer})
		r := run(t, srv, "user", "get", "a@x.com", "--max-retries", "0")
		if r.err == nil {
			t.Errorf("%s: expected an error", tc.answer)
			continue
		}
		if r.code != tc.code {
			t.Errorf("%s: exit = %d, want %d (%v)", tc.answer, r.code, tc.code, r.err)
		}
		if r.out != "" {
			t.Errorf("%s: stdout should be empty on error, got %q", tc.answer, r.out)
		}
	}
}

func TestDryRunSendsNothing(t *testing.T) {
	srv := newStub(t, nil)
	r := run(t, srv, "user", "create", "--user-name", "a", "--domain-name", "x.com",
		"--password", "secret", "--send-welcome-email=false", "--dry-run")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if n := len(srv.Calls()); n != 0 {
		t.Fatalf("dry run sent %d requests", n)
	}
	v := decodeJSON(t, r.out).(map[string]any)
	if v["dry_run"] != true || v["method"] != "POST" ||
		v["url"] != srv.URL+"/api/v0/createUser" {
		t.Errorf("dry run = %s", r.out)
	}
	headers := v["headers"].(map[string]any)
	if strings.Contains(r.out, "test-token-value") || headers[api.AuthHeader] == "" {
		t.Errorf("token not redacted: %s", r.out)
	}
	body := v["body"].(map[string]any)
	if body["userName"] != "a" || body["sendWelcomeEmail"] != false {
		t.Errorf("body = %v", body)
	}
}

func TestDryRunNeedsNoToken(t *testing.T) {
	t.Setenv("PURELYMAIL_API_TOKEN", "")
	r := runEnv(t, nil, "domain", "list", "--dry-run")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !strings.Contains(r.out, "listDomains") {
		t.Errorf("out = %s", r.out)
	}
}

func TestMissingTokenIsExitOne(t *testing.T) {
	t.Setenv("PURELYMAIL_API_TOKEN", "")
	r := runEnv(t, nil, "domain", "list")
	if r.err == nil || !strings.Contains(r.err.Error(), "PURELYMAIL_API_TOKEN") {
		t.Fatalf("err = %v", r.err)
	}
	if r.code != 1 {
		t.Errorf("exit = %d", r.code)
	}
}

func TestDestructiveOpsNeedYes(t *testing.T) {
	destructive := [][]string{
		{"user", "delete", "a@x.com"},
		{"domain", "delete", "x.com"},
		{"routing", "delete", "7"},
		{"app-password", "delete", "--user-name", "a@x.com", "--app-password", "p"},
		{"password-reset", "delete", "a@x.com", "--target", "b@y.com"},
		{"raw", "deleteDomain", "--body", `{"name":"x.com"}`},
	}
	for _, args := range destructive {
		srv := newStub(t, nil)
		r := run(t, srv, args...)
		if r.err == nil || !strings.Contains(r.err.Error(), "--yes") {
			t.Errorf("%v: err = %v, want a refusal naming --yes", args, r.err)
		}
		if n := len(srv.Calls()); n != 0 {
			t.Errorf("%v: sent %d requests without --yes", args, n)
		}

		r = run(t, srv, append(args, "--dry-run")...)
		if r.err != nil {
			t.Errorf("%v --dry-run: %v", args, r.err)
		}
		if n := len(srv.Calls()); n != 0 {
			t.Errorf("%v --dry-run: sent %d requests", args, n)
		}

		r = run(t, srv, append(args, "--yes")...)
		if r.err != nil {
			t.Errorf("%v --yes: %v", args, r.err)
		}
		if n := len(srv.Calls()); n != 1 {
			t.Errorf("%v --yes: sent %d requests, want 1", args, n)
		}
	}
}

func TestNonDestructiveOpsHaveNoYesFlag(t *testing.T) {
	r := run(t, nil, "domain", "add", "x.com", "--yes", "--dry-run")
	if r.err == nil || !strings.Contains(r.err.Error(), "unknown flag") {
		t.Errorf("err = %v", r.err)
	}
}

func TestRaw(t *testing.T) {
	srv := newStub(t, map[string]string{
		"checkAccountCredit": `{"type":"success","result":{"credit":"12.50"}}`,
	})
	r := run(t, srv, "raw", "checkAccountCredit")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if strings.TrimSpace(r.out) != `{"credit":"12.50"}` {
		t.Errorf("out = %s", r.out)
	}
	if c := srv.Calls()[0]; c.Path != "/api/v0/checkAccountCredit" || len(c.Body) != 0 {
		t.Errorf("call = %+v", c)
	}

	r = run(t, srv, "raw", "someFutureOp", "--body", `{"a":1}`)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !strings.Contains(r.errOut, "not in the generated tree") {
		t.Errorf("stderr = %q", r.errOut)
	}
	if c := srv.Calls()[1]; c.Path != "/api/v0/someFutureOp" || c.Body["a"] != float64(1) {
		t.Errorf("call = %+v", c)
	}

	if r := run(t, srv, "raw", "list/../x"); r.err == nil {
		t.Error("a path in the operation name should be refused")
	}
}

func TestConfigVerify(t *testing.T) {
	srv := newStub(t, map[string]string{
		"checkAccountCredit": `{"type":"success","result":{"credit":"3.14"}}`,
	})
	r := run(t, srv, "config", "verify")
	if r.err != nil {
		t.Fatal(r.err)
	}
	v := decodeJSON(t, r.out).(map[string]any)
	if v["ok"] != true || v["credit"] != "3.14" {
		t.Errorf("out = %s", r.out)
	}
	if c := srv.Calls()[0]; c.Path != "/api/v0/checkAccountCredit" {
		t.Errorf("verify called %s", c.Path)
	}

	refused := newStub(t, map[string]string{
		"checkAccountCredit": `{"type":"error","code":"invalidToken","message":"Token not valid."}`,
	})
	if r := run(t, refused, "config", "verify"); r.code != 2 {
		t.Errorf("refused token: exit = %d (%v), want 2", r.code, r.err)
	}

	broken := newStub(t, map[string]string{"checkAccountCredit": "HTTP 500"})
	if r := run(t, broken, "config", "verify", "--max-retries", "0"); r.code != 1 {
		t.Errorf("server error: exit = %d (%v), want 1", r.code, r.err)
	}

	t.Setenv("PURELYMAIL_API_TOKEN", "")
	if r := runEnv(t, srv, "config", "verify"); r.code != 1 {
		t.Errorf("no token: exit = %d (%v), want 1", r.code, r.err)
	}
}

func TestProfiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte("api_token = \"default-tok\"\n\n[work]\napi_token = \"work-tok\"\n"), 0o600)
	t.Setenv("PURELYMAIL_CONFIG", path)
	t.Setenv("PURELYMAIL_API_TOKEN", "")

	srv := newStub(t, nil)
	if r := runEnv(t, srv, "account", "credit"); r.err != nil {
		t.Fatal(r.err)
	}
	if r := runEnv(t, srv, "--profile", "work", "account", "credit"); r.err != nil {
		t.Fatal(r.err)
	}
	if r := runEnv(t, srv, "--token", "flag-tok", "--profile", "work", "account", "credit"); r.err != nil {
		t.Fatal(r.err)
	}
	calls := srv.Calls()
	got := []string{calls[0].Token, calls[1].Token, calls[2].Token}
	want := []string{"default-tok", "work-tok", "flag-tok"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d token = %q, want %q", i, got[i], want[i])
		}
	}
	if r := runEnv(t, srv, "--profile", "nope", "account", "credit"); r.err == nil {
		t.Error("unknown profile should fail")
	}
}

func TestDomainDNS(t *testing.T) {
	srv := newStub(t, map[string]string{
		"getOwnershipCode": `{"type":"success","result":{"code":"abc123"}}`,
	})
	r := run(t, srv, "domain", "dns", "Example.com.")
	if r.err != nil {
		t.Fatal(r.err)
	}
	var recs []map[string]any
	if err := json.Unmarshal([]byte(r.out), &recs); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"MX example.com":                           "mailserver.purelymail.com",
		"TXT example.com spf":                      "v=spf1 include:_spf.purelymail.com ~all",
		"TXT example.com own":                      "purelymail_ownership_proof=abc123",
		"CNAME purelymail1._domainkey.example.com": "key1.dkimroot.purelymail.com",
		"CNAME purelymail2._domainkey.example.com": "key2.dkimroot.purelymail.com",
		"CNAME purelymail3._domainkey.example.com": "key3.dkimroot.purelymail.com",
		"CNAME _dmarc.example.com":                 "dmarcroot.purelymail.com",
	}
	got := map[string]string{}
	for _, rec := range recs {
		key := rec["type"].(string) + " " + rec["name"].(string)
		if rec["type"] == "TXT" {
			if strings.HasPrefix(rec["content"].(string), "v=spf1") {
				key += " spf"
			} else {
				key += " own"
			}
		}
		got[key] = rec["content"].(string)
		if rec["type"] == "MX" && rec["priority"] != float64(50) {
			t.Errorf("MX priority = %v", rec["priority"])
		}
	}
	if !jsonEqual(got, want) {
		t.Errorf("records = %v\nwant %v", got, want)
	}

	// A code that already carries the prefix is used as-is.
	srv2 := newStub(t, map[string]string{
		"getOwnershipCode": `{"type":"success","result":{"code":"purelymail_ownership_proof=zz"}}`,
	})
	r = run(t, srv2, "domain", "dns", "x.org", "-o", "table")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !strings.Contains(r.out, "purelymail_ownership_proof=zz") || strings.Contains(r.out, "=purelymail_ownership_proof") {
		t.Errorf("table = %s", r.out)
	}
}

func TestDomainDNSOffline(t *testing.T) {
	t.Setenv("PURELYMAIL_API_TOKEN", "")
	r := runEnv(t, nil, "domain", "dns", "example.com", "--skip-ownership")
	if r.err != nil {
		t.Fatal(r.err)
	}
	var recs []map[string]any
	json.Unmarshal([]byte(r.out), &recs)
	if len(recs) != 6 {
		t.Errorf("got %d records, want 6 without ownership", len(recs))
	}
	if r := runEnv(t, nil, "domain", "dns", "not a domain", "--skip-ownership"); r.err == nil {
		t.Error("expected a bad-domain error")
	}
}

func TestDiscovery(t *testing.T) {
	t.Setenv("PURELYMAIL_API_TOKEN", "")

	r := runEnv(t, nil, "list")
	if r.err != nil {
		t.Fatal(r.err)
	}
	for _, name := range []string{"user", "password-reset", "app-password", "routing", "domain", "account"} {
		if !strings.Contains(r.out, name) {
			t.Errorf("list is missing %s", name)
		}
	}

	r = runEnv(t, nil, "list", "-o", "json")
	rows := decodeJSON(t, r.out).([]any)
	if len(rows) != 6 {
		t.Errorf("list -o json: %d resources", len(rows))
	}

	r = runEnv(t, nil, "describe", "user", "create", "-o", "json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	doc := decodeJSON(t, r.out).(map[string]any)
	op := doc["op"].(map[string]any)
	if op["operation"] != "createUser" || doc["command"] != "purelymail user create" {
		t.Errorf("describe = %s", r.out)
	}
	required := map[string]bool{}
	for _, p := range op["params"].([]any) {
		pm := p.(map[string]any)
		required[pm["name"].(string)] = pm["required"].(bool)
	}
	if !required["userName"] || !required["password"] || required["recoveryEmail"] {
		t.Errorf("required flags wrong: %v", required)
	}

	r = runEnv(t, nil, "describe", "domain", "list")
	for _, want := range []string{"--include-shared", "boolean", "domains[].dnsSummary.passesMx", "RETURNS"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("describe text is missing %q:\n%s", want, r.out)
		}
	}

	// The API's own name resolves too.
	r = runEnv(t, nil, "describe", "addDomain")
	if r.err != nil || !strings.Contains(r.out, "purelymail domain add") {
		t.Errorf("describe addDomain = %v %s", r.err, r.out)
	}

	r = runEnv(t, nil, "describe", "domian")
	if r.err == nil || !strings.Contains(r.err.Error(), "domain") {
		t.Errorf("typo suggestion: %v", r.err)
	}

	r = runEnv(t, nil, "tree")
	tree := decodeJSON(t, r.out).(map[string]any)
	if len(tree["resources"].([]any)) != 6 {
		t.Errorf("tree resources = %v", len(tree["resources"].([]any)))
	}
}

func TestUnknownCommandSuggests(t *testing.T) {
	r := run(t, nil, "domians")
	if r.err == nil || !strings.Contains(r.err.Error(), "domain") {
		t.Errorf("err = %v", r.err)
	}
}

func jsonEqual(a, b any) bool {
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	var va, vb any
	json.Unmarshal(ra, &va)
	json.Unmarshal(rb, &vb)
	na, _ := json.Marshal(va)
	nb, _ := json.Marshal(vb)
	return bytes.Equal(na, nb)
}
