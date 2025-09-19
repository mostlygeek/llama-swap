import { useAPI } from "../contexts/APIProvider";
import { useMemo } from "react";

const ConnectionStatusIcon = () => {
  const { connectionStatus } = useAPI();

  const eventStatusColor = useMemo(() => {
    switch (connectionStatus) {
      case "connected":
        return "bg-emerald-500";
      case "connecting":
        return "bg-amber-500";
      case "disconnected":
      default:
        return "bg-red-500";
    }
  }, [connectionStatus]);

  return (
    <div className="flex items-center" title={`event stream: ${connectionStatus}`}>
      <span className={`inline-block w-3 h-3 rounded-full ${eventStatusColor} mr-2`}></span>
    </div>
  );
};

export default ConnectionStatusIcon;
