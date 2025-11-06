import { useState, useCallback, useMemo } from "react";
import { useAPI } from "../contexts/APIProvider";
import { LogPanel } from "./LogViewer";
import { usePersistentState } from "../hooks/usePersistentState";
import { Panel, PanelGroup, PanelResizeHandle } from "react-resizable-panels";
import { useTheme } from "../contexts/ThemeProvider";
import { RiEyeFill, RiEyeOffFill, RiSwapBoxFill, RiEjectLine, RiMenuFill } from "react-icons/ri";

export default function ModelsPage() {
  const { isNarrow } = useTheme();
  const direction = isNarrow ? "vertical" : "horizontal";
  const { upstreamLogs } = useAPI();

  return (
    <PanelGroup direction={direction} className="gap-2" autoSaveId={"models-panel-group"}>
      <Panel id="models" defaultSize={50} minSize={isNarrow ? 0 : 25} maxSize={100} collapsible={isNarrow}>
        <ModelsPanel />
      </Panel>

      <PanelResizeHandle
        className={
          direction === "horizontal"
            ? "w-2 h-full bg-primary hover:bg-success transition-colors rounded"
            : "w-full h-2 bg-primary hover:bg-success transition-colors rounded"
        }
      />
      <Panel collapsible={true} defaultSize={50} minSize={0}>
        <div className="flex flex-col h-full space-y-4">
          {direction === "horizontal" && <StatsPanel />}
          <div className="flex-1 min-h-0">
            <LogPanel id="modelsupstream" title="Upstream Logs" logData={upstreamLogs} />
          </div>
        </div>
      </Panel>
    </PanelGroup>
  );
}

function ModelsPanel() {
  const { models, loadModel, unloadAllModels, unloadSingleModel } = useAPI();
  const { isNarrow } = useTheme();
  const [isUnloading, setIsUnloading] = useState(false);
  const [showUnlisted, setShowUnlisted] = usePersistentState("showUnlisted", true);
  const [showIdorName, setShowIdorName] = usePersistentState<"id" | "name">("showIdorName", "id"); // true = show ID, false = show name
  const [menuOpen, setMenuOpen] = useState(false);

  const filteredModels = useMemo(() => {
    return models.filter((model) => showUnlisted || !model.unlisted);
  }, [models, showUnlisted]);

  const handleUnloadAllModels = useCallback(async () => {
    setIsUnloading(true);
    try {
      await unloadAllModels();
    } catch (e) {
      console.error(e);
    } finally {
      setTimeout(() => {
        setIsUnloading(false);
      }, 1000);
    }
  }, [unloadAllModels]);

  const toggleIdorName = useCallback(() => {
    setShowIdorName((prev) => (prev === "name" ? "id" : "name"));
  }, [showIdorName]);

  return (
    <div className="card h-full flex flex-col">
      <div className="shrink-0">
        <div className="flex justify-between items-baseline">
          <h2 className={isNarrow ? "text-xl" : ""}>Models</h2>
          {isNarrow && (
            <div className="relative">
              <button className="btn text-base flex items-center gap-2 py-1" onClick={() => setMenuOpen(!menuOpen)}>
                <RiMenuFill size="20" />
              </button>
              {menuOpen && (
                <div className="absolute right-0 mt-2 w-48 bg-surface border border-gray-200 dark:border-white/10 rounded shadow-lg z-20">
                  <button
                    className="w-full text-left px-4 py-2 hover:bg-secondary-hover flex items-center gap-2"
                    onClick={() => {
                      toggleIdorName();
                      setMenuOpen(false);
                    }}
                  >
                    <RiSwapBoxFill size="20" /> {showIdorName === "id" ? "Show Name" : "Show ID"}
                  </button>
                  <button
                    className="w-full text-left px-4 py-2 hover:bg-secondary-hover flex items-center gap-2"
                    onClick={() => {
                      setShowUnlisted(!showUnlisted);
                      setMenuOpen(false);
                    }}
                  >
                    {showUnlisted ? <RiEyeOffFill size="20" /> : <RiEyeFill size="20" />}{" "}
                    {showUnlisted ? "Hide Unlisted" : "Show Unlisted"}
                  </button>
                  <button
                    className="w-full text-left px-4 py-2 hover:bg-secondary-hover flex items-center gap-2"
                    onClick={() => {
                      handleUnloadAllModels();
                      setMenuOpen(false);
                    }}
                    disabled={isUnloading}
                  >
                    <RiEjectLine size="24" /> {isUnloading ? "Unloading..." : "Unload All"}
                  </button>
                </div>
              )}
            </div>
          )}
        </div>
        {!isNarrow && (
          <div className="flex justify-between">
            <div className="flex gap-2">
              <button
                className="btn text-base flex items-center gap-2"
                onClick={toggleIdorName}
                style={{ lineHeight: "1.2" }}
              >
                <RiSwapBoxFill size="20" /> {showIdorName === "id" ? "ID" : "Name"}
              </button>

              <button
                className="btn text-base flex items-center gap-2"
                onClick={() => setShowUnlisted(!showUnlisted)}
                style={{ lineHeight: "1.2" }}
              >
                {showUnlisted ? <RiEyeFill size="20" /> : <RiEyeOffFill size="20" />} unlisted
              </button>
            </div>
            <button
              className="btn text-base flex items-center gap-2"
              onClick={handleUnloadAllModels}
              disabled={isUnloading}
            >
              <RiEjectLine size="24" /> {isUnloading ? "Unloading..." : "Unload All"}
            </button>
          </div>
        )}
      </div>

      <div className="flex-1 overflow-y-auto">
        <table className="w-full">
          <thead className="sticky top-0 bg-card z-10">
            <tr className="text-left border-b border-gray-200 dark:border-white/10 bg-surface">
              <th>{showIdorName === "id" ? "Model ID" : "Name"}</th>
              <th></th>
              <th>State</th>
            </tr>
          </thead>
          <tbody>
            {filteredModels.map((model) => (
              <tr key={model.id} className="border-b hover:bg-secondary-hover border-gray-200">
                <td className={`${model.unlisted ? "text-txtsecondary" : ""}`}>
                  <a href={`/upstream/${model.id}/`} className="font-semibold" target="_blank">
                    {showIdorName === "id" ? model.id : model.name !== "" ? model.name : model.id}
                  </a>

                  {!!model.description && (
                    <p className={model.unlisted ? "text-opacity-70" : ""}>
                      <em>{model.description}</em>
                    </p>
                  )}
                </td>
                <td className="w-12">
                  {model.state === "stopped" ? (
                    <button className="btn btn--sm" onClick={() => loadModel(model.id)}>
                      Load
                    </button>
                  ) : (
                    <button
                      className="btn btn--sm"
                      onClick={() => unloadSingleModel(model.id)}
                      disabled={model.state !== "ready"}
                    >
                      Unload
                    </button>
                  )}
                </td>
                <td className="w-20">
                  <span className={`w-16 text-center status status--${model.state}`}>{model.state}</span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

interface HistogramData {
  bins: number[];
  min: number;
  max: number;
  binSize: number;
  p99: number;
  p95: number;
  p50: number;
}

function TokenHistogram({ data }: { data: HistogramData }) {
  const { bins, min, max, p50, p95, p99 } = data;
  const maxCount = Math.max(...bins);

  const height = 120;
  const padding = { top: 10, right: 15, bottom: 25, left: 45 };

  // Use viewBox for responsive sizing
  const viewBoxWidth = 600;
  const chartWidth = viewBoxWidth - padding.left - padding.right;
  const chartHeight = height - padding.top - padding.bottom;

  const barWidth = chartWidth / bins.length;
  const range = max - min;

  // Calculate x position for a given value
  const getXPosition = (value: number) => {
    return padding.left + ((value - min) / range) * chartWidth;
  };

  return (
    <div className="mt-2 w-full">
      <svg
        viewBox={`0 0 ${viewBoxWidth} ${height}`}
        className="w-full h-auto"
        preserveAspectRatio="xMidYMid meet"
      >
        {/* Y-axis */}
        <line
          x1={padding.left}
          y1={padding.top}
          x2={padding.left}
          y2={height - padding.bottom}
          stroke="currentColor"
          strokeWidth="1"
          opacity="0.3"
        />

        {/* X-axis */}
        <line
          x1={padding.left}
          y1={height - padding.bottom}
          x2={viewBoxWidth - padding.right}
          y2={height - padding.bottom}
          stroke="currentColor"
          strokeWidth="1"
          opacity="0.3"
        />

        {/* Histogram bars */}
        {bins.map((count, i) => {
          const barHeight = maxCount > 0 ? (count / maxCount) * chartHeight : 0;
          const x = padding.left + i * barWidth;
          const y = height - padding.bottom - barHeight;
          const binStart = min + i * data.binSize;
          const binEnd = binStart + data.binSize;

          return (
            <g key={i}>
              <rect
                x={x}
                y={y}
                width={Math.max(barWidth - 1, 1)}
                height={barHeight}
                fill="currentColor"
                opacity="0.6"
                className="text-blue-500 dark:text-blue-400 hover:opacity-90 transition-opacity cursor-pointer"
              />
              <title>{`${binStart.toFixed(1)} - ${binEnd.toFixed(1)} tokens/sec\nCount: ${count}`}</title>
            </g>
          );
        })}

        {/* Percentile lines */}
        <line
          x1={getXPosition(p50)}
          y1={padding.top}
          x2={getXPosition(p50)}
          y2={height - padding.bottom}
          stroke="currentColor"
          strokeWidth="2"
          strokeDasharray="4 2"
          opacity="0.7"
          className="text-gray-600 dark:text-gray-400"
        />

        <line
          x1={getXPosition(p95)}
          y1={padding.top}
          x2={getXPosition(p95)}
          y2={height - padding.bottom}
          stroke="currentColor"
          strokeWidth="2"
          strokeDasharray="4 2"
          opacity="0.7"
          className="text-orange-500 dark:text-orange-400"
        />

        <line
          x1={getXPosition(p99)}
          y1={padding.top}
          x2={getXPosition(p99)}
          y2={height - padding.bottom}
          stroke="currentColor"
          strokeWidth="2"
          strokeDasharray="4 2"
          opacity="0.7"
          className="text-green-500 dark:text-green-400"
        />

        {/* X-axis labels */}
        <text
          x={padding.left}
          y={height - 5}
          fontSize="10"
          fill="currentColor"
          opacity="0.6"
          textAnchor="start"
        >
          {min.toFixed(1)}
        </text>

        <text
          x={viewBoxWidth - padding.right}
          y={height - 5}
          fontSize="10"
          fill="currentColor"
          opacity="0.6"
          textAnchor="end"
        >
          {max.toFixed(1)}
        </text>

        {/* X-axis label */}
        <text
          x={padding.left + chartWidth / 2}
          y={height - 2}
          fontSize="10"
          fill="currentColor"
          opacity="0.6"
          textAnchor="middle"
        >
          Tokens/Second Distribution
        </text>
      </svg>
    </div>
  );
}

function StatsPanel() {
  const { metrics } = useAPI();

  const [totalRequests, totalInputTokens, totalOutputTokens, tokenStats, histogramData] = useMemo(() => {
    const totalRequests = metrics.length;
    if (totalRequests === 0) {
      return [0, 0, 0, { p99: 0, p95: 0, p50: 0 }, null];
    }
    const totalInputTokens = metrics.reduce((sum, m) => sum + m.input_tokens, 0);
    const totalOutputTokens = metrics.reduce((sum, m) => sum + m.output_tokens, 0);

    // Calculate token statistics using output_tokens and duration_ms
    // Filter out metrics with invalid duration or output tokens
    const validMetrics = metrics.filter((m) => m.duration_ms > 0 && m.output_tokens > 0);
    if (validMetrics.length === 0) {
      return [totalRequests, totalInputTokens, totalOutputTokens, { p99: 0, p95: 0, p50: 0 }, null];
    }

    // Calculate tokens/second for each valid metric
    const tokensPerSecond = validMetrics.map((m) => m.output_tokens / (m.duration_ms / 1000));

    // Sort for percentile calculation
    const sortedTokensPerSecond = [...tokensPerSecond].sort((a, b) => a - b);

    // Calculate percentiles - showing speed thresholds where X% of requests are SLOWER (below)
    // P99: 99% of requests are slower than this speed (99th percentile - fast requests)
    // P95: 95% of requests are slower than this speed (95th percentile)
    // P50: 50% of requests are slower than this speed (median)
    const p99 = sortedTokensPerSecond[Math.floor(sortedTokensPerSecond.length * 0.99)];
    const p95 = sortedTokensPerSecond[Math.floor(sortedTokensPerSecond.length * 0.95)];
    const p50 = sortedTokensPerSecond[Math.floor(sortedTokensPerSecond.length * 0.5)];

    // Create histogram data
    const min = Math.min(...tokensPerSecond);
    const max = Math.max(...tokensPerSecond);
    const binCount = Math.min(30, Math.max(10, Math.floor(tokensPerSecond.length / 5))); // Adaptive bin count
    const binSize = (max - min) / binCount;

    const bins = Array(binCount).fill(0);
    tokensPerSecond.forEach((value) => {
      const binIndex = Math.min(Math.floor((value - min) / binSize), binCount - 1);
      bins[binIndex]++;
    });

    const histogramData = {
      bins,
      min,
      max,
      binSize,
      p99,
      p95,
      p50,
    };

    return [
      totalRequests,
      totalInputTokens,
      totalOutputTokens,
      {
        p99: p99.toFixed(2),
        p95: p95.toFixed(2),
        p50: p50.toFixed(2),
      },
      histogramData,
    ];
  }, [metrics]);

  const nf = new Intl.NumberFormat();

  return (
    <div className="card">
      <div className="rounded-lg overflow-hidden border border-card-border-inner">
        <table className="min-w-full divide-y divide-card-border-inner">
          <thead className="bg-secondary">
            <tr>
              <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-txtmain">
                Requests
              </th>
              <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-txtmain border-l border-card-border-inner">
                Processed
              </th>
              <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-txtmain border-l border-card-border-inner">
                Generated
              </th>
              <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-txtmain border-l border-card-border-inner">
                Token Stats (tokens/sec)
              </th>
            </tr>
          </thead>

          <tbody className="bg-surface divide-y divide-card-border-inner">
            <tr className="hover:bg-secondary">
              <td className="px-4 py-4 text-sm font-semibold text-gray-900 dark:text-white">{totalRequests}</td>

              <td className="px-4 py-4 text-sm text-gray-700 dark:text-gray-300 border-l border-gray-200 dark:border-white/10">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">{nf.format(totalInputTokens)}</span>
                  <span className="text-xs text-gray-500 dark:text-gray-400">tokens</span>
                </div>
              </td>

              <td className="px-4 py-4 text-sm text-gray-700 dark:text-gray-300 border-l border-gray-200 dark:border-white/10">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">{nf.format(totalOutputTokens)}</span>
                  <span className="text-xs text-gray-500 dark:text-gray-400">tokens</span>
                </div>
              </td>

              <td className="px-4 py-4 border-l border-gray-200 dark:border-white/10">
                <div className="space-y-3">
                  <div className="grid grid-cols-3 gap-2 items-center">
                    <div className="text-center">
                      <div className="text-xs text-gray-500 dark:text-gray-400">P50</div>
                      <div className="mt-1 inline-block rounded-full bg-gray-100 dark:bg-white/5 px-3 py-1 text-sm font-semibold text-gray-800 dark:text-white">
                        {tokenStats.p50}
                      </div>
                    </div>

                    <div className="text-center">
                      <div className="text-xs text-gray-500 dark:text-gray-400">P95</div>
                      <div className="mt-1 inline-block rounded-full bg-gray-100 dark:bg-white/5 px-3 py-1 text-sm font-semibold text-gray-800 dark:text-white">
                        {tokenStats.p95}
                      </div>
                    </div>

                    <div className="text-center">
                      <div className="text-xs text-gray-500 dark:text-gray-400">P99</div>
                      <div className="mt-1 inline-block rounded-full bg-gray-100 dark:bg-white/5 px-3 py-1 text-sm font-semibold text-gray-800 dark:text-white">
                        {tokenStats.p99}
                      </div>
                    </div>
                  </div>
                  {histogramData && <TokenHistogram data={histogramData} />}
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  );
}
