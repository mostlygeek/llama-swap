import { useState, useEffect } from 'react';

interface Metric {
  id: number;
  timestamp: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  duration_ms: number;
  tokens_per_second: number;
}

const ActivityPage = () => {
  const [metrics, setMetrics] = useState<Metric[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const maxShownMetrics = 1000;

  const fetchMetrics = async () => {
    try {
      const response = await fetch('/api/metrics');
      if (!response.ok) {
        throw new Error('Failed to fetch metrics');
      }
      const data = await response.json();
      if (data) {
        data.reverse();
        setMetrics(data);
      } else {
        setMetrics([]);
      }
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load metrics');
    } finally {
      setLoading(false);
    }
  };

  const setupStreaming = () => {
    const controller = new AbortController();

    const streamMetrics = async () => {
      // Continuously stream
      try {
        const response = await fetch('/api/metrics/stream', {
          signal: controller.signal,
        });

        if (!response.ok) {
          throw new Error('Failed to connect to metrics stream');
        }

        const reader = response.body?.getReader();
        if (!reader) {
          throw new Error('No response body');
        }

        setLoading(false);
        setError(null);

        const decoder = new TextDecoder();
        let buffer = '';

        while (true) {
          const { done, value } = await reader.read();

          if (done) break;

          buffer += decoder.decode(value, { stream: true });

          // Process complete lines
          const lines = buffer.split('\n');
          buffer = lines.pop() || ''; // Keep incomplete line in buffer

          for (const line of lines) {
            const trimmedLine = line.trim();
            if (trimmedLine) {
              try {
                const newMetric: Metric = JSON.parse(trimmedLine);
                setMetrics(prevMetrics => {
                  const updatedMetrics = [newMetric, ...prevMetrics];
                  return updatedMetrics.slice(0, maxShownMetrics);
                });
              } catch (err) {
                console.error('Error parsing metrics data:', err);
              }
            }
          }
        }
      } catch (err) {
        const error = err as Error;
        if (error.name !== 'AbortError') {
          console.error('Streaming error:', error);
          // Fallback to polling if streaming fails
          fetchMetrics();
        }
      }
    };

    streamMetrics();

    // Cleanup on unmount
    return () => {
      controller.abort();
    };
  };
  useEffect(setupStreaming, []);

  const formatTimestamp = (timestamp: string) => {
    return new Date(timestamp).toLocaleString();
  };

  const formatSpeed = (speed: number) => {
    return speed.toFixed(2) + ' t/s';
  };

  const formatDuration = (ms: number) => {
    return (ms / 1000).toFixed(2) + 's';
  };

  if (loading) {
    return (
      <div className="p-6">
        <h1 className="text-2xl font-bold mb-4">Activity</h1>
        <div className="text-center py-8">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-gray-900 mx-auto"></div>
          <p className="mt-2 text-gray-600">Loading metrics...</p>
        </div>
      </div>
    );
  }

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
          <p className="text-sm mt-2">
            Ensure metrics logging is enabled in your configuration
          </p>
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="min-w-full divide-y">
            <thead>
              <tr>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">
                  Timestamp
                </th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">
                  Model
                </th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">
                  Input Tokens
                </th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">
                  Output Tokens
                </th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">
                  Processing Speed
                </th>
                <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider">
                  Duration
                </th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {metrics.map((metric, index) => (
                <tr key={`${metric.id}-${index}`}>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">
                    {formatTimestamp(metric.timestamp)}
                  </td>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">
                    {metric.model}
                  </td>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">
                    {metric.input_tokens ? metric.input_tokens.toLocaleString() : '-'}
                  </td>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">
                    {metric.output_tokens ? metric.output_tokens.toLocaleString() : '-'}
                  </td>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">
                    {formatSpeed(metric.tokens_per_second)}
                  </td>
                  <td className="px-6 py-4 whitespace-nowrap text-sm">
                    {formatDuration(metric.duration_ms)}
                  </td>
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
