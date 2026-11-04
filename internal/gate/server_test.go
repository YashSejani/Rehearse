package gate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPFlow(t *testing.T) {
	kube := newKube(t, nil, deployment(0, "app:v1"))
	g := newGate(kube, Health{Available: true})
	srv := httptest.NewServer(g.Handler("secret"))
	defer srv.Close()

	do := func(method, path, token string, body any) (*http.Response, map[string]any) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(b))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}

	if r, _ := do("POST", "/actions", "", Request{}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: %d", r.StatusCode)
	}
	if r, _ := do("POST", "/actions", "wrong", Request{}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", r.StatusCode)
	}
	r, p := do("POST", "/actions", "secret", Request{Namespace: "hotel", Action: scaleTo(1)})
	if r.StatusCode != http.StatusCreated || p["status"] != StatusChecking {
		t.Fatalf("propose must return at once with status checking: %d %v", r.StatusCode, p)
	}
	id := int(p["id"].(float64))
	g.Wait() // background checks done
	if _, p = do("GET", fmt.Sprintf("/proposals/%d", id), "secret", nil); p["status"] != StatusAwaiting {
		t.Fatalf("after checks: %v", p)
	}
	r, p = do("POST", fmt.Sprintf("/proposals/%d/approve", id), "secret", map[string]any{"by": "yash", "wait": true})
	if r.StatusCode != http.StatusOK || p["status"] != StatusApplied {
		t.Fatalf("approve: %d %v", r.StatusCode, p)
	}
	if r, _ = do("POST", fmt.Sprintf("/proposals/%d/approve", id), "secret", map[string]any{"by": "yash"}); r.StatusCode != http.StatusConflict {
		t.Errorf("second approval must fail: %d", r.StatusCode)
	}
	r, p = do("GET", fmt.Sprintf("/proposals/%d", id), "secret", nil)
	if r.StatusCode != http.StatusOK || len(p["events"].([]any)) != 7 {
		t.Errorf("get with events: %d %v", r.StatusCode, p["events"])
	}
	if r, _ := do("GET", "/healthz", "", nil); r.StatusCode != http.StatusOK {
		t.Errorf("healthz: %d", r.StatusCode)
	}
}
