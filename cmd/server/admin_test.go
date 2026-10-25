package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	pastebox "pastebox/internal"
)

func TestFilterAdminPasteItems(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	items := []pastebox.AdminPasteItem{
		{
			ID:         "build-log",
			Filename:   "server.log",
			DataPolicy: "temporary",
			ExpiresAt:  now.Add(48 * time.Hour),
			Protected:  false,
		},
		{
			ID:         "secret",
			Filename:   "config.yaml",
			DataPolicy: "permanent",
			Protected:  true,
		},
		{
			ID:         "once1",
			Filename:   "readme.md",
			DataPolicy: "once",
			ExpiresAt:  now.Add(30 * time.Minute),
			Protected:  false,
		},
		{
			ID:         "custom1",
			Filename:   "trace.txt",
			DataPolicy: "12h",
			ExpiresAt:  now.Add(-time.Hour),
			Protected:  true,
		},
	}

	tests := []struct {
		name    string
		filters adminPasteFilters
		want    []string
	}{
		{
			name:    "query matches code and filename",
			filters: adminPasteFilters{Query: "LOG"},
			want:    []string{"build-log"},
		},
		{
			name:    "custom policy",
			filters: adminPasteFilters{Policy: "custom"},
			want:    []string{"custom1"},
		},
		{
			name:    "protected",
			filters: adminPasteFilters{Protected: "yes"},
			want:    []string{"secret", "custom1"},
		},
		{
			name:    "expiring",
			filters: adminPasteFilters{Status: "expiring"},
			want:    []string{"once1"},
		},
		{
			name:    "no expiration",
			filters: adminPasteFilters{Status: "no-expiration"},
			want:    []string{"secret"},
		},
		{
			name:    "combined filters",
			filters: adminPasteFilters{Policy: "custom", Protected: "yes", Status: "expired"},
			want:    []string{"custom1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterAdminPasteItems(items, tt.filters, now)
			gotIDs := make([]string, 0, len(got))
			for _, item := range got {
				gotIDs = append(gotIDs, item.ID)
			}
			if strings.Join(gotIDs, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("filterAdminPasteItems() = %v, want %v", gotIDs, tt.want)
			}
		})
	}
}

func TestBuildAdminPasteViews(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	i18n := &localizer{messages: map[string]string{
		"admin_protected":             "Protected",
		"admin_public":                "Public",
		"admin_expiring_soon":         "Expiring Soon",
		"admin_expired":               "Expired",
		"admin_time_now":              "now",
		"admin_time_minutes_ago":      "%dm ago",
		"admin_time_hours_ago":        "%dh ago",
		"admin_time_days_ago":         "%dd ago",
		"admin_time_minutes_from_now": "in %dm",
		"admin_time_hours_from_now":   "in %dh",
		"admin_time_days_from_now":    "in %dd",
	}}
	items := []pastebox.AdminPasteItem{
		{
			ID:        "expiring",
			Size:      1536,
			Protected: true,
			CreatedAt: now.Add(-2 * time.Hour),
			ExpiresAt: now.Add(time.Hour),
		},
		{
			ID:        "expired",
			Size:      42,
			CreatedAt: now.Add(-48 * time.Hour),
			ExpiresAt: now.Add(-time.Minute),
		},
	}

	views := buildAdminPasteViews(items, now, i18n)
	if got, want := views[0].SizeDisplay, "1.5 KiB"; got != want {
		t.Fatalf("SizeDisplay = %q, want %q", got, want)
	}
	if got, want := views[0].ProtectedDisplay, "Protected"; got != want {
		t.Fatalf("ProtectedDisplay = %q, want %q", got, want)
	}
	if got, want := views[0].CreatedRelative, "2h ago"; got != want {
		t.Fatalf("CreatedRelative = %q, want %q", got, want)
	}
	if got, want := views[0].ExpiresRelative, "in 1h"; got != want {
		t.Fatalf("ExpiresRelative = %q, want %q", got, want)
	}
	if got, want := views[0].ExpirationState, "expiring"; got != want {
		t.Fatalf("ExpirationState = %q, want %q", got, want)
	}
	if got, want := views[1].ProtectedDisplay, "Public"; got != want {
		t.Fatalf("Public display = %q, want %q", got, want)
	}
	if got, want := views[1].ExpirationState, "expired"; got != want {
		t.Fatalf("ExpirationState = %q, want %q", got, want)
	}
}

func TestAdminPagination(t *testing.T) {
	items := make([]pastebox.AdminPasteItem, 120)
	for i := range items {
		items[i].ID = strconv.Itoa(i + 1)
	}

	tests := []struct {
		name      string
		url       string
		wantLimit string
		wantPage  int
		wantPages int
		wantItems int
		wantFirst string
		wantPrev  bool
		wantNext  bool
	}{
		{
			name:      "defaults to the first 50 items",
			url:       "/admin",
			wantLimit: "50", wantPage: 1, wantPages: 3, wantItems: 50, wantFirst: "1", wantNext: true,
		},
		{
			name:      "keeps filters while navigating the second 100 item page",
			url:       "/admin?q=log&sort=size-desc&limit=100&page=2",
			wantLimit: "100", wantPage: 2, wantPages: 2, wantItems: 20, wantFirst: "101", wantPrev: true,
		},
		{
			name:      "all disables pagination",
			url:       "/admin?limit=all&page=8",
			wantLimit: "all", wantPage: 1, wantPages: 1, wantItems: 120, wantFirst: "1",
		},
		{
			name:      "invalid values fall back and out of range pages clamp",
			url:       "/admin?limit=200&page=99",
			wantLimit: "50", wantPage: 3, wantPages: 3, wantItems: 20, wantFirst: "101", wantPrev: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			got := adminPaginationFromRequest(req, len(items))
			if got.Limit != tt.wantLimit || got.Page != tt.wantPage || got.TotalPages != tt.wantPages {
				t.Fatalf("adminPaginationFromRequest() = %#v", got)
			}
			gotItems := paginateAdminPasteItems(items, got)
			if len(gotItems) != tt.wantItems {
				t.Fatalf("paginateAdminPasteItems() returned %d items, want %d", len(gotItems), tt.wantItems)
			}
			if len(gotItems) > 0 && gotItems[0].ID != tt.wantFirst {
				t.Fatalf("first item = %q, want %q", gotItems[0].ID, tt.wantFirst)
			}
			if (got.PreviousURL != "") != tt.wantPrev || (got.NextURL != "") != tt.wantNext {
				t.Fatalf("pagination URLs = previous %q, next %q", got.PreviousURL, got.NextURL)
			}
			if strings.Contains(tt.url, "q=log") && !strings.Contains(got.PreviousURL, "q=log") {
				t.Fatalf("previous URL did not preserve filters: %q", got.PreviousURL)
			}
			if strings.Contains(tt.url, "sort=size-desc") && !strings.Contains(got.PreviousURL, "sort=size-desc") {
				t.Fatalf("previous URL did not preserve sorting: %q", got.PreviousURL)
			}
		})
	}
}

func TestAdminPaginationPages(t *testing.T) {
	tests := []struct {
		name    string
		current int
		total   int
		want    string
	}{
		{name: "shows every short page range", current: 2, total: 3, want: "1,2*,3"},
		{name: "compresses middle pages near the start", current: 2, total: 10, want: "1,2*,3,…,10"},
		{name: "shows pages around the current page", current: 5, total: 10, want: "1,…,4,5*,6,…,10"},
		{name: "compresses middle pages near the end", current: 9, total: 10, want: "1,…,8,9*,10"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin?q=log&sort=size-desc", nil)
			pages := adminPaginationPages(req, "50", tt.current, tt.total)
			got := make([]string, 0, len(pages))
			for _, page := range pages {
				if page.Ellipsis {
					got = append(got, "…")
					continue
				}
				value := strconv.Itoa(page.Number)
				if page.Current {
					value += "*"
				} else if !strings.Contains(page.URL, "q=log") || !strings.Contains(page.URL, "sort=size-desc") {
					t.Fatalf("page URL did not preserve list state: %q", page.URL)
				}
				got = append(got, value)
			}
			if actual := strings.Join(got, ","); actual != tt.want {
				t.Fatalf("pages = %q, want %q", actual, tt.want)
			}
		})
	}
}

func TestSortAdminPasteItems(t *testing.T) {
	created := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	items := []pastebox.AdminPasteItem{
		{ID: "old", Filename: "zeta.log", CreatedAt: created, ExpiresAt: created.Add(72 * time.Hour), Size: 20},
		{ID: "permanent", Filename: "", CreatedAt: created.Add(2 * time.Hour), Size: 10},
		{ID: "new", Filename: "alpha.log", CreatedAt: created.Add(time.Hour), ExpiresAt: created.Add(24 * time.Hour), Size: 30},
	}

	tests := []struct {
		sort string
		want string
	}{
		{sort: "created-desc", want: "permanent,new,old"},
		{sort: "created-asc", want: "old,new,permanent"},
		{sort: "expires-asc", want: "new,old,permanent"},
		{sort: "expires-desc", want: "old,new,permanent"},
		{sort: "size-desc", want: "new,old,permanent"},
		{sort: "filename-asc", want: "new,old,permanent"},
		{sort: "filename-desc", want: "old,new,permanent"},
	}

	for _, tt := range tests {
		t.Run(tt.sort, func(t *testing.T) {
			got := append([]pastebox.AdminPasteItem(nil), items...)
			sortAdminPasteItems(got, adminPasteSort{Value: tt.sort})
			ids := make([]string, 0, len(got))
			for _, item := range got {
				ids = append(ids, item.ID)
			}
			if actual := strings.Join(ids, ","); actual != tt.want {
				t.Fatalf("sort %q = %q, want %q", tt.sort, actual, tt.want)
			}
		})
	}
}

func TestAdminLogoutClearsSingleDeleteConfirmationPreference(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/logout", nil)
	req.AddCookie(&http.Cookie{Name: "pastebox_admin", Value: "session-token"})
	rr := httptest.NewRecorder()

	app.adminLogoutHandler(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
	}
	if location := rr.Header().Get("Location"); location != "/admin/login" {
		t.Fatalf("Location = %q, want /admin/login", location)
	}

	for _, cookie := range rr.Result().Cookies() {
		if cookie.Name == adminSkipSingleDeleteConfirmCookieName {
			if cookie.MaxAge != -1 || cookie.Path != "/admin" {
				t.Fatalf("preference cookie = %#v, want an expired /admin cookie", cookie)
			}
			return
		}
	}
	t.Fatalf("logout did not clear %q", adminSkipSingleDeleteConfirmCookieName)
}

func TestNewAppGeneratesSetupTokenOnlyWithoutAdmin(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir("../.."); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	store, err := pastebox.NewStore(t.TempDir(), 24*time.Hour)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	i18n := loadLocalizer("en")

	appWithoutAdmin, err := newApp(store, i18n, "")
	if err != nil {
		t.Fatalf("newApp without admin failed: %v", err)
	}
	if appWithoutAdmin.adminSetupToken == "" {
		t.Fatalf("expected setup token when admin does not exist")
	}

	if err := store.CreateAdmin("admin", "password123"); err != nil {
		t.Fatalf("CreateAdmin failed: %v", err)
	}

	appWithAdmin, err := newApp(store, i18n, "")
	if err != nil {
		t.Fatalf("newApp with admin failed: %v", err)
	}
	if appWithAdmin.adminSetupToken != "" {
		t.Fatalf("expected empty setup token when admin exists, got %q", appWithAdmin.adminSetupToken)
	}
}

func TestSummarizeAdminIDs(t *testing.T) {
	tests := []struct {
		name string
		ids  []string
		want string
	}{
		{
			name: "empty",
			ids:  nil,
			want: "",
		},
		{
			name: "trim and join",
			ids:  []string{" one ", "", "two"},
			want: "one,two",
		},
		{
			name: "truncate long list",
			ids: []string{
				"1", "2", "3", "4", "5", "6", "7", "8", "9", "10",
				"11", "12", "13", "14", "15", "16", "17", "18", "19", "20",
				"21", "22",
			},
			want: "1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,...(+2 more)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summarizeAdminIDs(tt.ids); got != tt.want {
				t.Fatalf("summarizeAdminIDs() = %q, want %q", got, tt.want)
			}
		})
	}
}
