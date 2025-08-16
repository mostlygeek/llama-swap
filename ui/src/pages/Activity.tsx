import { useMemo } from "react";
import { useAPI } from "../contexts/APIProvider";

const formatTimestamp = (timestamp: string): string => {
  return new Date(timestamp).toLocaleString();
};

const formatSpeed = (speed: number): string => {
  return speed < 0 ? "unknown" : speed.toFixed(2) + " t/s";
};

const formatDuration = (ms: number): string => {
  return (ms / 1000).toFixed(2) + "s";
};

const ActivityPage = () => {
  const { metrics } = useAPI();
  const sortedMetrics = useMemo(() => {
    return [...metrics].sort((a, b) => b.id - a.id);
  }, [metrics]);

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
                <th className="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider">Id</th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">Timestamp</th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">Model</th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">Input Tokens</th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">Output Tokens</th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">Prompt Processing</th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">Generation Speed</th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">Duration</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {sortedMetrics.map((metric) => (
                <tr key={`metric_${metric.id}`}>
                  <td className="px-4 py-4 whitespace-nowrap text-sm">{metric.id + 1 /* un-zero index */}</td>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">{formatTimestamp(metric.timestamp)}</td>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">{metric.model}</td>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">{metric.input_tokens.toLocaleString()}</td>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">{metric.output_tokens.toLocaleString()}</td>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">{formatSpeed(metric.prompt_per_second)}</td>
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
