package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	pastebox "pastebox/internal"
)

func TestManagePageDeletesThroughAPI(t *testing.T) {
	templateSource, err := os.ReadFile("../../templates/manage.html")
	if err != nil {
		t.Fatalf("read manage template: %v", err)
	}

	source := string(templateSource)
	for _, required := range []string{
		`data-delete-url="/api/v1/pastes/{{ .ID }}"`,
		`method: "DELETE"`,
		`"paste-manage-token": button.dataset.manageToken`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("manage template does not use authenticated DELETE API; missing %q", required)
		}
	}
	if strings.Contains(source, `name="manage_action" value="delete"`) {
		t.Fatal("manage template still submits the legacy POST delete action")
	}
}

func TestParseManagePolicyForm(t *testing.T) {
	tests := []struct {
		name    string
		form    string
		want    string
		wantErr error
	}{
		{
			name: "temporary",
			form: "data_policy=temporary",
			want: "temporary",
		},
		{
			name: "custom duration",
			form: "data_policy=custom&custom_policy=12h",
			want: "12h",
		},
		{
			name: "custom duration trims and lowercases",
			form: "data_policy=custom&custom_policy= 7D ",
			want: "7d",
		},
		{
			name:    "custom duration missing",
			form:    "data_policy=custom",
			wantErr: pastebox.ErrInvalidPolicy,
		},
		{
			name:    "custom duration invalid",
			form:    "data_policy=custom&custom_policy=31d",
			wantErr: pastebox.ErrInvalidPolicy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/paste?manage=token", strings.NewReader(tt.form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if err := req.ParseForm(); err != nil {
				t.Fatalf("ParseForm failed: %v", err)
			}

			got, err := parseManagePolicyForm(req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("parseManagePolicyForm() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("parseManagePolicyForm() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSplitManagePolicy(t *testing.T) {
	tests := []struct {
		policy     string
		wantKind   string
		wantCustom string
	}{
		{policy: "", wantKind: "temporary", wantCustom: ""},
		{policy: "temporary", wantKind: "temporary", wantCustom: ""},
		{policy: "permanent", wantKind: "permanent", wantCustom: ""},
		{policy: "once", wantKind: "once", wantCustom: ""},
		{policy: "12h", wantKind: "custom", wantCustom: "12h"},
	}

	for _, tt := range tests {
		t.Run(tt.policy, func(t *testing.T) {
			gotKind, gotCustom := splitManagePolicy(tt.policy)
			if gotKind != tt.wantKind || gotCustom != tt.wantCustom {
				t.Fatalf("splitManagePolicy(%q) = (%q, %q), want (%q, %q)", tt.policy, gotKind, gotCustom, tt.wantKind, tt.wantCustom)
			}
		})
	}
}
