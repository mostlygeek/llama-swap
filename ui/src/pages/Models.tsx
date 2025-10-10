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
      <div className="rounded-lg overflow-hidden border border-gray-200 dark:border-white/10">
        <table className="w-full">
          <thead>
            <tr className="border-b border-gray-200 dark:border-white/10 text-right">
              <th>Requests</th>
              <th className="border-l border-gray-200 dark:border-white/10">Processed</th>
              <th className="border-l border-gray-200 dark:border-white/10">Generated</th>
              <th className="border-l border-gray-200 dark:border-white/10">Tokens/Sec</th>
            </tr>
          </thead>
          <tbody>
            <tr className="text-right">
              <td className="border-r border-gray-200 dark:border-white/10">{totalRequests}</td>
              <td className="border-r border-gray-200 dark:border-white/10">
                {new Intl.NumberFormat().format(totalInputTokens)}
              </td>
              <td className="border-r border-gray-200 dark:border-white/10">
                {new Intl.NumberFormat().format(totalOutputTokens)}
              </td>
              <td>{avgTokensPerSecond}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  );
}
