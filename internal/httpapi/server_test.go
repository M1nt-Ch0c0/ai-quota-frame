package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

const testAccessToken = "frame-test-token"

type staticSnapshotSource struct {
	snapshot quota.Snapshot
	ready    bool
}

func (source staticSnapshotSource) Snapshot() (quota.Snapshot, bool) {
	return source.snapshot, source.ready
}

type staticRenderer struct {
	png []byte
}

func (renderer staticRenderer) Render(quota.Snapshot) ([]byte, error) {
	return append([]byte(nil), renderer.png...), nil
}

type countingRenderer struct {
	calls int
	png   []byte
}

func (renderer *countingRenderer) Render(quota.Snapshot) ([]byte, error) {
	renderer.calls++
	return append([]byte(nil), renderer.png...), nil
}

func TestBearerAuthorization(t *testing.T) {
	handler := testServer().Handler()
	tests := []struct {
		name          string
		authorization string
		wantStatus    int
	}{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "wrong", authorization: "Bearer wrong-token", wantStatus: http.StatusUnauthorized},
		{name: "valid", authorization: "Bearer " + testAccessToken, wantStatus: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/quota", nil)
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantStatus == http.StatusUnauthorized && response.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("unauthorized response omitted WWW-Authenticate")
			}
		})
	}
}

func TestEmptyTokenIsDeniedUnlessExplicitlyAllowed(t *testing.T) {
	snapshot := staticSnapshotSource{ready: true, snapshot: quota.Snapshot{Accounts: []quota.Account{}}}
	for _, test := range []struct {
		name         string
		allowNoToken bool
		wantStatus   int
	}{
		{name: "secure default", wantStatus: http.StatusUnauthorized},
		{name: "explicit insecure mode", allowNoToken: true, wantStatus: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := New(snapshot, staticRenderer{png: []byte("png")}, "", test.allowNoToken).Handler()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/quota", nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}

func TestFramePNGRejectsWrongDisplayDimensions(t *testing.T) {
	handler := testServer().Handler()
	tests := []struct {
		name   string
		header string
		value  string
	}{
		{name: "width", header: "X-Display-Width", value: "799"},
		{name: "height", header: "X-Display-Height", value: "479"},
		{name: "invalid width", header: "X-Display-Width", value: "not-a-number"},
		{name: "explicit zero", header: "X-Display-Height", value: "0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/frame.png", nil)
			request.Header.Set("Authorization", "Bearer "+testAccessToken)
			request.Header.Set(test.header, test.value)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
		})
	}
}

func TestFramePNGETagWildcardConditionalRequest(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/frame.png", nil)
	request.Header.Set("Authorization", "Bearer "+testAccessToken)
	request.Header.Set("If-None-Match", "*")
	response := httptest.NewRecorder()
	testServer().Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotModified || response.Body.Len() != 0 {
		t.Fatalf("status/body = %d/%q, want 304/empty", response.Code, response.Body.String())
	}
}

func TestFramePNGETagConditionalRequest(t *testing.T) {
	handler := testServer().Handler()
	firstRequest := httptest.NewRequest(http.MethodGet, "/api/v1/frame.png", nil)
	firstRequest.Header.Set("Authorization", "Bearer "+testAccessToken)
	firstRequest.Header.Set("X-Display-Width", "800")
	firstRequest.Header.Set("X-Display-Height", "480")
	firstResponse := httptest.NewRecorder()

	handler.ServeHTTP(firstResponse, firstRequest)

	if firstResponse.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d; body = %s", firstResponse.Code, http.StatusOK, firstResponse.Body.String())
	}
	if got := firstResponse.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", got)
	}
	etag := firstResponse.Header().Get("ETag")
	if etag == "" {
		t.Fatal("first response omitted ETag")
	}
	if !bytes.Equal(firstResponse.Body.Bytes(), []byte("stable-png-payload")) {
		t.Fatalf("first body = %q", firstResponse.Body.Bytes())
	}

	secondRequest := httptest.NewRequest(http.MethodGet, "/api/v1/frame.png", nil)
	secondRequest.Header.Set("Authorization", "Bearer "+testAccessToken)
	secondRequest.Header.Set("If-None-Match", etag)
	secondResponse := httptest.NewRecorder()

	handler.ServeHTTP(secondResponse, secondRequest)

	if secondResponse.Code != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want %d; body = %s", secondResponse.Code, http.StatusNotModified, secondResponse.Body.String())
	}
	if secondResponse.Body.Len() != 0 {
		t.Fatalf("304 body length = %d, want 0", secondResponse.Body.Len())
	}
}

func TestConditionalFrameRequestReusesRenderAcrossObservationOnlyChanges(t *testing.T) {
	snapshot := testServer().source.(staticSnapshotSource).snapshot
	observedAt := snapshot.DataUpdatedAt
	snapshot.Accounts = []quota.Account{{
		Provider: "codex", Name: "account", Status: "ok", LastFreshAt: &observedAt,
		Windows: []quota.Window{{ID: "5h", RemainingPercent: quota.Percent(75), ObservedAt: &observedAt}},
	}}
	source := &mutableSnapshotSource{snapshot: snapshot, ready: true}
	renderer := &countingRenderer{png: []byte("cached-png")}
	handler := New(source, renderer, testAccessToken, false).Handler()

	firstRequest := httptest.NewRequest(http.MethodGet, "/api/v1/frame.png", nil)
	firstRequest.Header.Set("Authorization", "Bearer "+testAccessToken)
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, firstRequest)
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("first status = %d", firstResponse.Code)
	}

	newObservedAt := observedAt.Add(5 * time.Minute)
	source.snapshot.RefreshedAt = newObservedAt
	source.snapshot.NextRefreshAt = newObservedAt.Add(5 * time.Minute)
	source.snapshot.LastFreshAt = &newObservedAt
	source.snapshot.Accounts[0].LastFreshAt = &newObservedAt
	source.snapshot.Accounts[0].Windows[0].ObservedAt = &newObservedAt
	secondRequest := httptest.NewRequest(http.MethodGet, "/api/v1/frame.png", nil)
	secondRequest.Header.Set("Authorization", "Bearer "+testAccessToken)
	secondRequest.Header.Set("If-None-Match", firstResponse.Header().Get("ETag"))
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, secondRequest)

	if secondResponse.Code != http.StatusNotModified {
		t.Fatalf("second status = %d, want 304", secondResponse.Code)
	}
	if renderer.calls != 1 {
		t.Fatalf("Render() calls = %d, want 1", renderer.calls)
	}
}

type mutableSnapshotSource struct {
	snapshot quota.Snapshot
	ready    bool
}

func (source *mutableSnapshotSource) Snapshot() (quota.Snapshot, bool) {
	return source.snapshot, source.ready
}

func testServer() *Server {
	now := time.Date(2026, time.August, 30, 1, 30, 0, 0, time.UTC)
	return New(
		staticSnapshotSource{
			ready: true,
			snapshot: quota.Snapshot{
				SchemaVersion: quota.SchemaVersion,
				RefreshedAt:   now,
				DataUpdatedAt: now,
				NextRefreshAt: now.Add(5 * time.Minute),
				Accounts:      []quota.Account{},
			},
		},
		staticRenderer{png: []byte("stable-png-payload")},
		testAccessToken,
		false,
	)
}
