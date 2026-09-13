package main

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// buildClient returns a Kubernetes clientset. Resolution order:
//  1. explicit --kubeconfig path
//  2. in-cluster ServiceAccount config (when running as a pod)
//  3. the standard kubeconfig loading rules (KUBECONFIG, ~/.kube/config)
func buildClient(kubeconfig string) (kubernetes.Interface, error) {
	var (
		cfg *rest.Config
		err error
	)
	switch {
	case kubeconfig != "":
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("loading kubeconfig %s: %w", kubeconfig, err)
		}
	default:
		cfg, err = rest.InClusterConfig()
		if err != nil {
			loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
			cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
				loadingRules, &clientcmd.ConfigOverrides{}).ClientConfig()
			if err != nil {
				return nil, fmt.Errorf("no in-cluster config and no kubeconfig found: %w", err)
			}
		}
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building Kubernetes client: %w", err)
	}
	return clientset, nil
}
