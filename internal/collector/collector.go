package collector

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/YashSejani/Rehearse/internal/config"
)

// Collector bundles the read-only clients and settings every tool needs.
type Collector struct {
	Kube kubernetes.Interface
	Prom *Prom
	Loki *Loki
	Cfg  *config.Config
	Now  func() time.Time // replaceable in tests
}

// New builds a Collector from config and a Kubernetes client.
func New(cfg *config.Config, kube kubernetes.Interface) (*Collector, error) {
	pg, err := NewGetter(cfg.Prometheus, kube)
	if err != nil {
		return nil, fmt.Errorf("prometheus backend: %w", err)
	}
	lg, err := NewGetter(cfg.Loki.Backend, kube)
	if err != nil {
		return nil, fmt.Errorf("loki backend: %w", err)
	}
	return &Collector{Kube: kube, Prom: &Prom{G: pg}, Loki: &Loki{G: lg}, Cfg: cfg, Now: time.Now}, nil
}

func (c *Collector) ns() string { return c.Cfg.App.Namespace }

// withTimeout gives every external call its own deadline (rule: no call may hang forever).
func (c *Collector) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.Cfg.Limits.Timeout())
}

// serviceOf returns the service name of an object from its labels (io.kompose.service=geo → geo).
func (c *Collector) serviceOf(labels map[string]string) string {
	return labels[c.Cfg.App.ServiceLabel]
}
