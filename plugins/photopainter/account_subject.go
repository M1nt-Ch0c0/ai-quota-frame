package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Assigned by the C ABI bridge. Host responses are envelopes, not raw auth
// JSON. This callback never becomes a general-purpose browser RPC endpoint.
var callHost func(method string, request []byte) ([]byte, error)

func subjectJSON(status int, value any) managementResponse {
	body, _ := json.Marshal(value)
	return managementResponse{StatusCode: status, Body: body, Headers: http.Header{
		"Content-Type":  {"application/json; charset=utf-8"},
		"Cache-Control": {"no-store"}, "X-Content-Type-Options": {"nosniff"},
	}}
}

func subjectError(status int, code string) managementResponse {
	return subjectJSON(status, map[string]string{"error": code})
}

func hostResult(method string, input, output any) error {
	if callHost == nil {
		return errors.New("host callback unavailable")
	}
	request, err := json.Marshal(input)
	if err != nil {
		return err
	}
	raw, err := callHost(method, request)
	if err != nil || len(raw) > 4<<20 {
		return errors.New("host callback failed")
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(raw, &envelope) != nil || !envelope.OK || len(envelope.Result) == 0 || bytes.Equal(envelope.Result, []byte("null")) {
		return errors.New("invalid host callback response")
	}
	return json.Unmarshal(envelope.Result, output)
}

// accountSubject is reachable only through the authenticated management route.
// It projects xAI's public subject from the host's credential record. The
// access/refresh tokens, path, raw document and metadata never cross this API.
func accountSubject(query url.Values) managementResponse {
	values := query["name"]
	if len(query) != 1 || len(values) != 1 || values[0] == "" || len(values[0]) > 512 ||
		strings.ContainsAny(values[0], "/\\\x00\r\n\t") || strings.TrimSpace(values[0]) != values[0] {
		return subjectError(http.StatusBadRequest, "invalid_account_name")
	}
	name := values[0]
	var listing struct {
		Files []struct {
			Name      string `json:"name"`
			AuthIndex string `json:"auth_index"`
			Provider  string `json:"provider"`
			Type      string `json:"type"`
			Disabled  bool   `json:"disabled"`
		} `json:"files"`
	}
	if hostResult("host.auth.list", struct{}{}, &listing) != nil {
		return subjectError(http.StatusBadGateway, "account_lookup_failed")
	}
	match, count := -1, 0
	for index, item := range listing.Files {
		if item.Name == name {
			match, count = index, count+1
		}
	}
	if count == 0 {
		return subjectError(http.StatusNotFound, "account_not_found")
	}
	if count != 1 {
		return subjectError(http.StatusConflict, "ambiguous_account")
	}
	entry := listing.Files[match]
	provider := entry.Provider
	if provider == "" {
		provider = entry.Type
	}
	if provider != "xai" || (entry.Type != "" && entry.Type != "xai") || entry.Disabled || strings.TrimSpace(entry.AuthIndex) == "" {
		return subjectError(http.StatusNotFound, "account_not_available")
	}
	var credential struct {
		AuthIndex string          `json:"auth_index"`
		Name      string          `json:"name"`
		JSON      json.RawMessage `json:"json"`
	}
	if hostResult("host.auth.get", map[string]string{"auth_index": entry.AuthIndex}, &credential) != nil ||
		credential.AuthIndex != entry.AuthIndex || credential.Name != name {
		return subjectError(http.StatusBadGateway, "account_read_failed")
	}
	var record struct {
		Type    string `json:"type"`
		Subject string `json:"sub"`
	}
	if json.Unmarshal(credential.JSON, &record) != nil || record.Type != "xai" {
		return subjectError(http.StatusBadGateway, "invalid_account")
	}
	subject := strings.TrimSpace(record.Subject)
	if len(subject) == 0 || len(subject) > 256 {
		return subjectError(http.StatusNotFound, "account_subject_missing")
	}
	for _, r := range subject {
		if r < 0x21 || r > 0x7e {
			return subjectError(http.StatusBadGateway, "invalid_account_subject")
		}
	}
	return subjectJSON(http.StatusOK, map[string]string{"sub": subject})
}
