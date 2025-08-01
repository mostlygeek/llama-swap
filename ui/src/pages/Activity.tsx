import { useState, useEffect, Fragment } from "react";
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
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (metrics.length > 0) {
      setError(null);
    }
  }, [metrics]);

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

  const renderMetricRow = (metric: typeof metrics[0], index: number) => {
    const [isExpanded, setIsExpanded] = useState(false);
    const hasRequestData = metric.request_body && metric.response_body;

    return (
      <Fragment key={`${metric.id}-${index}`}>
        <tr>
          <td className="px-6 py-4 whitespace-nowrap text-sm">{formatTimestamp(metric.timestamp)}</td>
          <td className="px-6 py-4 whitespace-nowrap text-sm">{metric.model}</td>
          <td className="px-6 py-4 whitespace-nowrap text-sm">{metric.input_tokens.toLocaleString()}</td>
          <td className="px-6 py-4 whitespace-nowrap text-sm">{metric.output_tokens.toLocaleString()}</td>
          <td className="px-6 py-4 whitespace-nowrap text-sm">{formatSpeed(metric.tokens_per_second)}</td>
          <td className="px-6 py-4 whitespace-nowrap text-sm">{formatDuration(metric.duration_ms)}</td>
          {hasRequestData && (
            <td className="px-6 py-4 whitespace-nowrap text-sm">
              <button
                onClick={() => setIsExpanded(!isExpanded)}
                className="text-blue-600 hover:text-blue-800 text-sm font-medium"
              >
                {isExpanded ? 'Hide' : 'Show'}
              </button>
            </td>
          )}
        </tr>
        {isExpanded && hasRequestData && (
          <tr>
            <td colSpan={7} className="px-6 py-4 bg-gray-50 border-t">
              <div className="mt-2">
                <h4 className="font-bold text-sm mb-2">Request</h4>
                <pre className="bg-white p-3 rounded border text-sm whitespace-pre-wrap break-all max-h-40 overflow-y-auto">
                  <code>{metric.request_body}</code>
                </pre>
                <h4 className="font-bold text-sm mt-4 mb-2">Response</h4>
                <pre className="bg-white p-3 rounded border text-sm whitespace-pre-wrap break-all max-h-40 overflow-y-auto">
                  <code>{metric.response_body}</code>
                </pre>
              </div>
            </td>
          </tr>
        )}
      </Fragment>
    );
  };

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
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">Generation Speed</th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">Duration</th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">Request data</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {metrics.map(renderMetricRow)}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
};

export default ActivityPage;
