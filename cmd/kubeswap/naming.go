package main

import (
	"crypto/sha256"
	"encoding/hex"
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

// idHash is a short fingerprint of the ORIGINAL model ID. Distinct IDs can
// sanitize to the same string (Model_A and model-a) or share a long prefix,
// so object names carry a hash of the original ID to stay collision-free.
func idHash(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:4])
}

// deploymentName is the name of the model's Deployment: the sanitized ID
// (up to 50 chars) plus "-" plus the original-ID hash. The result is
// deterministic and at most 59 chars.
func deploymentName(modelID string) (string, error) {
	s, err := sanitizeModelID(modelID)
	if err != nil {
		return "", err
	}
	if len(s) > 50 {
		s = strings.TrimRight(s[:50], "-")
	}
	return s + "-" + idHash(modelID), nil
}

// serviceName is the name of the model's Service: the deployment name plus
// "-svc" (at most 63 chars).
func serviceName(modelID string) (string, error) {
	dep, err := deploymentName(modelID)
	if err != nil {
		return "", err
	}
	return dep + "-svc", nil
}

// managedLabels are the coarse ownership labels objects kubeswap creates
// carry. The sanitized model ID (not the original) is used so labels stay
// valid. NOTE: the sanitized ID is NOT unique across models (Model_A and
// model-a share it), so pod selection must use podSelectorLabels.
func managedLabels(sanitized string) map[string]string {
	return map[string]string{
		labelManagedBy: managedByValue,
		labelModel:     sanitized,
	}
}

// podSelectorLabels uniquely identify one model's pods: the deployment
// name carries the original-ID hash, so distinct model IDs never share a
// selector. Used for the Deployment/Service selectors and for finding
// pods (proxy upstream, readiness, logs, delete waits).
func podSelectorLabels(sanitized, depName string) map[string]string {
	labels := managedLabels(sanitized)
	labels[labelDeployment] = depName
	return labels
}

// podLabels are the pod template labels: a superset of podSelectorLabels
// so the Deployment and Service selectors match them.
func podLabels(sanitized, depName string) map[string]string {
	labels := map[string]string{
		labelAppName: appNameValue,
	}
	for k, v := range podSelectorLabels(sanitized, depName) {
		labels[k] = v
	}
	return labels
}
