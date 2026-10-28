//go:build !js

package webframe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/frame"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Browser transport verification uses only a loopback fixture. The same
// native and WASM clients must produce the same quota/usage semantics, and
// the browser must never receive the complete xAI credential document.
func TestBrowserCollectorMatchesNative(t *testing.T) {
	if os.Getenv("PHOTOFRAME_WEB_PARITY") != "1" {
		t.Skip("set PHOTOFRAME_WEB_PARITY=1 after building the web frame engine")
	}
	fixture := func(name string) string {
		value, err := os.ReadFile("../cliproxy/testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(value)
	}
	codex, claude, xai := fixture("codex_usage.json"), fixture("claude_usage.json"), fixture("xai_billing_fresh_week.json")
	var browserPhase, failUpstream, blockList, canceledList atomic.Bool
	var redirectList atomic.Value
	var forbiddenDownloads, subjectQueries, apiCalls, usageCalls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><html><body><script src="/bridge-test.js"></script></body></html>`))
			return
		}
		if r.URL.Path == "/bridge-test.js" || r.URL.Path == "/wasm_exec.js" || r.URL.Path == "/frame.wasm" {
			path := "../../web/dist" + r.URL.Path
			if strings.HasSuffix(path, ".js") {
				w.Header().Set("Content-Type", "text/javascript")
			} else {
				w.Header().Set("Content-Type", "application/wasm")
			}
			http.ServeFile(w, r, path)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fixture-management-key" {
			t.Error("management authentication missing")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/management/auth-files":
			if destination, ok := redirectList.Load().(string); ok && destination != "" {
				http.Redirect(w, r, destination, http.StatusFound)
				return
			}
			if blockList.Load() {
				select {
				case <-r.Context().Done():
					canceledList.Store(true)
					return
				case <-time.After(10 * time.Second):
				}
			}
			_, _ = w.Write([]byte(`{"files":[
{"name":"codex.json","provider":"codex","account_type":"oauth","auth_index":"codex-fixture","email":"codex@example.test","plan_type":"plus","id_token":{"chatgpt_account_id":"workspace-fixture"}},
{"name":"claude.json","provider":"claude","account_type":"oauth","auth_index":"claude-fixture","email":"claude@example.test","plan_type":"max"},
{"name":"xai.json","provider":"xai","account_type":"oauth","auth_index":"xai-fixture","email":"grok@example.test","plan_type":"heavy"},
{"name":"kimi.json","provider":"kimi","account_type":"oauth","auth_index":"kimi-fixture","disabled":true}
]}`))
		case "/v0/management/auth-files/download":
			if browserPhase.Load() {
				forbiddenDownloads.Add(1)
				w.WriteHeader(403)
				return
			}
			_, _ = w.Write([]byte(`{"type":"xai","sub":"xai-subject-fixture","access_token":"NEVER_EXPOSE_PROVIDER_SECRET"}`))
		case "/v0/management/plugins/photopainter/account-subject":
			if r.URL.Query().Get("name") != "xai.json" {
				t.Error("wrong subject identity")
			}
			subjectQueries.Add(1)
			_, _ = w.Write([]byte(`{"sub":"xai-subject-fixture"}`))
		case "/v0/management/api-call":
			apiCalls.Add(1)
			if r.Method != http.MethodPost || r.Header.Get("Origin") != server.URL {
				t.Error("management POST origin mismatch")
			}
			var request struct {
				URL    string            `json:"url"`
				Method string            `json:"method"`
				Header map[string]string `json:"header"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil {
				t.Error("invalid provider request")
			}
			status, body := 200, "{}"
			switch {
			case strings.Contains(request.URL, "/wham/usage"):
				body = codex
			case strings.Contains(request.URL, "anthropic.com") && strings.HasSuffix(request.URL, "/usage"):
				body = claude
			case strings.Contains(request.URL, "cli-chat-proxy.grok.com") && strings.Contains(request.URL, "billing"):
				body = xai
				if request.Header["x-userid"] != "xai-subject-fixture" {
					t.Error("xAI subject was not retained")
				}
			default:
				status = 404
			}
			if failUpstream.Load() {
				status = 503
				body = `{"error":"fixture outage"}`
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": status, "body": body})
		case "/v0/management/dashboard/summary":
			usageCalls.Add(1)
			_, _ = w.Write([]byte(`{"today":{"total_tokens":1234567,"total_cost":1.25}}`))
		default:
			t.Errorf("unexpected browser endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	options := Options{}
	native, err := NewCollector(server.URL, "fixture-management-key", options)
	if err != nil {
		t.Fatal(err)
	}
	nativeFirst, err := native.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if nativeFirst.Stale || len(nativeFirst.Accounts) != 4 || nativeFirst.Usage == nil {
		t.Fatal("native fixture is not complete")
	}
	for _, account := range nativeFirst.Accounts {
		if account.Provider != "kimi" && (account.Status == "error" || len(account.Windows) == 0) {
			t.Fatalf("fixture adapter %s failed: %s", account.Provider, account.Error)
		}
	}
	allocatorOptions := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Flag("disable-gpu", true))
	if path := os.Getenv("CHROME_BIN"); path != "" {
		allocatorOptions = append(allocatorOptions, chromedp.ExecPath(path))
	}
	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(), allocatorOptions...)
	defer cancelAllocator()
	browser, cancelBrowser := chromedp.NewContext(allocator)
	defer cancelBrowser()
	ctx, cancel := context.WithTimeout(browser, 2*time.Minute)
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.Navigate(server.URL)); err != nil {
		t.Fatal(err)
	}
	collect := func() quota.Snapshot {
		var encoded string
		expression := `(async()=>JSON.stringify(await SharedFrameTest.collectSnapshot('fixture-management-key',AbortSignal.timeout(30000))))()`
		if err := chromedp.Run(ctx, chromedp.Evaluate(expression, &encoded, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(encoded, "NEVER_EXPOSE_PROVIDER_SECRET") {
			t.Fatal("provider secret escaped normalization")
		}
		var snapshot quota.Snapshot
		if json.Unmarshal([]byte(encoded), &snapshot) != nil {
			t.Fatal("invalid normalized browser snapshot")
		}
		return snapshot
	}
	browserPhase.Store(true)
	webFirst := collect()
	// Exercise real Go/WASM template generation and the native producer's
	// visible-content identity, including metadata-only refresh changes.
	fixtureJSON, _ := json.Marshal(nativeFirst)
	var browserHTML string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`SharedFrameTest.frameHTML(`+string(fixtureJSON)+`)`, &browserHTML, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
		t.Fatal(err)
	}
	nativeHTML, err := HTML(nativeFirst, Options{})
	if err != nil || nativeHTML != browserHTML {
		t.Fatal("native/WASM HTML document differs")
	}
	var keys struct{ Before, MetadataOnly, Changed string }
	fingerprintExpression := `(async()=>{const snapshot=` + string(fixtureJSON) + `;const before=await SharedFrameTest.frameFingerprint(snapshot);snapshot.refreshed_at='2030-01-01T00:00:00Z';snapshot.next_refresh_at='2030-01-01T00:15:00Z';snapshot.accounts[0].name='different display-only identity';const metadataOnly=await SharedFrameTest.frameFingerprint(snapshot);const account=snapshot.accounts.find(a=>a.windows.length);account.windows[0].remaining_percent=account.windows[0].remaining_percent===0?1:0;return {before,metadataOnly,changed:await SharedFrameTest.frameFingerprint(snapshot)}})()`
	if err := chromedp.Run(ctx, chromedp.Evaluate(fingerprintExpression, &keys, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
		t.Fatal(err)
	}
	expectedKey, err := frame.ContentKey(nativeFirst)
	if err != nil || keys.Before != fmt.Sprintf("%x", expectedKey) || keys.MetadataOnly != keys.Before || keys.Changed == keys.Before {
		t.Fatal("native/WASM content-key behavior differs")
	}
	if !reflect.DeepEqual(canonicalSnapshot(nativeFirst), canonicalSnapshot(webFirst)) {
		left, _ := json.Marshal(canonicalSnapshot(nativeFirst))
		right, _ := json.Marshal(canonicalSnapshot(webFirst))
		t.Fatalf("collector semantics differ\nnative=%s\nweb=%s", left, right)
	}
	if forbiddenDownloads.Load() != 0 || subjectQueries.Load() != 1 {
		t.Fatal("browser credential boundary was not enforced")
	}
	if webFirst.Usage == nil || len(webFirst.Usage.Days) != 7 || usageCalls.Load() != 14 {
		t.Fatal("usage did not reuse the seven-day native collector")
	}
	// Exercise the same service's best-known-value retention after an outage.
	failUpstream.Store(true)
	browserPhase.Store(false)
	nativeSecond, err := native.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	browserPhase.Store(true)
	webSecond := collect()
	if !webSecond.Stale || !reflect.DeepEqual(canonicalSnapshot(nativeSecond), canonicalSnapshot(webSecond)) {
		t.Fatal("outage/retention semantics differ")
	}
	if forbiddenDownloads.Load() != 0 {
		t.Fatal("browser attempted private credential download")
	}
	t.Logf("native/WASM parity: %d accounts, %d usage days, active reads=%d, sanitized subject queries=%d", len(webFirst.Accounts), len(webFirst.Usage.Days), apiCalls.Load(), subjectQueries.Load())
	var reset any
	if err := chromedp.Run(ctx, chromedp.Evaluate(`SharedFrameTest.forgetSourceCredentials()`, &reset)); err != nil {
		t.Fatal(err)
	}
	blockList.Store(true)
	var aborted bool
	abortExpression := `(async()=>{const stop=new AbortController();const pending=SharedFrameTest.collectSnapshot('fixture-management-key',stop.signal);setTimeout(()=>stop.abort(),100);try{await pending;return false}catch{return stop.signal.aborted}})()`
	if err := chromedp.Run(ctx, chromedp.Evaluate(abortExpression, &aborted, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
		t.Fatal(err)
	}
	blockList.Store(false)
	if !aborted {
		t.Fatal("browser cancellation did not reject collection")
	}
	for i := 0; i < 25 && !canceledList.Load(); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !canceledList.Load() {
		t.Fatal("browser cancellation did not abort the active HTTP fetch")
	}
	var externalRequests atomic.Int32
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { externalRequests.Add(1); w.WriteHeader(200) }))
	defer external.Close()
	redirectList.Store(external.URL)
	var redirectRejected bool
	redirectExpression := `(async()=>{try{await SharedFrameTest.collectSnapshot('fixture-management-key',AbortSignal.timeout(5000));return false}catch{return true}})()`
	if err := chromedp.Run(ctx, chromedp.Evaluate(redirectExpression, &redirectRejected, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
		t.Fatal(err)
	}
	if !redirectRejected || externalRequests.Load() != 0 {
		t.Fatal("management fetch followed a redirect")
	}
}

func canonicalSnapshot(snapshot quota.Snapshot) quota.Snapshot {
	snapshot.RefreshedAt = time.Time{}
	snapshot.DataUpdatedAt = time.Time{}
	snapshot.NextRefreshAt = time.Time{}
	snapshot.LastFreshAt = nil
	for i := range snapshot.Accounts {
		snapshot.Accounts[i].RetentionID = ""
		snapshot.Accounts[i].LastFreshAt = nil
		for j := range snapshot.Accounts[i].Windows {
			snapshot.Accounts[i].Windows[j].ObservedAt = nil
		}
	}
	return snapshot
}
