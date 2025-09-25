import { createContext, useState, useContext, useEffect, useCallback, useMemo, type ReactNode } from "react";
import type { ConnectionState } from "../lib/types";

type ModelStatus = "ready" | "starting" | "stopping" | "stopped" | "shutdown" | "unknown";
const LOG_LENGTH_LIMIT = 1024 * 100; /* 100KB of log data */

export interface Model {
  id: string;
  state: ModelStatus;
  name: string;
  description: string;
  unlisted: boolean;
}

interface APIProviderType {
  models: Model[];
  listModels: () => Promise<Model[]>;
  unloadAllModels: () => Promise<void>;
  unloadSingleModel: (model: string) => Promise<void>;
  loadModel: (model: string) => Promise<void>;
  enableAPIEvents: (enabled: boolean) => void;
  proxyLogs: string;
  upstreamLogs: string;
  metrics: Metrics[];
  connectionStatus: ConnectionState;
}

interface Metrics {
  id: number;
  timestamp: string;
  model: string;
  cache_tokens: number;
  input_tokens: number;
  output_tokens: number;
  prompt_per_second: number;
  tokens_per_second: number;
  duration_ms: number;
}

interface LogData {
  source: "upstream" | "proxy";
  data: string;
}
interface APIEventEnvelope {
  type: "modelStatus" | "logData" | "metrics";
  data: string;
}

const APIContext = createContext<APIProviderType | undefined>(undefined);
type APIProviderProps = {
  children: ReactNode;
  autoStartAPIEvents?: boolean;
};

let apiEventSource: EventSource | null = null;

export function APIProvider({ children, autoStartAPIEvents = true }: APIProviderProps) {
  const [proxyLogs, setProxyLogs] = useState("");
  const [upstreamLogs, setUpstreamLogs] = useState("");
  const [metrics, setMetrics] = useState<Metrics[]>([]);
  const [connectionStatus, setConnectionState] = useState<ConnectionState>("disconnected");
  //const apiEventSource = useRef<EventSource | null>(null);

  const [models, setModels] = useState<Model[]>([]);

  const appendLog = useCallback((newData: string, setter: React.Dispatch<React.SetStateAction<string>>) => {
    setter((prev) => {
      const updatedLog = prev + newData;
      return updatedLog.length > LOG_LENGTH_LIMIT ? updatedLog.slice(-LOG_LENGTH_LIMIT) : updatedLog;
    });
  }, []);

  const enableAPIEvents = useCallback((enabled: boolean) => {
    if (!enabled) {
      apiEventSource?.close();
      apiEventSource = null;
      setMetrics([]);
      return;
    }

    let retryCount = 0;
    const initialDelay = 1000; // 1 second

    const connect = () => {
      apiEventSource?.close();
      apiEventSource = new EventSource("/api/events");

      setConnectionState("connecting");

      apiEventSource.onopen = () => {
        // clear everything out on connect to keep things in sync
        setProxyLogs("");
        setUpstreamLogs("");
        setMetrics([]); // clear metrics on reconnect
        setModels([]); // clear models on reconnect
        retryCount = 0;
        setConnectionState("connected");
      };

      apiEventSource.onmessage = (e: MessageEvent) => {
        try {
          const message = JSON.parse(e.data) as APIEventEnvelope;
          switch (message.type) {
            case "modelStatus":
              {
                const models = JSON.parse(message.data) as Model[];

                // sort models by name and id
                models.sort((a, b) => {
                  return (a.name + a.id).localeCompare(b.name + b.id);
                });

                setModels(models);
              }
              break;

            case "logData":
              const logData = JSON.parse(message.data) as LogData;
              switch (logData.source) {
                case "proxy":
                  appendLog(logData.data, setProxyLogs);
                  break;
                case "upstream":
                  appendLog(logData.data, setUpstreamLogs);
                  break;
              }
              break;

            case "metrics":
              {
                const newMetrics = JSON.parse(message.data) as Metrics[];
                setMetrics((prevMetrics) => {
                  return [...newMetrics, ...prevMetrics];
                });
              }
              break;
          }
        } catch (err) {
          console.error(e.data, err);
        }
      };

      apiEventSource.onerror = () => {
        apiEventSource?.close();
        retryCount++;
        const delay = Math.min(initialDelay * Math.pow(2, retryCount - 1), 5000);
        setConnectionState("disconnected");
        setTimeout(connect, delay);
      };
    };

    connect();
  }, []);

  useEffect(() => {
    if (autoStartAPIEvents) {
      enableAPIEvents(true);
    }

    return () => {
      enableAPIEvents(false);
    };
  }, [enableAPIEvents, autoStartAPIEvents]);

  const listModels = useCallback(async (): Promise<Model[]> => {
    try {
      const response = await fetch("/api/models/");
      if (!response.ok) {
        throw new Error(`HTTP error! status: ${response.status}`);
      }
      const data = await response.json();
      return data || [];
    } catch (error) {
      console.error("Failed to fetch models:", error);
      return []; // Return empty array as fallback
    }
  }, []);

  const unloadAllModels = useCallback(async () => {
    try {
      const response = await fetch(`/api/models/unload`, {
        method: "POST",
      });
      if (!response.ok) {
        throw new Error(`Failed to unload models: ${response.status}`);
      }
    } catch (error) {
      console.error("Failed to unload models:", error);
      throw error; // Re-throw to let calling code handle it
    }
  }, []);

  const unloadSingleModel = useCallback(async (model: string) => {
    try {
      const response = await fetch(`/api/models/unload/${model}`, {
        method: "POST",
      });
      if (!response.ok) {
        throw new Error(`Failed to unload model: ${response.status}`);
      }
    } catch (error) {
      console.error("Failed to unload model", model, error);
      throw error;
    }
  }, []);

  const loadModel = useCallback(async (model: string) => {
    try {
      const response = await fetch(`/upstream/${model}/`, {
        method: "GET",
      });
      if (!response.ok) {
        throw new Error(`Failed to load model: ${response.status}`);
      }
    } catch (error) {
      console.error("Failed to load model:", error);
      throw error; // Re-throw to let calling code handle it
    }
  }, []);

  const value = useMemo(
    () => ({
      models,
      listModels,
      unloadAllModels,
      unloadSingleModel,
      loadModel,
      enableAPIEvents,
      proxyLogs,
      upstreamLogs,
      metrics,
      connectionStatus,
    }),
    [models, listModels, unloadAllModels, loadModel, enableAPIEvents, proxyLogs, upstreamLogs, metrics]
  );

  return <APIContext.Provider value={value}>{children}</APIContext.Provider>;
}

export function useAPI() {
  const context = useContext(APIContext);
  if (context === undefined) {
    throw new Error("useAPI must be used within an APIProvider");
  }
  return context;
}
