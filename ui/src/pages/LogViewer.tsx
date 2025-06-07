import React, { useState, useEffect, useRef } from "react";

function LogViewer() {
  const [proxyLogs, setProxyLogs] = useState("");
  const [upstreamLogs, setUpstreamLogs] = useState("");
  const [proxyFilter, setProxyFilter] = useState("");
  const [upstreamFilter, setUpstreamFilter] = useState("");
  const [upstreamMinimized, setUpstreamMinimized] = useState(true);

  const proxyLogRef = useRef<HTMLPreElement>(null);
  const upstreamLogRef = useRef<HTMLPreElement>(null);

  useEffect(() => {
    // Set up EventSource for proxy logs
    const proxyEventSource = new EventSource("/api/logs/stream/proxy");

    proxyEventSource.onmessage = (event) => {
      setProxyLogs((prev) => {
        const newLogs = prev + event.data;
        return newLogs.slice(-1024 * 100); // Keep last 100KB
      });
    };

    // Set up EventSource for upstream logs
    const upstreamEventSource = new EventSource("/api/logs/stream/upstream");

    upstreamEventSource.onmessage = (event) => {
      setUpstreamLogs((prev) => {
        const newLogs = prev + event.data;
        return newLogs.slice(-1024 * 100); // Keep last 100KB
      });
    };

    return () => {
      proxyEventSource.close();
      upstreamEventSource.close();
    };
  }, []);

  // Auto-scroll to bottom when new logs arrive
  useEffect(() => {
    if (proxyLogRef.current) {
      proxyLogRef.current.scrollTop = proxyLogRef.current.scrollHeight;
    }
  }, [proxyLogs]);

  useEffect(() => {
    if (upstreamLogRef.current) {
      upstreamLogRef.current.scrollTop = upstreamLogRef.current.scrollHeight;
    }
  }, [upstreamLogs]);

  // @ts-ignore
  const filterLogs = (logs, filter) => {
    if (!filter) return logs;
    try {
      const regex = new RegExp(filter);

      return (
        logs
          .split("\n")
          // @ts-ignore
          .filter((line) => regex.test(line))
          .join("\n")
      );
    } catch (e) {
      return logs; // Return unfiltered if regex is invalid
    }
  };

  return (
    <div className="space-y-4">
      <h2 className="text-2xl font-bold text-gray-900">Log Viewer</h2>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 h-96">
        {/* Proxy Logs */}
        <div className="bg-white shadow rounded-lg flex flex-col">
          <div className="p-4 border-b">
            <h3 className="text-lg font-medium text-gray-900 mb-2">Proxy Logs</h3>
            <div className="flex space-x-2">
              <input
                type="text"
                value={proxyFilter}
                onChange={(e) => setProxyFilter(e.target.value)}
                placeholder="Regex filter..."
                className="flex-1 px-3 py-1 border border-gray-300 rounded text-sm"
              />
              <button
                onClick={() => setProxyFilter("")}
                className="px-3 py-1 bg-gray-200 hover:bg-gray-300 rounded text-sm"
              >
                Clear
              </button>
            </div>
          </div>
          <div className="flex-1 overflow-hidden">
            <pre
              ref={proxyLogRef}
              className="h-full p-4 bg-gray-50 overflow-y-auto text-sm font-mono whitespace-pre-wrap"
            >
              {filterLogs(proxyLogs, proxyFilter) || "Waiting for proxy logs..."}
            </pre>
          </div>
        </div>

        {/* Upstream Logs */}
        <div className={`bg-white shadow rounded-lg flex flex-col ${upstreamMinimized ? "col-span-1 lg:w-12" : ""}`}>
          <div className="p-4 border-b">
            <h3
              className="text-lg font-medium text-gray-900 mb-2 cursor-pointer hover:bg-gray-50"
              onClick={() => setUpstreamMinimized(!upstreamMinimized)}
            >
              {upstreamMinimized ? "↕" : "Upstream Logs"}
            </h3>
            {!upstreamMinimized && (
              <div className="flex space-x-2">
                <input
                  type="text"
                  value={upstreamFilter}
                  onChange={(e) => setUpstreamFilter(e.target.value)}
                  placeholder="Regex filter..."
                  className="flex-1 px-3 py-1 border border-gray-300 rounded text-sm"
                />
                <button
                  onClick={() => setUpstreamFilter("")}
                  className="px-3 py-1 bg-gray-200 hover:bg-gray-300 rounded text-sm"
                >
                  Clear
                </button>
              </div>
            )}
          </div>
          {!upstreamMinimized && (
            <div className="flex-1 overflow-hidden">
              <pre
                ref={upstreamLogRef}
                className="h-full p-4 bg-gray-50 overflow-y-auto text-sm font-mono whitespace-pre-wrap"
              >
                {filterLogs(upstreamLogs, upstreamFilter) || "Waiting for upstream logs..."}
              </pre>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

export default LogViewer;
