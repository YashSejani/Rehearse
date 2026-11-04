package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCatalog(t *testing.T) {
	srv := httptest.NewServer(catalog())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/products/3")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("get product: %v %v", err, resp)
	}
	var p product
	_ = json.NewDecoder(resp.Body).Decode(&p)
	if p.Name != "mug" {
		t.Errorf("product %+v", p)
	}
	if r, _ := http.Get(srv.URL + "/products/99"); r.StatusCode != http.StatusNotFound {
		t.Errorf("missing product: %d", r.StatusCode)
	}
}
