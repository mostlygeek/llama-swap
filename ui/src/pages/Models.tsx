import { useState, useCallback, useMemo } from "react";
import { useAPI } from "../contexts/APIProvider";
import { LogPanel } from "./LogViewer";
import { usePersistentState } from "../hooks/usePersistentState";
import { Panel, PanelGroup, PanelResizeHandle } from "react-resizable-panels";
import { useTheme } from "../contexts/ThemeProvider";
import { RiEyeFill, RiEyeOffFill, RiStopCircleLine, RiSwapBoxFill } from "react-icons/ri";

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
  const { models, loadModel, unloadAllModels } = useAPI();
  const [isUnloading, setIsUnloading] = useState(false);
  const [showUnlisted, setShowUnlisted] = usePersistentState("showUnlisted", true);
  const [showIdorName, setShowIdorName] = usePersistentState<"id" | "name">("showIdorName", "id"); // true = show ID, false = show name

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
        <h2>Models</h2>
        <div className="flex justify-between">
          <div className="flex gap-2">
            <button className="btn flex items-center gap-2" onClick={toggleIdorName} style={{ lineHeight: "1.2" }}>
              <RiSwapBoxFill /> {showIdorName === "id" ? "ID" : "Name"}
            </button>

            <button
              className="btn flex items-center gap-2"
              onClick={() => setShowUnlisted(!showUnlisted)}
              style={{ lineHeight: "1.2" }}
            >
              {showUnlisted ? <RiEyeFill /> : <RiEyeOffFill />} unlisted
            </button>
          </div>
          <button className="btn flex items-center gap-2" onClick={handleUnloadAllModels} disabled={isUnloading}>
            <RiStopCircleLine size="24" /> {isUnloading ? "Unloading..." : "Unload"}
          </button>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto">
        <table className="w-full">
          <thead className="sticky top-0 bg-card z-10">
            <tr className="border-b border-primary bg-surface">
              <th className="text-left p-2">{showIdorName === "id" ? "Model ID" : "Name"}</th>
              <th className="text-left p-2"></th>
              <th className="text-left p-2">State</th>
            </tr>
          </thead>
          <tbody>
            {filteredModels.map((model) => (
              <tr key={model.id} className="border-b hover:bg-secondary-hover border-border">
                <td className={`p-2 ${model.unlisted ? "text-txtsecondary" : ""}`}>
                  <a href={`/upstream/${model.id}/`} className={`underline`} target="_blank">
                    {showIdorName === "id" ? model.id : model.name !== "" ? model.name : model.id}
                  </a>
                  {model.description !== "" && (
                    <p className={model.unlisted ? "text-opacity-70" : ""}>
                      <em>{model.description}</em>
                    </p>
                  )}
                </td>
                <td className="p-2 w-[50px]">
                  <button
                    className="btn btn--sm"
                    disabled={model.state !== "stopped"}
                    onClick={() => loadModel(model.id)}
                  >
                    Load
                  </button>
                </td>
                <td className="p-2 w-[75px]">
                  <span className={`status status--${model.state}`}>{model.state}</span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function StatsPanel() {
  const { metrics } = useAPI();

  const [totalRequests, totalInputTokens, totalOutputTokens, avgTokensPerSecond] = useMemo(() => {
    const totalRequests = metrics.length;
    if (totalRequests === 0) {
      return [0, 0, 0];
    }
    const totalInputTokens = metrics.reduce((sum, m) => sum + m.input_tokens, 0);
    const totalOutputTokens = metrics.reduce((sum, m) => sum + m.output_tokens, 0);
    const avgTokensPerSecond = (metrics.reduce((sum, m) => sum + m.tokens_per_second, 0) / totalRequests).toFixed(2);
    return [totalRequests, totalInputTokens, totalOutputTokens, avgTokensPerSecond];
  }, [metrics]);

  return (
    <div className="card">
      <div className="rounded-lg overflow-hidden border border-gray-200">
        <table className="w-full">
          <tbody>
            <tr>
              <th className="p-2 font-medium border-b border-gray-200 text-right">Requests</th>
              <th className="p-2 font-medium border-l border-b border-gray-200 text-right">Processed</th>
              <th className="p-2 font-medium border-l border-b border-gray-200 text-right">Generated</th>
              <th className="p-2 font-medium border-l border-b border-gray-200 text-right">Tokens/Sec</th>
            </tr>
            <tr>
              <td className="p-2 text-right border-r border-gray-200">{totalRequests}</td>
              <td className="p-2 text-right border-r border-gray-200">
                {new Intl.NumberFormat().format(totalInputTokens)}
              </td>
              <td className="p-2 text-right border-r border-gray-200">
                {new Intl.NumberFormat().format(totalOutputTokens)}
              </td>
              <td className="p-2 text-right">{avgTokensPerSecond}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  );
}
