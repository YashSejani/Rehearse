package kube

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// PortForward opens a tunnel from a random local port to port `port` of one ready pod
// behind Service ns/svc, like `kubectl port-forward svc/<svc>`. It returns the local
// address ("127.0.0.1:12345") and a stop function. Used for TCP backends (Postgres) that
// the HTTP service proxy cannot reach.
func PortForward(ctx context.Context, cs kubernetes.Interface, cfg *rest.Config, ns, svc string, port int) (string, func(), error) {
	s, err := cs.CoreV1().Services(ns).Get(ctx, svc, metav1.GetOptions{})
	if err != nil {
		return "", nil, fmt.Errorf("service %s/%s: %w", ns, svc, err)
	}
	pods, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(s.Spec.Selector).String()})
	if err != nil {
		return "", nil, fmt.Errorf("pods of %s: %w", svc, err)
	}
	pod := ""
	for _, p := range pods.Items {
		for _, c := range p.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				pod = p.Name
			}
		}
	}
	if pod == "" {
		return "", nil, fmt.Errorf("no ready pod behind service %s/%s", ns, svc)
	}

	transport, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return "", nil, fmt.Errorf("spdy: %w", err)
	}
	u, err := url.Parse(strings.TrimRight(cfg.Host, "/") + fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", ns, pod))
	if err != nil {
		return "", nil, err
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, u)
	stop, ready := make(chan struct{}), make(chan struct{})
	fw, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"}, []string{fmt.Sprintf("0:%d", port)}, stop, ready, io.Discard, io.Discard)
	if err != nil {
		return "", nil, fmt.Errorf("port-forward: %w", err)
	}
	errc := make(chan error, 1)
	go func() { errc <- fw.ForwardPorts() }()
	select {
	case <-ready:
	case err := <-errc:
		return "", nil, fmt.Errorf("port-forward: %w", err)
	case <-ctx.Done():
		close(stop)
		return "", nil, ctx.Err()
	}
	ports, err := fw.GetPorts()
	if err != nil || len(ports) == 0 {
		close(stop)
		return "", nil, fmt.Errorf("port-forward: no local port: %v", err)
	}
	once := false
	return fmt.Sprintf("127.0.0.1:%d", ports[0].Local), func() {
		if !once {
			once = true
			close(stop)
		}
	}, nil
}
