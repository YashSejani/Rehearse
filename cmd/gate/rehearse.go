package main

import (
	"log"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/YashSejani/Rehearse/internal/config"
	"github.com/YashSejani/Rehearse/internal/gate"
	"github.com/YashSejani/Rehearse/internal/gate/sandbox"
)

// newRehearser returns the sandbox rehearser (Phase 7), or nil when sandboxing is off.
func newRehearser(cfg *config.Config, cs kubernetes.Interface) gate.Rehearser {
	sb := cfg.Gate.Sandbox
	if !sb.Enabled {
		return nil
	}
	return &sandbox.Sandbox{
		Kube:     cs,
		SourceNS: cfg.App.Namespace,
		GateSA:   "rehearse/rehearse-gate",
		Timeout:  time.Duration(sb.Timeout) * time.Second,
		Smoke:    sandbox.Smoke{Service: sb.SmokeService, Port: sb.SmokePort, Path: sb.SmokePath},
		Log:      log.Printf,
	}
}
