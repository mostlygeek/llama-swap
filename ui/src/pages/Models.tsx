import { useState, useEffect, useCallback } from "react";
import { useAPI } from "../contexts/APIProvider";
import { LogPanel } from "./LogViewer";

export default function ModelsPage() {
  const { models, enableModelUpdates, unloadAllModels, loadModel, upstreamLogs, enableUpstreamLogs } = useAPI();
  const [isUnloading, setIsUnloading] = useState(false);

  useEffect(() => {
    enableModelUpdates(true);
    enableUpstreamLogs(true);
    return () => {
      enableModelUpdates(false);
      enableUpstreamLogs(false);
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

  return (
    <div className="h-screen">
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
                      <button className="btn btn--sm" disabled={model.state !== "stopped"} onClick={() => loadModel(model.id)}>Load</button>
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
        <div className="w-full md:w-1/2  flex items-top">
          <LogPanel id="modelsupstream" title="Upstream Logs" logData={upstreamLogs} className="h-full" />
        </div>
      </div>
    </div>
  );
}
