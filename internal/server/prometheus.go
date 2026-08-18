package server

import (
	"fmt"
	"net/http"
	"strings"
)

// writeModelTokenMetrics emits Prometheus counters for request and token
// counts labeled per model. Values are summed across all activity rows
// currently retained by the store, so they drop whenever activity is pruned
// (see Store.PruneActivity) rather than only accumulating for the life of
// the process, unlike a typical Prometheus counter.
func (s *Server) writeModelTokenMetrics(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		return
	}
	totals, err := s.store.TokenTotalsByModel(r.Context())
	if err != nil || len(totals) == 0 {
		return
	}

	fmt.Fprintf(w, "# HELP llamaswap_model_requests_total Total requests recorded per model\n")
	fmt.Fprintf(w, "# TYPE llamaswap_model_requests_total counter\n")
	for _, t := range totals {
		fmt.Fprintf(w, "llamaswap_model_requests_total{model=\"%s\"} %d\n", sanitizeMetricLabel(t.Model), t.Requests)
	}

	fmt.Fprintf(w, "# HELP llamaswap_model_input_tokens_total Total input (prompt) tokens recorded per model\n")
	fmt.Fprintf(w, "# TYPE llamaswap_model_input_tokens_total counter\n")
	for _, t := range totals {
		fmt.Fprintf(w, "llamaswap_model_input_tokens_total{model=\"%s\"} %d\n", sanitizeMetricLabel(t.Model), t.InputTokens)
	}

	fmt.Fprintf(w, "# HELP llamaswap_model_output_tokens_total Total output (generated) tokens recorded per model\n")
	fmt.Fprintf(w, "# TYPE llamaswap_model_output_tokens_total counter\n")
	for _, t := range totals {
		fmt.Fprintf(w, "llamaswap_model_output_tokens_total{model=\"%s\"} %d\n", sanitizeMetricLabel(t.Model), t.OutputTokens)
	}

	fmt.Fprintf(w, "# HELP llamaswap_model_cache_tokens_total Total cached prompt tokens recorded per model\n")
	fmt.Fprintf(w, "# TYPE llamaswap_model_cache_tokens_total counter\n")
	for _, t := range totals {
		fmt.Fprintf(w, "llamaswap_model_cache_tokens_total{model=\"%s\"} %d\n", sanitizeMetricLabel(t.Model), t.CacheTokens)
	}
}

// sanitizeMetricLabel escapes characters that are invalid in Prometheus label values.
func sanitizeMetricLabel(s string) string {
	return strings.NewReplacer(`"`, `\"`, `\`, `\\`, "\n", `\n`).Replace(s)
}
