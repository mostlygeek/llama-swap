function Dashboard() {
  // just some place holder content for now
  return (
    <>
      <ModelList />
    </>
  );
}

interface Model {
  name: string;
  status: "ready" | "starting" | "stopped";
  statusText: string;
}

const ModelList = () => {
  const models: Model[] = [
    { name: "Qwen2.5 0.5B", status: "ready", statusText: "Ready" },
    { name: "Llama3 8B", status: "starting", statusText: "Starting" },
    { name: "Mistral 7B", status: "stopped", statusText: "Stopped" },
  ];
  const handleUnload = (modelName: string) => {
    console.log(`Unloading model: ${modelName}`);
    // Add your unload logic here (API call, state update, etc.)
  };
  return (
    <>
      <h2 className="my-8">Models</h2>
      <button className="btn">Unload All Models</button>
      <table className="w-full table-auto">
        <thead>
          <tr>
            <th className="text-left">Name</th>
            <th className="w-32 text-left">Status</th>
            <th className="w-24 text-left">Options</th>
            <th className="w-24 text-left">Upstream</th>
          </tr>
        </thead>
        <tbody>
          {models.map((model, index) => (
            <tr key={index}>
              <td className="py-2">{model.name}</td>
              <td className="w-32 py-2">
                <span className={`status status--${model.status}`}>{model.statusText}</span>
              </td>
              <td className="w-24 py-2">
                {model.status === "ready" && (
                  <button className="btn" onClick={() => handleUnload(model.name)}>
                    Unload
                  </button>
                )}
              </td>
              <td className="w-24 py-2">
                <a href={`/upstream/${model.name}`} className="btn btn--sm">
                  Upstream
                </a>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
};

const StatusBoard = () => {
  return (
    <>
      <h2 className="mb-8">Status</h2>
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
};

export default Dashboard;
