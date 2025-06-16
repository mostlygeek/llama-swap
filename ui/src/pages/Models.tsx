import { useState, useEffect, useCallback } from "react";
import { useAPI } from "../contexts/APIProvider";

export default function ModelsPage() {
  const { models, enableModelUpdates, unloadAllModels } = useAPI();
  const [isUnloading, setIsUnloading] = useState(false);

  useEffect(() => {
    enableModelUpdates(true);
    return () => {
      enableModelUpdates(false);
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
      <div className="flex flex-col md:flex-row gap-4 h-full">
        {/* Left Column */}
        <div className="w-full md:w-1/2 h-[80%] flex items-top">
          <div className="card h-[90%] w-full">
            <h2 className="">Models</h2>
            <button className="btn" onClick={handleUnloadAllModels} disabled={isUnloading}>
              {isUnloading ? "Unloading..." : "Unload All Models"}
            </button>
            <table className="w-full mt-4">
              <thead>
                <tr className="border-b">
                  <th className="text-left p-2">Name</th>
                  <th className="text-left p-2">State</th>
                </tr>
              </thead>
              <tbody>
                {models.map((model) => (
                  <tr key={model.id} className="border-b hover:bg-gray-50">
                    <td className="p-2">{model.id}</td>
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
        <div className="w-full md:w-1/2 h-[80%] flex items-top">
          <div className="card h-[90%] w-full"></div>
        </div>
      </div>
    </div>
  );
}
