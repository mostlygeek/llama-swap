package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/perf"
	"github.com/mostlygeek/llama-swap/internal/shared"
	"github.com/mostlygeek/llama-swap/internal/store"
)

// apiModel is one entry in the /api/events modelStatus payload.
type apiModel struct {
	Id           string         `json:"id"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	State        string         `json:"state"`
	Unlisted     bool           `json:"unlisted"`
	PeerID       string         `json:"peerID"`
	Aliases      []string       `json:"aliases,omitempty"`
	Capabilities map[string]any `json:"capabilities,omitempty"`
}

// modelStatus returns every configured model joined with its current process
// state (defaulting to "stopped"), followed by peer models.
func (s *Server) modelStatus() []apiModel {
	running := s.local.RunningModels()

	ids := make([]string, 0, len(s.cfg.Models))
	for id := range s.cfg.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	models := make([]apiModel, 0, len(ids))
	for _, id := range ids {
		mc := s.cfg.Models[id]
		state := "stopped"
		if st, ok := running[id]; ok {
			state = string(st)
		}
		_, capsMap, _, _ := renderCapabilities(mc.Capabilities)
		models = append(models, apiModel{
			Id:           id,
			Name:         mc.Name,
			Description:  mc.Description,
			State:        state,
			Unlisted:     mc.Unlisted,
			Aliases:      mc.Aliases,
			Capabilities: capsMap,
		})
	}

	for peerID, peer := range s.cfg.Peers {
		for _, modelID := range peer.Models {
			models = append(models, apiModel{Id: modelID, PeerID: peerID})
		}
	}

	return models
}

// handleAPIUnloadAll stops every running local process.
func (s *Server) handleAPIUnloadAll(w http.ResponseWriter, r *http.Request) {
	s.local.Unload(0)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"msg": "ok"})
}

// handleAPIUnloadModel stops a single named local process.
func (s *Server) handleAPIUnloadModel(w http.ResponseWriter, r *http.Request) {
	requested := strings.TrimPrefix(r.PathValue("model"), "/")
	realName, found := s.cfg.RealModelName(requested)
	if !found {
		shared.SendResponse(w, r, http.StatusNotFound, "model not found")
		return
	}
	if !s.local.Handles(realName) {
		shared.SendResponse(w, r, http.StatusNotFound, "no local server found for requested model")
		return
	}
	s.local.Unload(0, realName)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// handleAPIActivity serves paginated activity table rows.
func (s *Server) handleAPIActivity(w http.ResponseWriter, r *http.Request) {
	query, err := parseActivityQuery(r)
	if err != nil {
		shared.SendResponse(w, r, http.StatusBadRequest, err.Error())
		return
	}
	page, err := s.store.ListActivity(r.Context(), query)
	if err != nil {
		shared.SendResponse(w, r, http.StatusInternalServerError, "failed to get activity")
		return
	}
	s.metrics.overlayCaptureState(page.Data)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(page)
}

// handleAPIActivityStats serves aggregate activity statistics and histograms.
func (s *Server) handleAPIActivityStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.ActivityStats(r.Context(), store.ActivityStatsQuery{
		Model: strings.TrimSpace(r.URL.Query().Get("model")),
	})
	if err != nil {
		shared.SendResponse(w, r, http.StatusInternalServerError, "failed to get activity stats")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func parseActivityLimit(raw string) (int, error) {
	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid limit")
	}
	if limit > 0 && limit < 1000 {
		return limit, nil
	}
	return 0, fmt.Errorf("limit must be between 1 and 999")
}

func parseActivityQuery(r *http.Request) (store.ActivityQuery, error) {
	const defaultLimit = 25
	query := store.ActivityQuery{
		Model: strings.TrimSpace(r.URL.Query().Get("model")),
		Limit: defaultLimit,
		Page:  1,
	}

	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := parseActivityLimit(raw)
		if err != nil {
			return store.ActivityQuery{}, err
		}
		query.Limit = limit
	}

	if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
		page, err := strconv.Atoi(raw)
		if err != nil || page < 1 {
			return store.ActivityQuery{}, fmt.Errorf("page must be >= 1")
		}
		query.Page = page
	}

	if raw := strings.TrimSpace(r.URL.Query().Get("sort")); raw != "" {
		if _, ok := store.ActivitySortColumn(raw); !ok {
			return store.ActivityQuery{}, fmt.Errorf("invalid sort column")
		}
		query.Sort = raw
	}

	if raw := strings.TrimSpace(r.URL.Query().Get("order")); raw != "" {
		switch strings.ToLower(raw) {
		case "asc", "desc":
			query.Order = strings.ToLower(raw)
		default:
			return store.ActivityQuery{}, fmt.Errorf("order must be asc or desc")
		}
	}

	return query, nil
}

// handleAPIPerformance serves the buffered system/GPU stats, optionally
// filtered to samples after the ?after=<RFC3339> timestamp.
func (s *Server) handleAPIPerformance(w http.ResponseWriter, r *http.Request) {
	if s.perf == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]bool{"enabled": false})
		return
	}

	sysStats, gpuStats := s.perf.Current()

	if afterStr := r.URL.Query().Get("after"); afterStr != "" {
		after, err := time.Parse(time.RFC3339, afterStr)
		if err != nil {
			shared.SendResponse(w, r, http.StatusBadRequest, "invalid 'after' timestamp, use RFC3339 format")
			return
		}
		filteredSys := make([]perf.SysStat, 0, len(sysStats))
		for _, st := range sysStats {
			if st.Timestamp.After(after) {
				filteredSys = append(filteredSys, st)
			}
		}
		sysStats = filteredSys

		filteredGpu := make([]perf.GpuStat, 0, len(gpuStats))
		for _, g := range gpuStats {
			if g.Timestamp.After(after) {
				filteredGpu = append(filteredGpu, g)
			}
		}
		gpuStats = filteredGpu
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"enabled":   true,
		"sys_stats": sysStats,
		"gpu_stats": gpuStats,
	})
}

// handleAPIVersion serves the build metadata.
func (s *Server) handleAPIVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"version":    s.build.Version,
		"commit":     s.build.Commit,
		"build_date": s.build.Date,
	})
}

// handleAPICapture returns the stored request/response capture for a metric ID.
func (s *Server) handleAPICapture(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.SendResponse(w, r, http.StatusBadRequest, "invalid capture ID")
		return
	}

	capture := s.metrics.getCaptureByID(id)
	if capture == nil {
		shared.SendResponse(w, r, http.StatusNotFound, "capture not found")
		return
	}

	jsonBytes, err := json.Marshal(capture)
	if err != nil {
		shared.SendResponse(w, r, http.StatusInternalServerError, "failed to marshal capture")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonBytes)
}

// handleAPICancelInflight cancels an active model-dispatched request by its
// inflight ID. Normal request cleanup removes the row and emits the update.
func (s *Server) handleAPICancelInflight(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || !s.inflight.Cancel(id) {
		shared.SendResponse(w, r, http.StatusNotFound, "inflight request not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"msg": "ok"})
}

type messageType string

const (
	msgTypeModelStatus messageType = "modelStatus"
	msgTypeLogData     messageType = "logData"
	msgTypeActivity    messageType = "activity"
	msgTypeInFlight    messageType = "inflight"
	msgTypeUIConfig    messageType = "uiConfig"
)

type messageEnvelope struct {
	Type messageType `json:"type"`
	Data string      `json:"data"`
}

// handleAPIEvents streams server events (model status, log data, metrics,
// in-flight counts) to the client as Server-Sent Events.
func (s *Server) handleAPIEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// prevent nginx from buffering SSE
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		shared.SendResponse(w, r, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	// internal/event already has a 50K event buffer
	// a 1K message buffer should be enough, watch the logs for the warning that the sendBuffer is full
	sendBuffer := make(chan messageEnvelope, 1024)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	send := func(msg messageEnvelope) {
		select {
		case sendBuffer <- msg:
		case <-ctx.Done():
			s.proxylog.Warn("handleAPIEvents send suppressed due to context done")
		default:
			s.proxylog.Warn("handleAPIEvents sendBuffer full, dropped message")
		}
	}
	sendModels := func() {
		if data, err := json.Marshal(s.modelStatus()); err == nil {
			send(messageEnvelope{Type: msgTypeModelStatus, Data: string(data)})
		}
	}
	sendLogData := func(source string, data []byte) {
		if j, err := json.Marshal(map[string]string{"source": source, "data": string(data)}); err == nil {
			send(messageEnvelope{Type: msgTypeLogData, Data: string(j)})
		}
	}
	sendActivity := func(id int) {
		if j, err := json.Marshal(map[string]int{"id": id}); err == nil {
			send(messageEnvelope{Type: msgTypeActivity, Data: string(j)})
		}
	}
	sendInFlight := func(update shared.InFlightRequestsEvent) {
		if update.Operation == inflightOperationSnapshot && update.Requests == nil {
			update.Requests = []shared.InflightRequestEntry{}
		}
		if j, err := json.Marshal(update); err == nil {
			send(messageEnvelope{Type: msgTypeInFlight, Data: string(j)})
		}
	}
	sendUIConfig := func() {
		if j, err := json.Marshal(s.cfg.UI); err == nil {
			send(messageEnvelope{Type: msgTypeUIConfig, Data: string(j)})
		}
	}

	defer event.On(func(e shared.ProcessStateChangeEvent) { sendModels() })()
	defer event.On(func(e shared.ConfigFileChangedEvent) { sendModels() })()
	defer s.proxylog.OnLogData(func(data []byte) { sendLogData("proxy", data) })()
	defer s.upstreamlog.OnLogData(func(data []byte) { sendLogData("upstream", data) })()
	defer event.On(func(e ActivityLogEvent) { sendActivity(e.Metrics.ID) })()
	defer event.On(func(e shared.InFlightRequestsEvent) { sendInFlight(e) })()

	// initial payload
	sendLogData("proxy", s.proxylog.GetHistory())
	sendLogData("upstream", s.upstreamlog.GetHistory())
	sendModels()
	sendUIConfig()
	sendInFlight(s.inflight.Current())

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.shutdownCtx.Done():
			return
		case msg := <-sendBuffer:
			data, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event:message\ndata:%s\n\n", data)
			flusher.Flush()
		}
	}
}
