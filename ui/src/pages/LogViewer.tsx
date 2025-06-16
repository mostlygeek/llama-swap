import { useState, useEffect, useRef, useMemo, useCallback } from "react";
import { useAPI } from "../contexts/APIProvider";
import { usePersistentState } from "../hooks/usePersistentState";

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
      <LogPanel id="proxy" title="Proxy Logs" logData={proxyLogs} />
      <LogPanel id="upstream" title="Upstream Logs" logData={upstreamLogs} />
    </div>
  );
};

interface LogPanelProps {
  id: string;
  title: string;
  logData: string;
  className?: string;
}

export const LogPanel = ({ id, title, logData, className }: LogPanelProps) => {
  const [filterRegex, setFilterRegex] = useState("");
  const [panelState, setPanelState] = usePersistentState<"hide" | "small" | "max">(
    `logPanel-${id}-panelState`,
    "small"
  );
  const [fontSize, setFontSize] = usePersistentState<"xxs" | "xs" | "small" | "normal">(
    `logPanel-${id}-fontSize`,
    "normal"
  );
  const [wrapText, setTextWrap] = usePersistentState(`logPanel-${id}-wrapText`, false);

  const textWrapClass = useMemo(() => {
    return wrapText ? "whitespace-pre-wrap" : "whitespace-pre";
  }, [wrapText]);

  const toggleFontSize = useCallback(() => {
    setFontSize((prev) => {
      switch (prev) {
        case "xxs":
          return "xs";
        case "xs":
          return "small";
        case "small":
          return "normal";
        case "normal":
          return "xxs";
      }
    });
  }, []);

  const togglePanelState = useCallback(() => {
    setPanelState((prev) => {
      if (prev === "small") return "max";
      if (prev === "hide") return "small";
      return "hide";
    });
  }, []);

  const fontSizeClass = useMemo(() => {
    switch (fontSize) {
      case "xxs":
        return "text-[0.5rem]"; // 0.5rem (8px)
      case "xs":
        return "text-[0.75rem]"; // 0.75rem (12px)
      case "small":
        return "text-[0.875rem]"; // 0.875rem (14px)
      case "normal":
        return "text-base"; // 1rem (16px)
    }
  }, [fontSize]);

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
    <div className={`bg-surface border border-border rounded-lg overflow-hidden flex flex-col ${className || ""}`}>
      <div className="p-4 border-b border-border bg-secondary">
        <div className="flex flex-col md:flex-row md:items-center md:justify-between gap-4">
          {/* Title - Always full width on mobile, normal on desktop */}
          <div className="w-full md:w-auto" onClick={togglePanelState}>
            <h3 className="m-0 text-lg">{title}</h3>
          </div>

          <div className="flex flex-col sm:flex-row gap-4 w-full md:w-auto">
            {/* Sizing Buttons - Stacks vertically on mobile */}
            <div className="flex flex-wrap gap-2">
              <button className="btn" onClick={togglePanelState}>
                size: {panelState}
              </button>
              <button className="btn" onClick={toggleFontSize}>
                font: {fontSize}
              </button>
              <button className="btn" onClick={() => setTextWrap((prev) => !prev)}>
                {wrapText ? "wrap" : "wrap off"}
              </button>
            </div>

            {/* Filtering Options - Full width on mobile, normal on desktop */}
            <div className="flex flex-1 min-w-0 gap-2">
              <input
                type="text"
                className="flex-1 min-w-[120px] text-sm border p-2 rounded"
                placeholder="Filter logs..."
                value={filterRegex}
                onChange={(e) => setFilterRegex(e.target.value)}
              />
              <button className="btn" onClick={() => setFilterRegex("")}>
                Clear
              </button>
            </div>
          </div>
        </div>
      </div>

      {panelState !== "hide" && (
        <div className="flex-1 bg-background font-mono text-sm leading-[1.4] p-3">
          <pre
            ref={preTagRef}
            className={`flex-1 p-4 overflow-y-auto whitespace-pre min-h-0 ${textWrapClass} ${fontSizeClass}`}
            style={{
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
