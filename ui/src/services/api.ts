import axios from "axios";

const api = axios.create({
  baseURL: "/api",
  timeout: 10000,
});

export const fetchRunningModels = async () => {
  const response = await api.get("/models/running");
  return response.data.running || [];
};

export const fetchAvailableModels = async () => {
  const response = await api.get("/models/available");
  return response.data.data || [];
};

export const unloadAllModels = async () => {
  const response = await api.post("/models/unload");
  return response.data;
};
