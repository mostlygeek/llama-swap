import { useAPI } from "../contexts/APIProvider";
import { useEffect, useState, useMemo } from "react";

type ConnectionStatus = "disconnected" | "connecting" | "connected";

const ConnectionStatus = () => {
  const { getConnectionStatus } = useAPI();
  const [eventStreamStatus, setEventStreamStatus] = useState<ConnectionStatus>("disconnected");

  useEffect(() => {
    const interval = setInterval(() => {
      setEventStreamStatus(getConnectionStatus());
    }, 1000);
    return () => clearInterval(interval);
  });

  const eventStatusColor = useMemo(() => {
    switch (eventStreamStatus) {
      case "connected":
        return "bg-green-500";
      case "connecting":
        return "bg-yellow-500";
      case "disconnected":
      default:
        return "bg-red-500";
    }
  }, [eventStreamStatus]);

  return (
    <div className="flex items-center" title={`event stream: ${eventStreamStatus}`}>
      <span className={`inline-block w-3 h-3 rounded-full ${eventStatusColor} mr-2`}></span>
    </div>
  );
};

export default ConnectionStatus;
