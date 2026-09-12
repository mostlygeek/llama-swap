package main

import (
	"fmt"
	"strings"
)

// sanitizeModelID translates a model ID into a DNS-1123 label: lowercase
// alphanumerics and dashes, max 63 chars. The original ID is preserved in
// the llama-swap.io/model-id annotation on every created object.
func sanitizeModelID(id string) (string, error) {
	var b strings.Builder
	prevDash := false
	for _, r := range id {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r - 'A' + 'a')
			prevDash = false
		default:
			if b.Len() > 0 && !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-")
	}
	if s == "" {
		return "", fmt.Errorf("model ID %q has no usable characters for a Kubernetes name", id)
	}
	return s, nil
}

// deploymentName is the name of the model's Deployment.
func deploymentName(sanitized string) string { return sanitized }

// serviceName is the name of the model's Service. A suffix is appended so the
// name cannot collide with a deployment of another model whose ID is a
// prefix match; the suffix is trimmed to stay within the 63-char limit.
func serviceName(sanitized string) string {
	const suffix = "-svc"
	if len(sanitized)+len(suffix) > 63 {
		return sanitized[:63-len(suffix)] + suffix
	}
	return sanitized + suffix
}

// managedLabels are the labels every object kubeswap creates carries. The
// sanitized model ID (not the original) is used so labels stay valid.
func managedLabels(sanitized string) map[string]string {
	return map[string]string{
		labelManagedBy: managedByValue,
		labelModel:     sanitized,
	}
}

// podLabels selects the model's pods. They are a superset of
// managedLabels so a Service selector matches the pod template labels.
func podLabels(sanitized string) map[string]string {
	labels := map[string]string{
		labelAppName: appNameValue,
	}
	for k, v := range managedLabels(sanitized) {
		labels[k] = v
	}
	return labels
}
