import type { Model } from "../services/api";
import { fetchModels } from "../services/api";
import { useState, useEffect } from "react";

export default function ModelsPage() {
  const [models, setModels] = useState<Model[]>([]);

  useEffect(() => {
    const fetchData = () => {
      fetchModels()
        .then((data) => {
          setModels(data);
        })
        .catch((error) => {
          console.error("Error fetching models:", error);
        });
    };

    // Initial fetch
    fetchData();

    // Set up interval for periodic fetching
    const intervalId = setInterval(fetchData, 3000);

    // Clean up interval on component unmount
    return () => clearInterval(intervalId);
  }, []);

  return (
    <div className="h-screen">
      <div className="flex flex-col md:flex-row gap-4 h-full">
        {/* Left Column */}
        <div className="w-full md:w-1/2 h-[80%] flex items-top">
          <div className="card h-[90%] w-full">
            <h2 className="">Models</h2>
            <table className="w-full mt-4">
              <thead>
                <tr className="border-b">
                  <th className="text-left p-2">Name</th>
                  <th className="text-left p-2">State</th>
                  <th className="text-left p-2">Options</th>
                </tr>
              </thead>
              <tbody>
                {models.map((model) => (
                  <tr key={model.id} className="border-b hover:bg-gray-50">
                    <td className="p-2">{model.id}</td>
                    <td className="p-2">
                      <span className={`status status--${model.state.toLowerCase()}`}>{model.state}</span>
                    </td>
                    <td className="p-2">
                      {model.state === "Ready" && (
                        <button
                          className="btn"
                          onClick={() => {
                            console.log("stopping model", model.id);
                          }}
                        >
                          Stop
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>

        {/* Right Column */}
        <div className="w-full md:w-1/2 h-[80%] flex items-top">
          <div className="card h-[90%] w-full">
            <h2>Stats</h2>
            <div className="card mt-4">
              <h3>...</h3>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
