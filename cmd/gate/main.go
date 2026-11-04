// Command gate is the Rehearse Safety Gate service: the only component allowed to change
// the cluster. It runs with its own ServiceAccount (deploy/rehearse/gate.yaml).
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/YashSejani/Rehearse/internal/collector"
	"github.com/YashSejani/Rehearse/internal/config"
	"github.com/YashSejani/Rehearse/internal/gate"
	"github.com/YashSejani/Rehearse/internal/knowledge"
	"github.com/YashSejani/Rehearse/internal/kube"
)

func main() {
	cfgPath := flag.String("config", "/etc/rehearse/config.yaml", "config file")
	listen := flag.String("listen", ":8080", "HTTP listen address")
	flag.Parse()
	if err := run(*cfgPath, *listen); err != nil {
		log.Fatal(err)
	}
}

func run(cfgPath, listen string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	token := os.Getenv("REHEARSE_GATE_TOKEN")
	if token == "" {
		log.Fatal("REHEARSE_GATE_TOKEN is not set")
	}
	cs, _, err := kube.Client(cfg.Kube.Context)
	if err != nil {
		return err
	}
	col, err := collector.New(cfg, cs)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	g := &gate.Gate{
		Kube:              cs,
		Store:             gate.NewMemStore(),
		Prober:            gate.CollectorProber{C: col},
		Watch:             gate.DefaultWatch(),
		AllowedNamespaces: []string{cfg.App.Namespace},
	}
	if dsn := os.Getenv("REHEARSE_DB_DSN"); dsn != "" {
		st, err := knowledge.Open(ctx, dsn)
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.Migrate(ctx); err != nil {
			return err
		}
		g.Store = &gate.PGStore{Pool: st.Pool}
		g.Baseline = func(ctx context.Context, ns, dep string) float64 {
			m, err := st.LoadMap(ctx, ns)
			if err != nil {
				return 0
			}
			return baselineErrorRatio(m, dep)
		}
		log.Print("audit log: postgres")
	} else {
		log.Print("audit log: in memory (REHEARSE_DB_DSN not set)")
	}
	if r := newRehearser(cfg, cs); r != nil {
		g.Rehearser = r
	}

	if err := g.RecoverInterrupted(ctx); err != nil {
		log.Printf("recover interrupted proposals: %v", err)
	}

	srv := &http.Server{Addr: listen, Handler: g.Handler(token), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	log.Printf("gate listening on %s, managing namespace %q", listen, cfg.App.Namespace)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	g.Wait() // let running watches finish (they may need to undo)
	return nil
}

// baselineErrorRatio is the worst normal error ratio over the endpoints the deployment serves.
func baselineErrorRatio(m *knowledge.ServiceMap, dep string) float64 {
	worst := 0.0
	for _, b := range m.Baselines {
		if b.Service == dep && len(b.Metric) > 12 && b.Metric[len(b.Metric)-12:] == ":error_ratio" && b.Value > worst {
			worst = b.Value
		}
	}
	return worst
}
