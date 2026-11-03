// Package kube builds Kubernetes clients from the user's kubeconfig.
package kube

import (
	"fmt"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// RESTConfig loads ~/.kube/config (or $KUBECONFIG) for the given context ("" = current),
// or the in-cluster ServiceAccount when running inside a pod.
func RESTConfig(context string) (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{CurrentContext: context}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("kubeconfig (context %q): %w", context, err)
	}
	cfg.Timeout = 30 * time.Second
	return cfg, nil
}

// Client returns a typed clientset.
func Client(context string) (kubernetes.Interface, *rest.Config, error) {
	cfg, err := RESTConfig(context)
	if err != nil {
		return nil, nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("kubernetes client: %w", err)
	}
	return cs, cfg, nil
}
