package main

import (
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
	i18n := loadLocalizer(getenv("LANGUAGE", "en"))
	adminResetToken := strings.TrimSpace(os.Getenv("ADMIN_RESET_TOKEN"))

	store, err := pastebox.NewStore(dataDir, time.Duration(expireDays)*24*time.Hour)
	if err != nil {
		logFatalEvent("server.init_store_failed", map[string]any{
			"data_dir": dataDir,
			"error":    err,
		})
	}

	a, err := newApp(store, i18n, adminResetToken)
	if err != nil {
		logFatalEvent("server.init_app_failed", map[string]any{
			"error": err,
		})
	}

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

	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handle)

	logEvent("server.started", map[string]any{
		"data_dir":    dataDir,
		"listen_addr": listenAddr,
	})
	logEvent("admin.setup_token_generated", map[string]any{
		"token": a.adminSetupToken,
	})

	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		logFatalEvent("server.listen_failed", map[string]any{
			"error":       err,
			"listen_addr": listenAddr,
		})
	}
}
