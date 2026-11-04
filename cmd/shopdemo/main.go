// Command shopdemo is the second, deliberately different app for Phase 11: two small HTTP
// services (catalog, orders) and Postgres. It exists to prove Rehearse works on an app it
// was not written for. Run as `shopdemo catalog` or `shopdemo orders`.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: shopdemo catalog|orders")
		os.Exit(2)
	}
	addr := ":" + env("PORT", "8080")
	var h http.Handler
	switch os.Args[1] {
	case "catalog":
		h = catalog()
	case "orders":
		h = orders()
	default:
		fmt.Fprintln(os.Stderr, "unknown service", os.Args[1])
		os.Exit(2)
	}
	mux := http.NewServeMux()
	mux.Handle("/", h)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	logger.Info("listening", "service", os.Args[1], "addr", addr)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		logger.Error("server failed", "err", err)
		os.Exit(1)
	}
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

type product struct {
	ID    int     `json:"id"`
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}

var products = []product{{1, "kettle", 24.5}, {2, "teapot", 18}, {3, "mug", 6.5}, {4, "tea 250g", 9}, {5, "infuser", 4.25}}

// catalog serves GET /products and GET /products/{id}. It keeps an in-memory cache that
// grows with CACHE_MB (used by an OOM scenario).
func catalog() http.Handler {
	if mb, _ := strconv.Atoi(os.Getenv("CACHE_MB")); mb > 0 {
		cache := make([]byte, mb<<20)
		for i := range cache {
			cache[i] = byte(i)
		}
		logger.Info("cache warmed", "mb", mb, "bytes", len(cache))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /products", func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(products) })
	mux.HandleFunc("GET /products/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.PathValue("id"))
		for _, p := range products {
			if p.ID == id {
				_ = json.NewEncoder(w).Encode(p)
				return
			}
		}
		http.Error(w, "no such product", http.StatusNotFound)
	})
	return mux
}

// orders serves POST /orders: looks the product up in catalog (CATALOG_URL) and stores the
// order in Postgres (DB_HOST, DB_PASSWORD).
func orders() http.Handler {
	catalogURL := env("CATALOG_URL", "http://catalog:8080")
	dsn := fmt.Sprintf("postgres://shop:%s@%s:5432/shop?sslmode=disable&connect_timeout=3", os.Getenv("DB_PASSWORD"), env("DB_HOST", "postgres"))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var pool *pgxpool.Pool
	for {
		p, err := pgxpool.New(ctx, dsn)
		if err == nil {
			if err = p.Ping(ctx); err == nil {
				pool = p
				break
			}
			p.Close()
		}
		logger.Error("database unavailable", "err", err)
		if ctx.Err() != nil {
			logger.Error("giving up: cannot connect to the database", "host", env("DB_HOST", "postgres"))
			os.Exit(1)
		}
		time.Sleep(2 * time.Second)
	}
	if _, err := pool.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS orders (id bigserial PRIMARY KEY, product int NOT NULL, qty int NOT NULL, at timestamptz NOT NULL DEFAULT now())`); err != nil {
		logger.Error("migration failed", "err", err)
		os.Exit(1)
	}
	hc := &http.Client{Timeout: 3 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		id := 1 + rand.IntN(len(products))
		resp, err := hc.Get(fmt.Sprintf("%s/products/%d", catalogURL, id))
		if err != nil {
			logger.Error("catalog request failed", "err", err, "url", catalogURL)
			http.Error(w, "catalog unavailable", http.StatusBadGateway)
			return
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			logger.Error("catalog returned an error", "status", resp.StatusCode)
			http.Error(w, "catalog error", http.StatusBadGateway)
			return
		}
		var oid int64
		if err := pool.QueryRow(r.Context(), `INSERT INTO orders(product, qty) VALUES ($1, 1) RETURNING id`, id).Scan(&oid); err != nil {
			logger.Error("insert order failed", "err", err)
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]int64{"order": oid})
	})
	mux.HandleFunc("GET /orders/count", func(w http.ResponseWriter, r *http.Request) {
		var n int64
		if err := pool.QueryRow(r.Context(), `SELECT count(*) FROM orders`).Scan(&n); err != nil {
			logger.Error("count failed", "err", err)
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]int64{"orders": n})
	})
	return mux
}
