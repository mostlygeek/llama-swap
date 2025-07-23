import { useState, useCallback, useMemo } from "react";
import { useAPI } from "../contexts/APIProvider";
import { LogPanel } from "./LogViewer";
import { usePersistentState } from "../hooks/usePersistentState";

export default function ModelsPage() {
  const { models, unloadAllModels, loadModel, upstreamLogs, metrics } = useAPI();
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
      // at least give it a second to show the unloading message
      setTimeout(() => {
        setIsUnloading(false);
      }, 1000);
    }
  }, []);

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
    <div>
      <div className="flex flex-col md:flex-row gap-4">
        {/* Left Column */}
        <div className="w-full md:w-1/2 flex items-top">
          <div className="card w-full">
            <h2 className="">Models</h2>
            <div className="flex justify-between">
              <button className="btn" onClick={() => setShowUnlisted(!showUnlisted)} style={{ lineHeight: "1.2" }}>
                {showUnlisted ? "🟢 unlisted" : "⚫️ unlisted"}
              </button>
              <button className="btn" onClick={handleUnloadAllModels} disabled={isUnloading}>
                {isUnloading ? "Stopping ..." : "Stop All"}
              </button>
            </div>

            <table className="w-full mt-4">
              <thead>
                <tr className="border-b border-primary">
                  <th className="text-left p-2">Name</th>
                  <th className="text-left p-2"></th>
                  <th className="text-left p-2">State</th>
                </tr>
              </thead>
              <tbody>
                {filteredModels.map((model) => (
                  <tr key={model.id} className="border-b hover:bg-secondary-hover border-border">
                    <td className="p-2">
                      <a href={`/upstream/${model.id}/`} className="underline" target="_blank">
                        {model.name !== "" ? model.name : model.id}
                      </a>
                      {model.description != "" && (
                        <p>
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

        {/* Right Column */}
        <div className="w-full md:w-1/2 flex flex-col" style={{ height: "calc(100vh - 125px)" }}>
          <div className="card mb-4 min-h-[225px]">
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

          <LogPanel id="modelsupstream" title="Upstream Logs" logData={upstreamLogs} />
        </div>
      </div>
    </div>
  );
}
