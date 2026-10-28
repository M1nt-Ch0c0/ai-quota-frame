package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
)

func hostEnvelope(value any) []byte {
	raw, _ := json.Marshal(map[string]any{"ok": true, "result": value})
	return raw
}

func TestAccountSubjectProjectsOnlySubject(t *testing.T) {
	previous := callHost
	t.Cleanup(func() { callHost = previous })
	calls := []string{}
	callHost = func(method string, request []byte) ([]byte, error) {
		calls = append(calls, method)
		if method == "host.auth.list" {
			return hostEnvelope(map[string]any{"files": []any{map[string]any{"name": "xai-fixture.json", "auth_index": "fixture-index", "provider": "xai", "type": "xai"}}}), nil
		}
		if method != "host.auth.get" || string(request) != `{"auth_index":"fixture-index"}` {
			t.Fatal("unexpected host operation")
		}
		return hostEnvelope(map[string]any{"auth_index": "fixture-index", "name": "xai-fixture.json", "path": "/private/auth.json", "json": json.RawMessage(`{"type":"xai","sub":"fixture-subject","access_token":"NEVER_RETURN_ACCESS","refresh_token":"NEVER_RETURN_REFRESH","other":"private"}`)}), nil
	}
	response := accountSubject(url.Values{"name": {"xai-fixture.json"}})
	if response.StatusCode != 200 || string(response.Body) != `{"sub":"fixture-subject"}` || len(calls) != 2 || response.Headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid projection: status=%d body=%q calls=%v", response.StatusCode, response.Body, calls)
	}
}

func TestAccountSubjectRejectsInvalidAndAmbiguousLookup(t *testing.T) {
	previous := callHost
	t.Cleanup(func() { callHost = previous })
	for _, query := range []url.Values{{}, {"name": {"../auth.json"}}, {"name": {"one", "two"}}, {"name": {"one"}, "auth_index": {"other"}}, {"name": {" spaced "}}} {
		callHost = func(string, []byte) ([]byte, error) { t.Fatal("invalid request reached host"); return nil, nil }
		if accountSubject(query).StatusCode != 400 {
			t.Fatal("invalid name was accepted")
		}
	}
	for _, files := range [][]map[string]any{
		{},
		{{"name": "one", "auth_index": "id", "provider": "claude"}},
		{{"name": "one", "auth_index": "id", "provider": "xai", "disabled": true}},
		{{"name": "one", "auth_index": "id", "provider": "xai"}, {"name": "one", "auth_index": "other", "provider": "xai"}},
	} {
		callHost = func(method string, _ []byte) ([]byte, error) {
			if method != "host.auth.list" {
				t.Fatal("ineligible account reached get")
			}
			return hostEnvelope(map[string]any{"files": files}), nil
		}
		if response := accountSubject(url.Values{"name": {"one"}}); response.StatusCode == 200 {
			t.Fatal("ineligible account accepted")
		}
	}
}

func TestAccountSubjectNeverReturnsHostErrorsOrWrongRecord(t *testing.T) {
	previous := callHost
	t.Cleanup(func() { callHost = previous })
	for _, mode := range []string{"error", "envelope", "identity", "type", "subject"} {
		callHost = func(method string, _ []byte) ([]byte, error) {
			if mode == "error" {
				return nil, errors.New("PRIVATE_HOST_VALUE")
			}
			if mode == "envelope" {
				return []byte(`{"ok":false,"error":{"message":"PRIVATE_HOST_VALUE"}}`), nil
			}
			if method == "host.auth.list" {
				return hostEnvelope(map[string]any{"files": []any{map[string]any{"name": "one", "auth_index": "id", "provider": "xai"}}}), nil
			}
			name, kind, subject := "one", "xai", "valid"
			if mode == "identity" {
				name = "other"
			}
			if mode == "type" {
				kind = "claude"
			}
			if mode == "subject" {
				subject = "invalid\nsubject"
			}
			raw, _ := json.Marshal(map[string]string{"type": kind, "sub": subject, "access_token": "PRIVATE_HOST_VALUE"})
			return hostEnvelope(map[string]any{"auth_index": "id", "name": name, "json": json.RawMessage(raw)}), nil
		}
		response := accountSubject(url.Values{"name": {"one"}})
		if response.StatusCode == 200 || strings.Contains(string(response.Body), "PRIVATE_HOST_VALUE") {
			t.Fatalf("unsafe response for %s", mode)
		}
	}
}
