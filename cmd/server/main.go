package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	pastebox "pastebox/internal"
)

func main() {
	listenAddr := getenv("LISTEN_ADDR", ":8080")
	dataDir := getenv("DATA_DIR", "/paste-data")
	expireDays := getenvInt("EXPIRE_DAYS", 30)
	storageBackend := getenv("STORAGE_BACKEND", "local")
	mysqlDSN := strings.TrimSpace(os.Getenv("MYSQL_DSN"))
	zstdLevel := getenvInt("DB_ZSTD_LEVEL", 3)
	migrateLocalPastes := getenvBool("MIGRATE_LOCAL_PASTES", false)
	migrateSQLiteAdminAccounts := getenvBool("MIGRATE_SQLITE_ADMIN_ACCOUNTS", false)
	i18n := loadLocalizer(getenv("LANGUAGE", "en"))
	adminResetToken := strings.TrimSpace(os.Getenv("ADMIN_RESET_TOKEN"))
	discordWebhookURL := strings.TrimSpace(os.Getenv("DISCORD_WEBHOOK"))

	store, err := pastebox.NewStoreWithOptions(pastebox.StoreOptions{
		DataDir:                    dataDir,
		TTL:                        time.Duration(expireDays) * 24 * time.Hour,
		StorageBackend:             storageBackend,
		MySQLDSN:                   mysqlDSN,
		ZstdLevel:                  zstdLevel,
		MigrateLocalPastes:         migrateLocalPastes,
		MigrateSQLiteAdminAccounts: migrateSQLiteAdminAccounts,
	})
	if err != nil {
		logFatalEvent("server.init_store_failed", map[string]any{
			"data_dir":        dataDir,
			"error":           err,
			"storage_backend": storageBackend,
		})
	}

	a, err := newApp(store, i18n, adminResetToken)
	if err != nil {
		logFatalEvent("server.init_app_failed", map[string]any{
			"error": err,
		})
	}
	a.discordWebhook = newDiscordWebhookNotifier(discordWebhookURL)

	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			if err := store.CleanupExpired(); err != nil {
				logEvent("store.cleanup_failed", map[string]any{
					"error": err,
				})
			}
			<-ticker.C
		}
	}()

	go monitorStoreHealth(store, 30*time.Second)

	logEvent("server.started", map[string]any{
		"data_dir":        dataDir,
		"listen_addr":     listenAddr,
		"storage_backend": store.StorageBackend,
	})
	logEvent("server.storage_backend", map[string]any{
		"storage_backend": store.StorageBackend,
		"zstd_level":      zstdLevel,
	})
	if a.adminSetupToken != "" {
		logEvent("admin.setup_token_generated", map[string]any{
			"token": a.adminSetupToken,
		})
	}

	if err := http.ListenAndServe(listenAddr, a.httpHandler()); err != nil {
		logFatalEvent("server.listen_failed", map[string]any{
			"error":       err,
			"listen_addr": listenAddr,
		})
	}
}

func staticFileHandler(prefix string, directory string, cacheControl string) http.Handler {
	files := http.StripPrefix(prefix, http.FileServer(http.Dir(directory)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", cacheControl)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	})
}

func monitorStoreHealth(store *pastebox.Store, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var unhealthy bool

	check := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := store.HealthCheck(ctx)
		cancel()

		if err != nil {
			if !unhealthy {
				logEvent("store.health_unhealthy", map[string]any{
					"error":           err,
					"storage_backend": store.StorageBackend,
				})
			}
			unhealthy = true
			return
		}

		if unhealthy {
			logEvent("store.health_recovered", map[string]any{
				"storage_backend": store.StorageBackend,
			})
		}
		unhealthy = false
	}

	check()

	for range ticker.C {
		check()
	}
}
