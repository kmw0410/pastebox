package main

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	pastebox "pastebox/internal"
)

type adminPasteFilters struct {
	Query     string
	Policy    string
	Protected string
	Status    string
}

const defaultAdminPasteSort = "created-desc"

type adminPasteSort struct {
	Value string
}

type adminPasteView struct {
	pastebox.AdminPasteItem
	SizeDisplay       string
	ProtectedDisplay  string
	CreatedRelative   string
	ExpiresRelative   string
	ExpirationState   string
	ExpirationDisplay string
}

func buildAdminPasteViews(items []pastebox.AdminPasteItem, now time.Time, i18n *localizer) []adminPasteView {
	views := make([]adminPasteView, len(items))
	for i, item := range items {
		view := adminPasteView{
			AdminPasteItem:  item,
			SizeDisplay:     formatBytes(item.Size),
			CreatedRelative: adminRelativeTime(now, item.CreatedAt, i18n),
		}
		if item.Protected {
			view.ProtectedDisplay = i18n.T("admin_protected")
		} else {
			view.ProtectedDisplay = i18n.T("admin_public")
		}
		if !item.ExpiresAt.IsZero() {
			view.ExpiresRelative = adminRelativeTime(now, item.ExpiresAt, i18n)
			switch {
			case now.After(item.ExpiresAt):
				view.ExpirationState = "expired"
				view.ExpirationDisplay = i18n.T("admin_expired")
			case item.ExpiresAt.Sub(now) <= 24*time.Hour:
				view.ExpirationState = "expiring"
				view.ExpirationDisplay = i18n.T("admin_expiring_soon")
			}
		}
		views[i] = view
	}
	return views
}

func adminRelativeTime(now, value time.Time, i18n *localizer) string {
	delta := now.Sub(value)
	future := delta < 0
	if future {
		delta = -delta
	}
	if delta < time.Minute {
		return i18n.T("admin_time_now")
	}
	count := int(delta / time.Minute)
	unit := "admin_time_minutes"
	if delta >= 24*time.Hour {
		count = int(delta / (24 * time.Hour))
		unit = "admin_time_days"
	} else if delta >= time.Hour {
		count = int(delta / time.Hour)
		unit = "admin_time_hours"
	}
	if future {
		unit += "_from_now"
	} else {
		unit += "_ago"
	}
	return fmt.Sprintf(i18n.T(unit), count)
}

func adminPasteSortFromRequest(r *http.Request) adminPasteSort {
	value := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort")))
	switch value {
	case "created-asc", "expires-asc", "expires-desc", "size-asc", "size-desc", "filename-asc", "filename-desc":
		return adminPasteSort{Value: value}
	default:
		return adminPasteSort{Value: defaultAdminPasteSort}
	}
}

func sortAdminPasteItems(items []pastebox.AdminPasteItem, sorting adminPasteSort) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		switch sorting.Value {
		case "created-asc":
			return left.CreatedAt.Before(right.CreatedAt)
		case "expires-asc":
			return adminPasteExpiryBefore(left.ExpiresAt, right.ExpiresAt)
		case "expires-desc":
			return adminPasteExpiryAfter(left.ExpiresAt, right.ExpiresAt)
		case "size-asc":
			return left.Size < right.Size
		case "size-desc":
			return left.Size > right.Size
		case "filename-asc":
			return adminPasteFilenameBefore(left.Filename, right.Filename)
		case "filename-desc":
			return adminPasteFilenameAfter(left.Filename, right.Filename)
		default:
			return left.CreatedAt.After(right.CreatedAt)
		}
	})
}

func adminPasteExpiryBefore(left, right time.Time) bool {
	if left.IsZero() {
		return false
	}
	if right.IsZero() {
		return true
	}
	return left.Before(right)
}

func adminPasteExpiryAfter(left, right time.Time) bool {
	if left.IsZero() {
		return false
	}
	if right.IsZero() {
		return true
	}
	return left.After(right)
}

func adminPasteFilenameBefore(left, right string) bool {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	if left == "" {
		return false
	}
	if right == "" {
		return true
	}
	return left < right
}

func adminPasteFilenameAfter(left, right string) bool {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	if left == "" {
		return false
	}
	if right == "" {
		return true
	}
	return left > right
}

const defaultAdminPageSize = 50

type adminPagination struct {
	Limit       string
	Page        int
	TotalPages  int
	PreviousURL string
	NextURL     string
	Pages       []adminPaginationPage
}

type adminPaginationPage struct {
	Number   int
	URL      string
	Current  bool
	Ellipsis bool
}

func adminPaginationFromRequest(r *http.Request, totalItems int) adminPagination {
	limit := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("limit")))
	pageSize := defaultAdminPageSize
	switch limit {
	case "100":
		pageSize = 100
	case "all":
		return adminPagination{Limit: "all", Page: 1, TotalPages: 1}
	default:
		limit = strconv.Itoa(defaultAdminPageSize)
	}

	totalPages := (totalItems + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}

	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}

	pagination := adminPagination{
		Limit:      limit,
		Page:       page,
		TotalPages: totalPages,
	}
	if page > 1 {
		pagination.PreviousURL = adminPaginationURL(r, limit, page-1)
	}
	if page < totalPages {
		pagination.NextURL = adminPaginationURL(r, limit, page+1)
	}
	pagination.Pages = adminPaginationPages(r, limit, page, totalPages)

	return pagination
}

func adminPaginationPages(r *http.Request, limit string, current, total int) []adminPaginationPage {
	pages := make([]adminPaginationPage, 0, 7)
	addPage := func(number int) {
		page := adminPaginationPage{Number: number, Current: number == current}
		if !page.Current {
			page.URL = adminPaginationURL(r, limit, number)
		}
		pages = append(pages, page)
	}
	addEllipsis := func() {
		pages = append(pages, adminPaginationPage{Ellipsis: true})
	}

	if total <= 7 {
		for number := 1; number <= total; number++ {
			addPage(number)
		}
		return pages
	}

	addPage(1)
	if current > 3 {
		addEllipsis()
	}
	start := max(2, current-1)
	end := min(total-1, current+1)
	for number := start; number <= end; number++ {
		addPage(number)
	}
	if current < total-2 {
		addEllipsis()
	}
	addPage(total)
	return pages
}

func paginateAdminPasteItems(items []pastebox.AdminPasteItem, pagination adminPagination) []pastebox.AdminPasteItem {
	if pagination.Limit == "all" {
		return items
	}

	pageSize, _ := strconv.Atoi(pagination.Limit)
	start := (pagination.Page - 1) * pageSize
	if start >= len(items) {
		return nil
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

func adminPaginationURL(r *http.Request, limit string, page int) string {
	query := r.URL.Query()
	query.Set("limit", limit)
	query.Set("page", strconv.Itoa(page))
	return r.URL.Path + "?" + query.Encode()
}

func adminPasteFiltersFromRequest(r *http.Request) adminPasteFilters {
	return adminPasteFilters{
		Query:     strings.TrimSpace(r.URL.Query().Get("q")),
		Policy:    normalizeAdminFilterValue(r.URL.Query().Get("policy"), "temporary", "permanent", "once", "custom"),
		Protected: normalizeAdminFilterValue(r.URL.Query().Get("protected"), "yes", "no"),
		Status:    normalizeAdminFilterValue(r.URL.Query().Get("status"), "active", "expiring", "expired", "no-expiration"),
	}
}

func normalizeAdminFilterValue(value string, allowed ...string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, item := range allowed {
		if value == item {
			return value
		}
	}
	return ""
}

func filterAdminPasteItems(items []pastebox.AdminPasteItem, filters adminPasteFilters, now time.Time) []pastebox.AdminPasteItem {
	query := strings.ToLower(filters.Query)
	if query == "" && filters.Policy == "" && filters.Protected == "" && filters.Status == "" {
		return items
	}

	out := make([]pastebox.AdminPasteItem, 0, len(items))
	for _, item := range items {
		if query != "" && !strings.Contains(strings.ToLower(item.ID), query) && !strings.Contains(strings.ToLower(item.Filename), query) && !strings.Contains(strings.ToLower(item.Label), query) {
			continue
		}

		if filters.Policy != "" {
			policy := strings.ToLower(strings.TrimSpace(item.DataPolicy))
			if filters.Policy == "custom" {
				if policy == "" || policy == "temporary" || policy == "permanent" || policy == "once" {
					continue
				}
			} else if policy != filters.Policy {
				continue
			}
		}

		if filters.Protected != "" {
			wantProtected := filters.Protected == "yes"
			if item.Protected != wantProtected {
				continue
			}
		}

		if filters.Status != "" {
			if !adminPasteMatchesStatus(item, filters.Status, now) {
				continue
			}
		}

		out = append(out, item)
	}

	return out
}

func adminPasteMatchesStatus(item pastebox.AdminPasteItem, status string, now time.Time) bool {
	if item.ExpiresAt.IsZero() {
		return status == "no-expiration"
	}

	if now.After(item.ExpiresAt) {
		return status == "expired"
	}

	if item.ExpiresAt.Sub(now) <= 24*time.Hour {
		return status == "expiring"
	}

	return status == "active"
}

type adminStats struct {
	Total          int
	Temporary      int
	Permanent      int
	Protected      int
	Expiring24h    int
	Expired        int
	TotalSizeBytes int64
	TotalSize      string
}

func buildAdminStats(items []pastebox.AdminPasteItem) adminStats {
	now := time.Now().UTC()
	stats := adminStats{
		Total: len(items),
	}

	for _, item := range items {
		stats.TotalSizeBytes += item.Size

		if strings.EqualFold(item.DataPolicy, "permanent") {
			stats.Permanent++
		} else {
			stats.Temporary++
		}

		if item.Protected {
			stats.Protected++
		}

		if !item.ExpiresAt.IsZero() {
			if now.After(item.ExpiresAt) {
				stats.Expired++
			} else if item.ExpiresAt.Sub(now) <= 24*time.Hour {
				stats.Expiring24h++
			}
		}
	}

	stats.TotalSize = formatBytes(stats.TotalSizeBytes)
	return stats
}

func localizeAdminPasteItems(items []pastebox.AdminPasteItem, loc *time.Location) []pastebox.AdminPasteItem {
	if loc == nil {
		return items
	}

	out := make([]pastebox.AdminPasteItem, len(items))
	copy(out, items)

	for i := range out {
		out[i].CreatedAt = out[i].CreatedAt.In(loc)
		if !out[i].ExpiresAt.IsZero() {
			out[i].ExpiresAt = out[i].ExpiresAt.In(loc)
		}
	}

	return out
}

func formatBytes(size int64) string {
	const unit = 1024

	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	div := int64(unit)
	exp := 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}
