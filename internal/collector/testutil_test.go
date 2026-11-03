package collector

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/YashSejani/Rehearse/internal/config"
)

type runtimeObject = runtime.Object

var testNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func testConfig() *config.Config {
	c := config.Default()
	c.App.Namespace = "hotel"
	c.App.ServiceLabel = "io.kompose.service"
	c.App.Endpoints = map[string][]string{"user": {"user", "reservation"}}
	return c
}

// fakeBackend answers Prometheus/Loki requests: the first rule whose key is a substring
// of the query returns its JSON body. It records every query it saw.
type fakeBackend struct {
	rules   []rule
	queries []string
}

type rule struct{ match, body string }

func (f *fakeBackend) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		f.queries = append(f.queries, q)
		for _, rl := range f.rules {
			if strings.Contains(q, rl.match) {
				_, _ = w.Write([]byte(rl.body))
				return
			}
		}
		if strings.Contains(r.URL.Path, "loki") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}
}

// newTestCollector wires a fake clientset and one httptest server used as both backends.
func newTestCollector(t *testing.T, fb *fakeBackend, objs ...runtimeObject) *Collector {
	t.Helper()
	srv := httptest.NewServer(fb.handler(t))
	t.Cleanup(srv.Close)
	g := &URLGetter{Base: srv.URL, Client: srv.Client()}
	return &Collector{
		Kube: fake.NewClientset(objs...),
		Prom: &Prom{G: g},
		Loki: &Loki{G: g},
		Cfg:  testConfig(),
		Now:  func() time.Time { return testNow },
	}
}
