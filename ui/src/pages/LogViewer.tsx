import { useState, useEffect, useRef, useMemo, useCallback } from "react";
import { useAPI } from "../contexts/APIProvider";
import { usePersistentState } from "../hooks/usePersistentState";
import { Panel, PanelGroup, PanelResizeHandle } from "react-resizable-panels";
import {
  RiTextWrap,
  RiAlignJustify,
  RiFontSize,
  RiMenuSearchLine,
  RiMenuSearchFill,
  RiCloseCircleFill,
} from "react-icons/ri";
import { useTheme } from "../contexts/ThemeProvider";

const LogViewer = () => {
  const { proxyLogs, upstreamLogs } = useAPI();
  const { screenWidth } = useTheme();
  const direction = screenWidth === "xs" || screenWidth === "sm" ? "vertical" : "horizontal";

  return (
    <PanelGroup direction={direction} className="gap-2" autoSaveId="logviewer-panel-group">
      <Panel id="proxy" defaultSize={50} minSize={5} maxSize={100} collapsible={true}>
        <LogPanel id="proxy" title="Proxy Logs" logData={proxyLogs} />
      </Panel>
      <PanelResizeHandle
        className={
          direction === "horizontal"
            ? "w-2 h-full bg-primary hover:bg-success transition-colors rounded"
            : "w-full h-2 bg-primary hover:bg-success transition-colors rounded"
        }
      />
      <Panel id="upstream" defaultSize={50} minSize={5} maxSize={100} collapsible={true}>
        <LogPanel id="upstream" title="Upstream Logs" logData={upstreamLogs} />
      </Panel>
    </PanelGroup>
  );
};

interface LogPanelProps {
  id: string;
  title: string;
  logData: string;
}
export const LogPanel = ({ id, title, logData }: LogPanelProps) => {
  const [filterRegex, setFilterRegex] = useState("");
  const [fontSize, setFontSize] = usePersistentState<"xxs" | "xs" | "small" | "normal">(
    `logPanel-${id}-fontSize`,
    "normal"
  );
  const [wrapText, setTextWrap] = usePersistentState(`logPanel-${id}-wrapText`, false);
  const [showFilter, setShowFilter] = usePersistentState(`logPanel-${id}-showFilter`, false);

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

  const toggleWrapText = useCallback(() => {
    setTextWrap((prev) => !prev);
  }, []);

  const toggleFilter = useCallback(() => {
    if (showFilter) {
      setShowFilter(false);
      setFilterRegex(""); // Clear filter when closing
    } else {
      setShowFilter(true);
    }
  }, [filterRegex, setFilterRegex, showFilter]);

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
    <div className="rounded-lg overflow-hidden flex flex-col bg-gray-950/5 dark:bg-white/10 h-full p-1">
      <div className="p-4">
        <div className="flex items-center justify-between">
          <h3 className="m-0 text-lg p-0">{title}</h3>

          <div className="flex gap-2 items-center">
            <button className="btn border-0" onClick={toggleFontSize}>
              <RiFontSize />
            </button>
            <button className="btn border-0" onClick={toggleWrapText}>
              {wrapText ? <RiTextWrap /> : <RiAlignJustify />}
            </button>
            <button className="btn border-0" onClick={toggleFilter}>
              {showFilter ? <RiMenuSearchFill /> : <RiMenuSearchLine />}
            </button>
          </div>
        </div>

        {/* Filtering Options - Full width on mobile, normal on desktop */}
        {showFilter && (
          <div className="mt-2 w-full">
            <div className="flex gap-2 items-center w-full">
              <input
                type="text"
                className="w-full text-sm border border-gray-950/10 dark:border-white/5 p-2 rounded outline-none"
                placeholder="Filter logs..."
                value={filterRegex}
                onChange={(e) => setFilterRegex(e.target.value)}
              />
              <button className="pl-2" onClick={() => setFilterRegex("")}>
                <RiCloseCircleFill size="24" />
              </button>
            </div>
          </div>
        )}
      </div>
      <div className="rounded-lg bg-background font-mono text-sm flex-1 overflow-hidden">
        <pre ref={preTagRef} className={`${textWrapClass} ${fontSizeClass} h-full overflow-auto p-4`}>
          {filteredLogs}
        </pre>
      </div>
    </div>
  );
};
export default LogViewer;
