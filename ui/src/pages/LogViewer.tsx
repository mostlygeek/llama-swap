import React, { useState, useEffect, useRef, useMemo } from "react";
import { useAPI } from "../contexts/APIProvider";

function LogViewer() {
  const { proxyLogs, upstreamLogs, enableProxyLogs, enableUpstreamLogs } = useAPI();
  const proxyLogsRef = useRef<HTMLPreElement>(null);
  const upstreamLogsRef = useRef<HTMLPreElement>(null);
  const [proxyFilter, setProxyFilter] = useState("");
  const [upstreamFilter, setUpstreamFilter] = useState("");

  useEffect(() => {
    enableProxyLogs(true);
    enableUpstreamLogs(true);
    return () => {
      enableProxyLogs(false);
      enableUpstreamLogs(false);
    };
  }, []);

  const filteredProxyLogs = useMemo(() => {
    if (!proxyFilter) return proxyLogs;
    try {
      const regex = new RegExp(proxyFilter, "i");
      const lines = proxyLogs.split("\n");
      const filtered = lines.filter((line) => regex.test(line));
      return filtered.join("\n");
    } catch (e) {
      return proxyLogs; // Return unfiltered if regex is invalid
    }
  }, [proxyLogs, proxyFilter]);

  const filteredUpstreamLogs = useMemo(() => {
    if (!upstreamFilter) return upstreamLogs;
    try {
      const regex = new RegExp(upstreamFilter, "i");
      const lines = upstreamLogs.split("\n");
      const filtered = lines.filter((line) => regex.test(line));
      return filtered.join("\n");
    } catch (e) {
      return upstreamLogs; // Return unfiltered if regex is invalid
    }
  }, [upstreamLogs, upstreamFilter]);

  useEffect(() => {
    if (!proxyLogsRef.current) return;
    proxyLogsRef.current.scrollTop = proxyLogsRef.current.scrollHeight;
  }, [filteredProxyLogs]);

  useEffect(() => {
    if (!upstreamLogsRef.current) return;
    upstreamLogsRef.current.scrollTop = upstreamLogsRef.current.scrollHeight;
  }, [filteredUpstreamLogs]);

  return (
    <div className="logs-container">
      <div className="log-panel">
        <div className="log-panel-header">
          <div className="log-panel-title">
            <h3>Proxy Logs</h3>
            <span className="status status--success">Live</span>
          </div>
          <div className="log-panel-controls">
            <input
              type="text"
              className="form-control log-filter"
              placeholder="Filter logs..."
              value={proxyFilter}
              onChange={(e) => setProxyFilter(e.target.value)}
            />
            <button className="btn btn--sm btn--outline" onClick={() => setProxyFilter("")}>
              Clear
            </button>
          </div>
        </div>
        <div className="log-content" id="proxy-logs">
          <pre
            ref={proxyLogsRef}
            className="flex-1 p-4 overflow-y-auto whitespace-pre-wrap break-words min-h-0 max-h-[500px]"
          >
            {filteredProxyLogs}
          </pre>
        </div>
      </div>

      <div className="log-panel" id="upstream-panel">
        <div className="log-panel-header" id="upstream-header">
          <div className="log-panel-title">
            <h3>Upstream Logs</h3>
            <span className="status status--info">Minimized</span>
            <button className="collapse-toggle" id="upstream-toggle">
              ▼
            </button>
          </div>
          <div className="log-panel-controls">
            <input
              type="text"
              className="form-control log-filter"
              placeholder="Filter logs..."
              value={upstreamFilter}
              onChange={(e) => setUpstreamFilter(e.target.value)}
            />
            <button className="btn btn--sm btn--outline" onClick={() => setUpstreamFilter("")}>
              Clear
            </button>
          </div>
        </div>

        <div className="log-content">
          <pre
            ref={upstreamLogsRef}
            className="flex-1 p-4 overflow-y-auto whitespace-pre-wrap break-words min-h-0 max-h-[500px]"
          >
            {filteredUpstreamLogs}
          </pre>
        </div>
      </div>
    </div>
  );
}

export default LogViewer;
