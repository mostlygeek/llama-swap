package server

import "time"

// halogenExtractor reads halogen responses. Halogen sends the API usage
// block plus a llama-server style timings block (prompt_n, predicted_n,
// rates, cache_n, draft_n / draft_n_accepted), so it parses the same way
// as llama-server.
type halogenExtractor struct{}

func (halogenExtractor) newParser(path string, start time.Time) usageParser {
	return llamaServerExtractor{}.newParser(path, start)
}
