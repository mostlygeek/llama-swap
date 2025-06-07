export default function Models() {
  return (
    <div className="models-section">
      <h2>Running Models</h2>
      <div className="models-grid" id="running-models">
        <div className="model-item">
          <div className="model-header">
            <div className="model-details">
              <h4>Model Name</h4>
              <span className="status status--ready">ready</span>
            </div>
          </div>
          <div className="model-meta">
            <div className="model-meta-item">
              <span className="label">ID:</span>
              <span className="value">my model ID</span>
            </div>
            <div className="model-meta-item">
              <span className="label">Key</span>
              <span className="value">Value</span>
            </div>
            <div className="model-meta-item">
              <span className="label">Port:</span>
              <span className="value">1234</span>
            </div>
          </div>
          <div className="model-controls">
            <p>Some buttons here</p>
          </div>
        </div>
      </div>
    </div>
  );
}
