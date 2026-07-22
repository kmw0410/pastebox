package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pastebox "pastebox/internal"
)

func TestPasteAPIManageLifecycle(t *testing.T) {
	app := newTestApp(t)
	meta, _, _, manageToken, err := app.store.CreateWithLabel(strings.NewReader("api body"), "api.log", "text/plain", false, mustParseServerPolicy(t, "temporary"), "apipaste", "initial")
	if err != nil {
		t.Fatalf("CreateWithLabel failed: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/pastes/"+meta.ID, nil)
	request.Header.Set(manageTokenHeader, manageToken)
	response := httptest.NewRecorder()
	app.handle(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body=%q", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "_hash") || strings.Contains(response.Body.String(), manageToken) {
		t.Fatalf("GET response exposed private data: %q", response.Body.String())
	}
	var got pasteAPIResponse
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if got.ID != meta.ID || got.Filename != "api.log" || got.Label != "initial" || got.PasswordProtected {
		t.Fatalf("unexpected GET response: %+v", got)
	}

	patches := []struct {
		body          string
		wantLabel     string
		wantPolicy    string
		wantProtected bool
	}{
		{body: `{"action":"set_label","label":"updated"}`, wantLabel: "updated", wantPolicy: "temporary"},
		{body: `{"action":"set_policy","data_policy":"12h"}`, wantLabel: "updated", wantPolicy: "12h"},
		{body: `{"action":"enable_password","new_password":"managed-secret"}`, wantLabel: "updated", wantPolicy: "12h", wantProtected: true},
		{body: `{"action":"disable_password","password":"managed-secret"}`, wantLabel: "updated", wantPolicy: "12h"},
	}
	for _, patch := range patches {
		request = httptest.NewRequest(http.MethodPatch, "/api/v1/pastes/"+meta.ID, strings.NewReader(patch.body))
		request.Header.Set(manageTokenHeader, manageToken)
		response = httptest.NewRecorder()
		app.handle(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("PATCH %s status = %d, body=%q", patch.body, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "managed-secret") || strings.Contains(response.Body.String(), manageToken) {
			t.Fatalf("PATCH response exposed secret: %q", response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode PATCH response: %v", err)
		}
		if got.Label != patch.wantLabel || got.DataPolicy != patch.wantPolicy || got.PasswordProtected != patch.wantProtected {
			t.Fatalf("unexpected PATCH response: %+v", got)
		}
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/v1/pastes/"+meta.ID, nil)
	request.Header.Set(manageTokenHeader, manageToken)
	response = httptest.NewRecorder()
	app.handle(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"deleted": true`) {
		t.Fatalf("DELETE status = %d, body=%q", response.Code, response.Body.String())
	}
}

func TestPasteAPIDeletesWithDeleteToken(t *testing.T) {
	app := newTestApp(t)
	meta, _, deleteToken, _, err := app.store.Create(strings.NewReader("delete me"), "", "text/plain", false, mustParseServerPolicy(t, "temporary"), "apidel")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	request := httptest.NewRequest(http.MethodDelete, "/api/v1/pastes/"+meta.ID, nil)
	request.Header.Set(deleteTokenHeader, deleteToken)
	response := httptest.NewRecorder()
	app.handle(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body=%q", response.Code, response.Body.String())
	}
	if _, err := app.store.Open(meta.ID, ""); !errors.Is(err, pastebox.ErrNotFound) {
		t.Fatalf("Open after DELETE error = %v", err)
	}
}

func mustParseServerPolicy(t *testing.T, value string) pastebox.DataPolicy {
	t.Helper()
	policy, err := pastebox.ParseDataPolicy(value)
	if err != nil {
		t.Fatalf("ParseDataPolicy(%q): %v", value, err)
	}
	return policy
}
