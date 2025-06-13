import { useState, useEffect, useRef, useMemo } from "react";
import { useAPI } from "../contexts/APIProvider";

const LogViewer = () => {
  const { proxyLogs, upstreamLogs, enableProxyLogs, enableUpstreamLogs } = useAPI();

  useEffect(() => {
    enableProxyLogs(true);
    enableUpstreamLogs(true);
    return () => {
      enableProxyLogs(false);
      enableUpstreamLogs(false);
    };
  }, []);

  return (
    <div className="flex flex-col gap-5">
      <LogPanel title="Proxy Logs" logData={proxyLogs} />
      <LogPanel title="Upstream Logs" logData={upstreamLogs} />
    </div>
  );
};

interface LogPanelProps {
  title: string;
  logData: string;
}

const LogPanel = ({ title, logData }: LogPanelProps) => {
  const [filterRegex, setFilterRegex] = useState("");
  const [panelState, setPanelState] = useState<"show" | "hide" | "max">("show");
  const filteredLogs = useMemo(() => {
    if (!filterRegex) return logData;
    try {
      const regex = new RegExp(filterRegex, "i");
      const lines = logData.split("\n");
      const filtered = lines.filter((line) => regex.test(line));
      return filtered.join("\n");
    } catch (e) {
      return logData; // Return unfiltered if regex is invalid
    }
  }, [logData, filterRegex]);

  // auto scroll to bottom
  const preTagRef = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (!preTagRef.current) return;
    preTagRef.current.scrollTop = preTagRef.current.scrollHeight;
  }, [filteredLogs]);

  return (
    <div className="bg-surface border border-border rounded-lg overflow-hidden flex flex-col">
      <div className="p-4 border-b border-border flex items-center justify-between bg-secondary gap-3">
        <div className="flex items-center gap-3 flex-shrink-0">
          <h3 className="m-0 text-lg">{title}</h3>
          <button
            className="btn btn--sm"
            onClick={() => {
              setPanelState((prev) => {
                if (prev === "show") return "max";
                if (prev === "hide") return "show";
                return "hide";
              });
            }}
          >
            {panelState}
          </button>
        </div>
        <div className="flex items-center gap-2 flex-1 min-w-0 justify-end">
          <input
            type="text"
            className="min-w-[200px] text-sm border p-2 rounded"
            placeholder="Filter logs..."
            value={filterRegex}
            onChange={(e) => setFilterRegex(e.target.value)}
          />
          <button className="btn btn--sm btn--outline flex-shrink-0" onClick={() => setFilterRegex("")}>
            Clear
          </button>
        </div>
      </div>

      {panelState !== "hide" && (
        <div className="flex-1 bg-background font-mono text-sm leading-[1.4] p-3">
          <pre
            ref={preTagRef}
            className="flex-1 p-4 overflow-y-auto whitespace-pre min-h-0 text-sm"
            style={{
              scrollbarWidth: "auto",
              maxHeight: panelState === "max" ? "1500px" : "500px",
            }}
          >
            {filteredLogs}
          </pre>
        </div>
      )}
    </div>
  );
};

export default LogViewer;
