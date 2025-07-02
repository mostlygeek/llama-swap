import { useState, useEffect, useCallback, useMemo } from "react";
import { useAPI } from "../contexts/APIProvider";
import { LogPanel } from "./LogViewer";
import { processEvalTimes } from "../lib/Utils";

export default function ModelsPage() {
  const { models, unloadAllModels, loadModel, upstreamLogs, enableAPIEvents } = useAPI();
  const [isUnloading, setIsUnloading] = useState(false);

  useEffect(() => {
    enableAPIEvents(true);
    return () => {
      enableAPIEvents(false);
    };
  }, []);

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

  const [totalLines, totalTokens, avgTokensPerSecond] = useMemo(() => {
    return processEvalTimes(upstreamLogs);
  }, [upstreamLogs]);

  return (
    <div>
      <div className="flex flex-col md:flex-row gap-4">
        {/* Left Column */}
        <div className="w-full md:w-1/2 flex items-top">
          <div className="card w-full">
            <h2 className="">Models</h2>
            <button className="btn" onClick={handleUnloadAllModels} disabled={isUnloading}>
              {isUnloading ? "Unloading..." : "Unload All Models"}
            </button>
            <table className="w-full mt-4">
              <thead>
                <tr className="border-b border-primary">
                  <th className="text-left p-2">Name</th>
                  <th className="text-left p-2"></th>
                  <th className="text-left p-2">State</th>
                </tr>
              </thead>
              <tbody>
                {models.map((model) => (
                  <tr key={model.id} className="border-b hover:bg-secondary-hover border-border">
                    <td className="p-2">
                      <a href={`/upstream/${model.id}/`} className="underline" target="_blank">
                        {model.id}
                      </a>
                    </td>
                    <td className="p-2">
                      <button
                        className="btn btn--sm"
                        disabled={model.state !== "stopped"}
                        onClick={() => loadModel(model.id)}
                      >
                        Load
                      </button>
                    </td>
                    <td className="p-2">
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
          <div className="card mb-4 min-h-[250px]">
            <h2>Log Stats</h2>
            <p className="italic my-2">note: eval logs from llama-server</p>
            <table className="w-full border border-gray-200">
              <tbody>
                <tr className="border-b border-gray-200">
                  <td className="py-2 px-4 font-medium border-r border-gray-200">Requests</td>
                  <td className="py-2 px-4 text-right">{totalLines}</td>
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
