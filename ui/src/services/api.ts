import axios from "axios";

const api = axios.create({
  baseURL: "/api",
  timeout: 10000,
});

export interface Model {
  id: string;
  state: string;
}

export const fetchModels = async (): Promise<Model[]> => {
  const response = await api.get("/models/");
  return response.data || [];
};

export const unloadAllModels = async () => {
  const response = await api.post("/models/unload");
  return response.data;
};
