import { useQuery } from "@tanstack/react-query";
import { fetchRunningModels, fetchAvailableModels } from "../services/api";

function Dashboard() {
  // just some place holder content for now
  return (
    <>
      <div className="status-grid">
        <div className="card status-card">
          <div className="card__body">
            <div className="status-card-header">
              <h3>System Status</h3>
              <span className="status status--success">Running</span>
            </div>
            <p className="status-card-value">Proxy Server Active</p>
            <small className="status-card-detail">Port: 8080</small>
          </div>
        </div>

        <div className="card status-card">
          <div className="card__body">
            <div className="status-card-header">
              <h3>Active Models</h3>
              <span className="status-card-number">1</span>
            </div>
            <p className="status-card-value">qwen2.5 Ready</p>
            <small className="status-card-detail">Qwen2.5 0.5B</small>
          </div>
        </div>

        <div className="card status-card">
          <div className="card__body">
            <div className="status-card-header">
              <h3>Total Requests</h3>
              <span className="status-card-number">127</span>
            </div>
            <p className="status-card-value">Last 24 hours</p>
            <small className="status-card-detail">Avg: 5.3/hour</small>
          </div>
        </div>

        <div className="card status-card">
          <div className="card__body">
            <div className="status-card-header">
              <h3>Memory Usage</h3>
              <span className="status-card-number">2.1GB</span>
            </div>
            <p className="status-card-value">Current load</p>
            <small className="status-card-detail">Peak: 3.4GB</small>
          </div>
        </div>
      </div>
    </>
  );
}

export default Dashboard;
