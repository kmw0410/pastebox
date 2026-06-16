package main

import (
	"log"
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
		log.Fatalf("failed to initialize store: %v", err)
	}

	a, err := newApp(store, i18n, adminResetToken)
	if err != nil {
		log.Fatalf("failed to initialize app: %v", err)
	}

	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			if err := store.CleanupExpired(); err != nil {
				log.Printf("cleanup failed: %v", err)
			}
			<-ticker.C
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handle)

	log.Printf("pastebox listening on %s, data=%s", listenAddr, dataDir)
	log.Printf("admin setup token: %s", a.adminSetupToken)

	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatal(err)
	}
}
