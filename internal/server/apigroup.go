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

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/perf"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/store"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// apiModel is one entry in the /api/events modelStatus payload.
type apiModel struct {
	Id            string         `json:"id"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	State         string         `json:"state"`
	Unlisted      bool           `json:"unlisted"`
	PeerID        string         `json:"peerID"`
	Aliases       []string       `json:"aliases,omitempty"`
	Capabilities  map[string]any `json:"capabilities,omitempty"`
	ContextLength int            `json:"context_length,omitempty"`
	// ReadySince is when the model last became ready (RFC 3339). Only set
	// while the model is ready.
	ReadySince string `json:"readySince,omitempty"`
	// UptimeMs is how long the model has been ready as of this payload. The
	// UI counts from it rather than from ReadySince, so a browser clock that
	// differs from the server's doesn't skew the uptime.
	UptimeMs int64 `json:"uptimeMs,omitempty"`
}

type apiProfile struct {
	ID          string            `json:"id"`
	Description string            `json:"description"`
	Pins        map[string]string `json:"pins"`
}

func nullableProfile(name string) any {
	if name == "" {
		return nil
	}
	return name
}

func (s *Server) handleAPIProfiles(w http.ResponseWriter, r *http.Request) {
	ids := make([]string, 0, len(s.cfg.Profiles))
	for id := range s.cfg.Profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	profiles := make([]apiProfile, 0, len(ids))
	for _, id := range ids {
		profile := s.cfg.Profiles[id]
		profiles = append(profiles, apiProfile{
			ID:          id,
			Description: profile.Description,
			Pins:        profile.Pins,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"active":   nullableProfile(s.ActiveProfile()),
		"profiles": profiles,
	})
}

func (s *Server) handleAPIActiveProfile(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&body); err != nil {
		swaputil.SendResponse(w, r, http.StatusBadRequest, "invalid profile request")
		return
	}
	raw, ok := body["name"]
	if !ok {
		swaputil.SendResponse(w, r, http.StatusBadRequest, "profile name is required")
		return
	}

	var name string
	if string(raw) != "null" {
		if err := json.Unmarshal(raw, &name); err != nil {
			swaputil.SendResponse(w, r, http.StatusBadRequest, "profile name must be a string or null")
			return
		}
	}
	if _, err := s.setActiveProfile(name); err != nil {
		swaputil.SendResponse(w, r, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"active": nullableProfile(s.ActiveProfile())})
}

// modelStatus returns every configured model joined with its current process
// state (defaulting to "stopped"), followed by peer models.
func (s *Server) modelStatus() []apiModel {
	running := s.local.RunningStatus()

	ids := make([]string, 0, len(s.cfg.Models))
	for id := range s.cfg.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	models := make([]apiModel, 0, len(ids))
	for _, id := range ids {
		mc := s.cfg.Models[id]
		state := "stopped"
		var readySince string
		var uptimeMs int64
		if st, ok := running[id]; ok {
			state = string(st.State)
			if st.State == process.StateReady && !st.ReadySince.IsZero() {
				readySince = st.ReadySince.UTC().Format(time.RFC3339)
				// At least 1 so omitempty keeps it for a model that just became ready.
				uptimeMs = max(1, time.Since(st.ReadySince).Milliseconds())
			}
		}
		// Same resolution /v1/models uses, so the dashboard and the OpenAI
		// listing never disagree about what a model can do. Bound by
		// shutdownCtx rather than a request: modelStatus is also called from
		// event callbacks that have no request of their own.
		caps := s.resolveCapabilities(s.shutdownCtx, id, mc)
		_, capsMap, _, ctxLen := renderCapabilities(caps)
		models = append(models, apiModel{
			Id:            id,
			Name:          mc.Name,
			Description:   mc.Description,
			State:         state,
			Unlisted:      mc.Unlisted,
			Aliases:       mc.Aliases,
			Capabilities:  capsMap,
			ContextLength: ctxLen,
			ReadySince:    readySince,
			UptimeMs:      uptimeMs,
		})
	}

	for peerID, peer := range s.cfg.Peers {
		for _, modelID := range peer.Models {
			models = append(models, apiModel{Id: config.PeerModelFQN(peerID, modelID), PeerID: peerID})
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
		swaputil.SendResponse(w, r, http.StatusNotFound, "model not found")
		return
	}
	if !s.local.Handles(realName) {
		swaputil.SendResponse(w, r, http.StatusNotFound, "no local server found for requested model")
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
		swaputil.SendResponse(w, r, http.StatusBadRequest, err.Error())
		return
	}
	page, err := s.store.Activity().List(r.Context(), query)
	if err != nil {
		swaputil.SendResponse(w, r, http.StatusInternalServerError, "failed to get activity")
		return
	}
	s.metrics.overlayCaptureState(page.Data)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(page)
}

// handleAPIActivityStats serves aggregate activity statistics and histograms.
func (s *Server) handleAPIActivityStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.Activity().Stats(r.Context(), store.ActivityStatsQuery{
		Model: strings.TrimSpace(r.URL.Query().Get("model")),
	})
	if err != nil {
		swaputil.SendResponse(w, r, http.StatusInternalServerError, "failed to get activity stats")
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

// parseActivityTime reads an optional RFC3339 timestamp param, matching the
// ?after= convention used by handleAPIPerformance. A missing param is the zero
// time, which the store treats as unbounded. The UI does not send these; they
// exist for direct API consumers.
func parseActivityTime(r *http.Request, param string) (time.Time, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(param))
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid '%s' timestamp, use RFC3339 format", param)
	}
	return parsed, nil
}

// parseActivityID reads an optional row id bound. A missing param is 0, which
// the store treats as unbounded.
func parseActivityID(r *http.Request, param string) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(param))
	if raw == "" {
		return 0, nil
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("%s must be >= 1", param)
	}
	return id, nil
}

func parseActivityQuery(r *http.Request) (store.ActivityQuery, error) {
	const defaultLimit = 25
	query := store.ActivityQuery{
		Limit: defaultLimit,
		Page:  1,
	}
	query.SrcPrefix = r.URL.Query().Get("src_prefix")

	// model repeats to filter on several models at once (?model=a&model=b).
	// A single ?model=x stays a plain exact match.
	for _, raw := range r.URL.Query()["model"] {
		if model := strings.TrimSpace(raw); model != "" {
			query.Models = append(query.Models, model)
		}
	}

	start, err := parseActivityTime(r, "start")
	if err != nil {
		return store.ActivityQuery{}, err
	}
	end, err := parseActivityTime(r, "end")
	if err != nil {
		return store.ActivityQuery{}, err
	}
	if !start.IsZero() && !end.IsZero() && start.After(end) {
		return store.ActivityQuery{}, fmt.Errorf("start must be before end")
	}
	query.Start, query.End = start, end

	minID, err := parseActivityID(r, "min_id")
	if err != nil {
		return store.ActivityQuery{}, err
	}
	maxID, err := parseActivityID(r, "max_id")
	if err != nil {
		return store.ActivityQuery{}, err
	}
	if minID > 0 && maxID > 0 && minID > maxID {
		return store.ActivityQuery{}, fmt.Errorf("min_id must be <= max_id")
	}
	query.MinID, query.MaxID = minID, maxID

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
		if !store.ValidActivitySortKey(raw) {
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
			swaputil.SendResponse(w, r, http.StatusBadRequest, "invalid 'after' timestamp, use RFC3339 format")
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

func (s *Server) handleAPITailcat(w http.ResponseWriter, r *http.Request) {
	address := ""
	models := []string{}
	enabled := s.cfg.TailcatEnabled()
	if enabled {
		address = s.TailcatAddress()
		if ids := s.tailcatExposedModelIDs(); ids != nil {
			models = ids
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"enabled": enabled, "address": address, "models": models})
}

// tailcatExposedModelIDs returns the sorted model IDs a Tailcat caller can
// currently request, using the same active-profile routing rules as the
// listener itself (ServeTailcatHTTP's tailcatModelAllowed check): local
// models and their aliases, selectors, the active profile's pins, and peer
// models addressed either by their fully qualified name or, where
// unambiguous, their bare name. A "*" entry expands to every candidate.
func (s *Server) tailcatExposedModelIDs() []string {
	tc := s.cfg.Tailcat
	if tc == nil {
		return nil
	}

	candidates := make(map[string]struct{})
	for id, mc := range s.cfg.Models {
		candidates[id] = struct{}{}
		for _, alias := range mc.Aliases {
			candidates[alias] = struct{}{}
		}
	}
	for selectorID := range s.cfg.Selectors {
		candidates[selectorID] = struct{}{}
	}
	for peerID, peer := range s.cfg.Peers {
		for _, modelID := range peer.Models {
			candidates[config.PeerModelFQN(peerID, modelID)] = struct{}{}
			if resolvedPeer, resolvedModel, found := s.cfg.ResolvePeerModel(modelID); found &&
				resolvedPeer == peerID && resolvedModel == modelID {
				candidates[modelID] = struct{}{}
			}
		}
	}
	if profile, ok := s.cfg.Profiles[s.ActiveProfile()]; ok {
		for pin, target := range profile.Pins {
			if target != "" {
				candidates[pin] = struct{}{}
			}
		}
	}

	ids := make([]string, 0, len(candidates))
	for id := range candidates {
		if tailcatModelAllowed(tc.Models, id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// handleAPIHardware serves the hardware snapshot captured at process startup.
func (s *Server) handleAPIHardware(w http.ResponseWriter, r *http.Request) {
	if s.hardware == nil {
		swaputil.SendResponse(w, r, http.StatusServiceUnavailable, "hardware detection unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.hardware); err != nil {
		s.logs.ProxyLogs.Warnf("failed to encode hardware snapshot: %v", err)
	}
}

// handleAPICapture returns the stored request/response capture for a metric ID.
func (s *Server) handleAPICapture(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		swaputil.SendResponse(w, r, http.StatusBadRequest, "invalid capture ID")
		return
	}

	capture := s.metrics.getCaptureByID(id)
	if capture == nil {
		swaputil.SendResponse(w, r, http.StatusNotFound, "capture not found")
		return
	}

	jsonBytes, err := json.Marshal(capture)
	if err != nil {
		swaputil.SendResponse(w, r, http.StatusInternalServerError, "failed to marshal capture")
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
		swaputil.SendResponse(w, r, http.StatusNotFound, "inflight request not found")
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
	msgTypeProfile     messageType = "profileChanged"
)

// sendDropReportInterval is how often an SSE handler reports messages that
// were dropped because the send buffer was full.
const sendDropReportInterval = 5 * time.Second

type messageEnvelope struct {
	Type messageType `json:"type"`
	Data string      `json:"data"`
}

// serveSSE streams messages to the client as Server-Sent Events until the
// client disconnects or the server shuts down. setup subscribes to the events
// the stream carries, queues any initial payload with send, and returns a
// function that unsubscribes. name identifies the handler in drop warnings.
func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request, name string, setup func(send func(messageEnvelope)) (unsubscribe func())) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// prevent nginx from buffering SSE
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		swaputil.SendResponse(w, r, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	// internal/event already has a 50K event buffer
	// a 1K message buffer should be enough, watch the logs for the warning that the sendBuffer is full
	sendBuffer := make(chan messageEnvelope, 1024)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Dropped messages are counted and reported at most once per interval.
	// Logging every drop floods the logs because each warning becomes a log
	// event that may be sent over this same buffer, which drops again.
	dropped := newSuppressionCounter(sendDropReportInterval)
	cancelled := newSuppressionCounter(sendDropReportInterval)
	reportDropped := func(n int) {
		s.logs.ProxyLogs.Warnf("%s sendBuffer full, %d messages suppressed", name, n)
	}
	reportCancelled := func(n int) {
		s.logs.ProxyLogs.Warnf("%s send suppressed due to context done, %d messages suppressed", name, n)
	}
	// runs after setup's subscriptions are removed so any remaining counts
	// are reported before the connection goes away
	defer func() {
		if n, ok := dropped.Flush(); ok {
			reportDropped(n)
		}
		if n, ok := cancelled.Flush(); ok {
			reportCancelled(n)
		}
	}()

	send := func(msg messageEnvelope) {
		select {
		case sendBuffer <- msg:
		case <-ctx.Done():
			if n, ok := cancelled.Add(); ok {
				reportCancelled(n)
			}
		default:
			if n, ok := dropped.Add(); ok {
				reportDropped(n)
			}
		}
	}

	defer setup(send)()

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

// handleAPIEvents streams server events (model status, activity, in-flight
// requests, UI config, profile changes) to the client as Server-Sent Events.
// Log data has its own stream, handleAPILogEvents.
func (s *Server) handleAPIEvents(w http.ResponseWriter, r *http.Request) {
	s.serveSSE(w, r, "handleAPIEvents", func(send func(messageEnvelope)) func() {
		sendModels := func() {
			if data, err := json.Marshal(s.modelStatus()); err == nil {
				send(messageEnvelope{Type: msgTypeModelStatus, Data: string(data)})
			}
		}
		sendActivity := func(id int) {
			if j, err := json.Marshal(map[string]int{"id": id}); err == nil {
				send(messageEnvelope{Type: msgTypeActivity, Data: string(j)})
			}
		}
		sendInFlight := func(update swaputil.InFlightRequestsEvent) {
			if update.Operation == inflightOperationSnapshot && update.Requests == nil {
				update.Requests = []swaputil.InflightRequestEntry{}
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
		sendProfile := func() {
			if j, err := json.Marshal(map[string]any{"active": nullableProfile(s.ActiveProfile())}); err == nil {
				send(messageEnvelope{Type: msgTypeProfile, Data: string(j)})
			}
		}

		unsubscribe := []context.CancelFunc{
			event.On(func(e swaputil.ProcessStateChangeEvent) { sendModels() }),
			event.On(func(e swaputil.ModelCapabilitiesChangedEvent) { sendModels() }),
			event.On(func(e swaputil.ConfigFileChangedEvent) { sendModels() }),
			event.On(func(e swaputil.ProfileChangedEvent) {
				sendProfile()
				sendModels()
			}),
			event.On(func(e ActivityLogEvent) { sendActivity(e.Metrics.ID) }),
			event.On(func(e swaputil.InFlightRequestsEvent) { sendInFlight(e) }),
		}

		// initial payload
		sendModels()
		sendUIConfig()
		sendProfile()
		sendInFlight(s.inflight.Current())

		return func() { cancelAll(unsubscribe) }
	})
}

// handleAPILogEvents streams log data to the client as Server-Sent Events.
// The streams are chosen with repeated query parameters, for example
// ?stream=proxy&stream=http. Each stream's history is sent first unless
// ?no-history is set.
func (s *Server) handleAPILogEvents(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	names := query["stream"]
	if len(names) == 0 {
		swaputil.SendResponse(w, r, http.StatusBadRequest, "at least one stream is required: proxy, upstream or http")
		return
	}
	streams := make(map[logmon.StreamID]*logmon.Monitor, len(names))
	for _, name := range names {
		id := logmon.StreamID(name)
		log, ok := s.logs.Stream(id)
		if !ok {
			swaputil.SendResponse(w, r, http.StatusBadRequest, fmt.Sprintf("invalid stream %q. Use proxy, upstream or http", name))
			return
		}
		streams[id] = log
	}
	_, skipHistory := query["no-history"]

	s.serveSSE(w, r, "handleAPILogEvents", func(send func(messageEnvelope)) func() {
		sendLogData := func(source logmon.StreamID, data []byte) {
			if j, err := json.Marshal(map[string]string{"source": string(source), "data": string(data)}); err == nil {
				send(messageEnvelope{Type: msgTypeLogData, Data: string(j)})
			}
		}

		unsubscribe := make([]context.CancelFunc, 0, len(streams))
		for id, log := range streams {
			unsubscribe = append(unsubscribe, log.OnLogData(func(data []byte) { sendLogData(id, data) }))
		}
		if !skipHistory {
			for id, log := range streams {
				if history := log.GetHistory(); len(history) != 0 {
					sendLogData(id, history)
				}
			}
		}
		return func() { cancelAll(unsubscribe) }
	})
}

// cancelAll calls every cancel function, last first, matching the order
// deferred calls would run in.
func cancelAll(cancels []context.CancelFunc) {
	for i := len(cancels) - 1; i >= 0; i-- {
		cancels[i]()
	}
}
