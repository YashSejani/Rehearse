package gate

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// Handler is the gate's HTTP API:
//
//	POST /actions                    propose -> proposal "checking"; dry-run, policy and
//	                                 rehearsal run in the background (poll GET /proposals/{id})
//	GET  /proposals                  list (newest first)
//	GET  /proposals/{id}             one proposal with its audit events
//	POST /proposals/{id}/approve     {"by": "yash"}   apply + watch (+ undo)
//	POST /proposals/{id}/reject      {"by": "yash", "reason": "..."}
//	GET  /healthz
//
// Every route except /healthz needs "X-Rehearse-Token: <token>" (or "Authorization: Bearer <token>").
func (g *Gate) Handler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("POST /actions", func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			httpError(w, http.StatusBadRequest, "bad request: "+err.Error())
			return
		}
		p, err := g.ProposeAsync(r.Context(), req)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, p)
	})
	mux.HandleFunc("GET /proposals", func(w http.ResponseWriter, r *http.Request) {
		ps, err := g.Store.List(r.Context(), 100)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, ps)
	})
	mux.HandleFunc("GET /proposals/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		p, err := g.Store.Get(r.Context(), id)
		if err != nil {
			httpError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, p)
	})
	decision := func(approve bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			id, ok := pathID(w, r)
			if !ok {
				return
			}
			var body struct {
				By     string `json:"by"`
				Reason string `json:"reason"`
				Wait   bool   `json:"wait"`
			}
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body)
			var p *Proposal
			var err error
			if approve {
				// The watch outlives this request; it must not be cancelled with it.
				p, err = g.Approve(context.WithoutCancel(r.Context()), id, body.By, body.Wait)
			} else {
				p, err = g.Reject(r.Context(), id, body.By, body.Reason)
			}
			if err != nil {
				httpError(w, http.StatusConflict, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, p)
		}
	}
	mux.HandleFunc("POST /proposals/{id}/approve", decision(true))
	mux.HandleFunc("POST /proposals/{id}/reject", decision(false))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			// X-Rehearse-Token works through the Kubernetes API proxy, which keeps
			// Authorization for itself.
			got := r.Header.Get("X-Rehearse-Token")
			if got == "" {
				got = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			}
			if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				httpError(w, http.StatusUnauthorized, "missing or wrong token")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad id")
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
