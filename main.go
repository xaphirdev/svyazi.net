package main

import (
	"embed"
	"errors"
	"log"
	"net/http"
	"os"
	"svyazi.net/server"
	"time"

	"github.com/joho/godotenv"
)

//go:embed index.html styles.css app.js incident-model.mjs frontend-assets/vue.esm-browser.prod.js
var assets embed.FS

//go:embed data/incident-seed.json
var seed []byte

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		client := http.Client{Timeout: 3 * time.Second}
		response, err := client.Get("http://127.0.0.1:8000/healthz")
		if err != nil {
			log.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			log.Fatal("application is not ready")
		}
		return
	}
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Fatalf("Не удалось прочитать .env: %v", err)
	}
	if err := server.Run(assets, seed); err != nil {
		log.Fatal(err)
	}
}
