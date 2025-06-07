import React, { useState, useEffect, useRef } from "react";

function LogViewer() {
  return (
    <div className="logs-container">
      <div className="log-panel">
        <div className="log-panel-header">
          <div className="log-panel-title">
            <h3>Proxy Logs</h3>
            <span className="status status--success">Live</span>
          </div>
          <div className="log-panel-controls">
            <input type="text" className="form-control log-filter" placeholder="Filter logs..." id="proxy-filter" />
            <button className="btn btn--sm btn--outline" id="clear-proxy">
              Clear
            </button>
          </div>
        </div>
        <div className="log-content" id="proxy-logs">
          <div className="log-entry">afasdfas dffasdf </div>
          <div className="log-entry">afasdfas dffasdf </div>
          <div className="log-entry">afasdfas dffasdf </div>
          <div className="log-entry">afasdfas dffasdf </div>
        </div>
      </div>

      {/* note collapsed class */}
      <div className="log-panel collapsed" id="upstream-panel">
        <div className="log-panel-header" id="upstream-header">
          <div className="log-panel-title">
            <h3>Upstream Logs</h3>
            <span className="status status--info">Minimized</span>
            <button className="collapse-toggle" id="upstream-toggle">
              ▼
            </button>
          </div>
          <div className="log-panel-controls">
            <input type="text" className="form-control log-filter" placeholder="Filter logs..." id="upstream-filter" />
            <button className="btn btn--sm btn--outline" id="clear-upstream">
              Clear
            </button>
          </div>
        </div>

        <div className="log-content">
          <div className="log-entry">afasdfas dffasdf </div>
          <div className="log-entry">afasdfas dffasdf </div>
          <div className="log-entry">afasdfas dffasdf </div>
        </div>
      </div>
    </div>
  );
}

export default LogViewer;
