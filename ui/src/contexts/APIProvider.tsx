import { useRef, createContext, useState, useContext, useEffect, useCallback, useMemo, type ReactNode } from "react";

type ModelStatus = "ready" | "starting" | "stopping" | "stopped" | "shutdown" | "unknown";
const LOG_LENGTH_LIMIT = 1024 * 100; /* 100KB of log data */

export interface Model {
  id: string;
  state: ModelStatus;
}

interface APIProviderType {
  models: Model[];
  listModels: () => Promise<Model[]>;
  unloadAllModels: () => Promise<void>;
  enableProxyLogs: (enabled: boolean) => void;
  enableUpstreamLogs: (enabled: boolean) => void;
  enableModelUpdates: (enabled: boolean) => void;
  proxyLogs: string;
  upstreamLogs: string;
}

const APIContext = createContext<APIProviderType | undefined>(undefined);
type APIProviderProps = {
  children: ReactNode;
};

export function APIProvider({ children }: APIProviderProps) {
  const [proxyLogs, setProxyLogs] = useState("");
  const [upstreamLogs, setUpstreamLogs] = useState("");
  const proxyEventSource = useRef<EventSource | null>(null);
  const upstreamEventSource = useRef<EventSource | null>(null);

  const [models, setModels] = useState<Model[]>([]);
  const modelStatusEventSource = useRef<EventSource | null>(null);

  const appendLog = useCallback((newData: string, setter: React.Dispatch<React.SetStateAction<string>>) => {
    setter((prev) => {
      const updatedLog = prev + newData;
      return updatedLog.length > LOG_LENGTH_LIMIT ? updatedLog.slice(-LOG_LENGTH_LIMIT) : updatedLog;
    });
  }, []);

  const handleProxyMessage = useCallback(
    (e: MessageEvent) => {
      appendLog(e.data, setProxyLogs);
    },
    [proxyLogs, appendLog]
  );

  const handleUpstreamMessage = useCallback(
    (e: MessageEvent) => {
      appendLog(e.data, setUpstreamLogs);
    },
    [appendLog]
  );

  const enableProxyLogs = useCallback(
    (enabled: boolean) => {
      if (enabled) {
        const eventSource = new EventSource("/logs/streamSSE/proxy");
        eventSource.onmessage = handleProxyMessage;
        proxyEventSource.current = eventSource;
      } else {
        proxyEventSource.current?.close();
        proxyEventSource.current = null;
      }
    },
    [handleProxyMessage]
  );

  const enableUpstreamLogs = useCallback(
    (enabled: boolean) => {
      if (enabled) {
        const eventSource = new EventSource("/logs/streamSSE/upstream");
        eventSource.onmessage = handleUpstreamMessage;
        upstreamEventSource.current = eventSource;
      } else {
        upstreamEventSource.current?.close();
        upstreamEventSource.current = null;
      }
    },
    [upstreamEventSource, handleUpstreamMessage]
  );

  const enableModelUpdates = useCallback(
    (enabled: boolean) => {
      if (enabled) {
        const eventSource = new EventSource("/api/modelsSSE");
        eventSource.onmessage = (e: MessageEvent) => {
          try {
            const models = JSON.parse(e.data) as Model[];
            setModels(models);
          } catch (e) {
            console.error(e);
          }
        };
        modelStatusEventSource.current = eventSource;
      } else {
        modelStatusEventSource.current?.close();
        modelStatusEventSource.current = null;
      }
    },
    [setModels]
  );

  useEffect(() => {
    return () => {
      proxyEventSource.current?.close();
      upstreamEventSource.current?.close();
      modelStatusEventSource.current?.close();
    };
  }, []);

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
      const response = await fetch(`/api/models/unload/`, {
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

  const value = useMemo(
    () => ({
      models,
      listModels,
      unloadAllModels,
      enableProxyLogs,
      enableUpstreamLogs,
      enableModelUpdates,
      proxyLogs,
      upstreamLogs,
    }),
    [
      models,
      listModels,
      unloadAllModels,
      enableProxyLogs,
      enableUpstreamLogs,
      enableModelUpdates,
      proxyLogs,
      upstreamLogs,
    ]
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
