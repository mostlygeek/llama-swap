import { useTheme } from "../contexts/ThemeProvider";
import { useMemo } from "react";

const ConnectionStatus = () => {
  const { connectionState } = useTheme(); //

  const eventStatusColor = useMemo(() => {
    switch (connectionState) {
      case "connected":
        return "bg-green-500";
      case "connecting":
        return "bg-yellow-500";
      case "disconnected":
      default:
        return "bg-red-500";
    }
  }, [connectionState]);

  return (
    <div className="flex items-center" title={`event stream: ${connectionState}`}>
      <span className={`inline-block w-3 h-3 rounded-full ${eventStatusColor} mr-2`}></span>
    </div>
  );
};

export default ConnectionStatus;
