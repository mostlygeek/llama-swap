import { useMemo } from "react";
import { useAPI } from "../contexts/APIProvider";

const formatSpeed = (speed: number): string => {
  return speed < 0 ? "unknown" : speed.toFixed(2) + " t/s";
};

const formatDuration = (ms: number): string => {
  return (ms / 1000).toFixed(2) + "s";
};

const formatRelativeTime = (timestamp: string): string => {
  const now = new Date();
  const date = new Date(timestamp);
  const diffInSeconds = Math.floor((now.getTime() - date.getTime()) / 1000);

  // Handle future dates by returning "just now"
  if (diffInSeconds < 5) {
    return "now";
  }

  if (diffInSeconds < 60) {
    return `${diffInSeconds}s ago`;
  }

  const diffInMinutes = Math.floor(diffInSeconds / 60);
  if (diffInMinutes < 60) {
    return `${diffInMinutes}m ago`;
  }

  const diffInHours = Math.floor(diffInMinutes / 60);
  if (diffInHours < 24) {
    return `${diffInHours}h ago`;
  }

  return "a while ago";
};

const ActivityPage = () => {
  const { metrics } = useAPI();
  const sortedMetrics = useMemo(() => {
    return [...metrics].sort((a, b) => b.id - a.id);
  }, [metrics]);

  return (
    <div className="p-2">
      <h1 className="text-2xl font-bold">Activity</h1>

      {metrics.length === 0 && (
        <div className="text-center py-8">
          <p className="text-gray-600">No metrics data available</p>
        </div>
      )}
      {metrics.length > 0 && (
        <div className="card overflow-auto">
          <table className="min-w-full divide-y">
            <thead className="border-gray-200 dark:border-white/10">
              <tr className="text-left text-xs uppercase tracking-wider">
                <th className="px-6 py-3">ID</th>
                <th className="px-6 py-3">Time</th>
                <th className="px-6 py-3">Model</th>
                <th className="px-6 py-3">
                  Cached <Tooltip content="prompt tokens from cache" />
                </th>
                <th className="px-6 py-3">
                  Prompt <Tooltip content="new prompt tokens processed" />
                </th>
                <th className="px-6 py-3">Generated</th>
                <th className="px-6 py-3">Prompt Processing</th>
                <th className="px-6 py-3">Generation Speed</th>
                <th className="px-6 py-3">Duration</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {sortedMetrics.map((metric) => (
                <tr key={metric.id} className="whitespace-nowrap text-sm border-gray-200 dark:border-white/10">
                  <td className="px-4 py-4">{metric.id + 1 /* un-zero index */}</td>
                  <td className="px-6 py-4">{formatRelativeTime(metric.timestamp)}</td>
                  <td className="px-6 py-4">{metric.model}</td>
                  <td className="px-6 py-4">{metric.cache_tokens > 0 ? metric.cache_tokens.toLocaleString() : "-"}</td>
                  <td className="px-6 py-4">{metric.input_tokens.toLocaleString()}</td>
                  <td className="px-6 py-4">{metric.output_tokens.toLocaleString()}</td>
                  <td className="px-6 py-4">{formatSpeed(metric.prompt_per_second)}</td>
                  <td className="px-6 py-4">{formatSpeed(metric.tokens_per_second)}</td>
                  <td className="px-6 py-4">{formatDuration(metric.duration_ms)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
};

interface TooltipProps {
  content: string;
}

const Tooltip: React.FC<TooltipProps> = ({ content }) => {
  return (
    <div className="relative group inline-block">
      ⓘ
      <div
        className="absolute top-full left-1/2 transform -translate-x-1/2 mt-2
                     px-3 py-2 bg-gray-900 text-white text-sm rounded-md
                     opacity-0 group-hover:opacity-100 transition-opacity
                     duration-200 pointer-events-none whitespace-nowrap z-50 normal-case"
      >
        {content}
        <div
          className="absolute bottom-full left-1/2 transform -translate-x-1/2
                       border-4 border-transparent border-b-gray-900"
        ></div>
      </div>
    </div>
  );
};

export default ActivityPage;
