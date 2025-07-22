import { useState, useEffect } from "react";
import { useAPI } from "../contexts/APIProvider";

const ActivityPage = () => {
  const { metrics } = useAPI();
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (metrics.length > 0) {
      setError(null);
    }
  }, [metrics]);

  const formatTimestamp = (timestamp: string) => {
    return new Date(timestamp).toLocaleString();
  };

  const formatSpeed = (speed: number) => {
    return speed.toFixed(2) + " t/s";
  };

  const formatDuration = (ms: number) => {
    return (ms / 1000).toFixed(2) + "s";
  };

  if (error) {
    return (
      <div className="p-6">
        <h1 className="text-2xl font-bold mb-4">Activity</h1>
        <div className="bg-red-50 border border-red-200 rounded-md p-4">
          <p className="text-red-800">{error}</p>
        </div>
      </div>
    );
  }

  return (
    <div className="p-6">
      <h1 className="text-2xl font-bold mb-4">Activity</h1>

      {metrics.length === 0 ? (
        <div className="text-center py-8">
          <p className="text-gray-600">No metrics data available</p>
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="min-w-full divide-y">
            <thead>
              <tr>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">Timestamp</th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">Model</th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">Input Tokens</th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">Output Tokens</th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">Processing Speed</th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">Duration</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {metrics.map((metric, index) => (
                <tr key={`${metric.id}-${index}`}>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">{formatTimestamp(metric.timestamp)}</td>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">{metric.model}</td>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">{metric.input_tokens.toLocaleString()}</td>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">{metric.output_tokens.toLocaleString()}</td>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">{formatSpeed(metric.tokens_per_second)}</td>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">{formatDuration(metric.duration_ms)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
};

export default ActivityPage;
