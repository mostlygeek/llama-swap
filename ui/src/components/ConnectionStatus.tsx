import { useAPI } from "../contexts/APIProvider";
import { useMemo, useEffect } from "react";

const ConnectionStatusIcon = () => {
  const { connectionStatus, versionInfo, getVersionInfo } = useAPI();

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

  useEffect(() => {
    if (typeof connectionStatus === "string" &&
        connectionStatus === "connected") {
      getVersionInfo();
    }
  }, [connectionStatus, getVersionInfo]);

  const title = useMemo(() => {
    let baseTitle = `Event Stream: ${connectionStatus}`;
    if (versionInfo) {
      baseTitle += `\nVersion: ${versionInfo.version}\nCommit: ${versionInfo.commit}\nDate: ${versionInfo.date}`;
    }
    return baseTitle;
  }, [connectionStatus, versionInfo]);

  return (
    <div className="flex items-center" title={`${title}`}>
      <span className={`inline-block w-3 h-3 rounded-full ${eventStatusColor} mr-2`}></span>
    </div>
  );
};

export default ConnectionStatusIcon;
