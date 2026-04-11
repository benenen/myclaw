package main

import (
	"log"
	stdhttp "net/http"
	"strings"

	"github.com/benenen/myclaw/internal/bootstrap"
	"github.com/benenen/myclaw/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	app, err := bootstrap.New(cfg)
	if err != nil {
		log.Fatalf("bootstrap app: %v", err)
	}

	log.Printf("web server listening on %s", serviceURL(cfg.HTTPAddr))

	if err := stdhttp.ListenAndServe(cfg.HTTPAddr, app.Handler); err != nil {
		log.Fatalf("run server: %v", err)
	}
}

func serviceURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	return "http://" + addr
}
