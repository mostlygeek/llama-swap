import { useState, useCallback, useMemo } from "react";
import { useAPI } from "../contexts/APIProvider";
import { LogPanel } from "./LogViewer";
import { usePersistentState } from "../hooks/usePersistentState";
import { Panel, PanelGroup, PanelResizeHandle } from "react-resizable-panels";
import { useTheme } from "../contexts/ThemeProvider";
import { RiEyeFill, RiEyeOffFill, RiStopCircleLine } from "react-icons/ri";

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

  return (
    <div className="card h-full flex flex-col">
      <div className="shrink-0">
        <h2>Models</h2>
        <div className="flex justify-between">
          <button
            className="btn flex items-center gap-2"
            onClick={() => setShowUnlisted(!showUnlisted)}
            style={{ lineHeight: "1.2" }}
          >
            {showUnlisted ? <RiEyeFill /> : <RiEyeOffFill />} unlisted
          </button>
          <button className="btn flex items-center gap-2" onClick={handleUnloadAllModels} disabled={isUnloading}>
            <RiStopCircleLine size="24" /> {isUnloading ? "Unloading..." : "Unload"}
          </button>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto">
        <table className="w-full">
          <thead className="sticky top-0 bg-card z-10">
            <tr className="border-b border-primary bg-surface">
              <th className="text-left p-2">Name</th>
              <th className="text-left p-2"></th>
              <th className="text-left p-2">State</th>
            </tr>
          </thead>
          <tbody>
            {filteredModels.map((model) => (
              <tr key={model.id} className="border-b hover:bg-secondary-hover border-border">
                <td className={`p-2 ${model.unlisted ? "text-txtsecondary" : ""}`}>
                  <a href={`/upstream/${model.id}/`} className={`underline`} target="_blank">
                    {model.name !== "" ? model.name : model.id}
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

  const [totalRequests, totalTokens, avgTokensPerSecond] = useMemo(() => {
    const totalRequests = metrics.length;
    if (totalRequests === 0) {
      return [0, 0, 0];
    }
    const totalTokens = metrics.reduce((sum, m) => sum + m.output_tokens, 0);
    const avgTokensPerSecond = (metrics.reduce((sum, m) => sum + m.tokens_per_second, 0) / totalRequests).toFixed(2);
    return [totalRequests, totalTokens, avgTokensPerSecond];
  }, [metrics]);

  return (
    <div className="card">
      <h2>Chat Activity</h2>
      <table className="w-full border border-gray-200">
        <tbody>
          <tr className="border-b border-gray-200">
            <td className="py-2 px-4 font-medium border-r border-gray-200">Requests</td>
            <td className="py-2 px-4 text-right">{totalRequests}</td>
          </tr>
          <tr className="border-b border-gray-200">
            <td className="py-2 px-4 font-medium border-r border-gray-200">Total Tokens Generated</td>
            <td className="py-2 px-4 text-right">{totalTokens}</td>
          </tr>
          <tr>
            <td className="py-2 px-4 font-medium border-r border-gray-200">Average Tokens/Second</td>
            <td className="py-2 px-4 text-right">{avgTokensPerSecond}</td>
          </tr>
        </tbody>
      </table>
    </div>
  );
}
