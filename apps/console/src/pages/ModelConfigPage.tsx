import type { EmbeddingBackfillResponse, ModelConfigResponse, SaveModelConfigRequest } from "@ariad/contracts";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";

import { getJSON, sendJSON, workspaceID } from "../api";

export function ModelConfigPage() {
  const path = `/api/v1/model-config?workspace_id=${encodeURIComponent(workspaceID)}`;
  const configQuery = useQuery({ queryKey: ["model-config", workspaceID], queryFn: () => getJSON<ModelConfigResponse>(path) });
  const config = configQuery.data;
  return (
    <section className="model-config-page">
      <div className="eyebrow">Workspace / Settings</div>
      <h1>Model configuration</h1>
      <p className="lede">Use your own OpenAI-compatible model endpoint for this workspace.</p>
      {configQuery.isPending && <p className="empty-state">Loading model configuration…</p>}
      {configQuery.error && <p className="notice error" role="alert">{configQuery.error.message}</p>}
      {config && <ModelConfigEditor config={config} path={path} />}
    </section>
  );
}

function ModelConfigEditor({ config, path }: { config: ModelConfigResponse; path: string }) {
  const queryClient = useQueryClient();
  const [baseURL, setBaseURL] = useState(config.base_url);
  const [model, setModel] = useState(config.model);
  const [apiKey, setAPIKey] = useState("");
  const [embeddingBaseURL, setEmbeddingBaseURL] = useState(config.embedding_base_url);
  const [embeddingModel, setEmbeddingModel] = useState(config.embedding_model);
  const [embeddingAPIKey, setEmbeddingAPIKey] = useState("");
  const [embeddingThreshold, setEmbeddingThreshold] = useState(config.embedding_threshold || 0.35);
  const [notice, setNotice] = useState("");
  const backfillPath = `/api/v1/knowledge/embeddings/backfill?workspace_id=${encodeURIComponent(workspaceID)}`;
  const backfillQuery = useQuery({ queryKey: ["embedding-backfill", workspaceID], queryFn: () => getJSON<EmbeddingBackfillResponse>(backfillPath), refetchInterval: (query) => query.state.data?.status === "running" ? 1000 : false });
  const backfillMutation = useMutation({ mutationFn: () => sendJSON<never, { status: string }>(backfillPath, "POST"), onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["embedding-backfill", workspaceID] }) });
  const saveMutation = useMutation({
    mutationFn: (input: SaveModelConfigRequest) => sendJSON<SaveModelConfigRequest, ModelConfigResponse>(path, "PUT", input),
    onSuccess: (response) => {
      queryClient.setQueryData(["model-config", workspaceID], response);
      setBaseURL(response.base_url);
      setModel(response.model);
      setAPIKey("");
      setEmbeddingBaseURL(response.embedding_base_url); setEmbeddingModel(response.embedding_model); setEmbeddingAPIKey(""); setEmbeddingThreshold(response.embedding_threshold || 0.35);
      setNotice("Workspace model configuration saved and active.");
    },
    onMutate: () => setNotice(""),
  });
  const resetMutation = useMutation({
    mutationFn: () => sendJSON<never, ModelConfigResponse>(path, "DELETE"),
    onSuccess: (response) => {
      queryClient.setQueryData(["model-config", workspaceID], response);
      setBaseURL(response.base_url);
      setModel(response.model);
      setAPIKey("");
      setEmbeddingBaseURL(response.embedding_base_url); setEmbeddingModel(response.embedding_model); setEmbeddingAPIKey(""); setEmbeddingThreshold(response.embedding_threshold || 0.35);
      setNotice("Workspace configuration cleared. System default restored.");
    },
    onMutate: () => setNotice(""),
  });

  function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    saveMutation.mutate({ workspace_id: workspaceID, base_url: baseURL, model, api_key: apiKey, embedding_base_url: embeddingBaseURL, embedding_model: embeddingModel, embedding_api_key: embeddingAPIKey, embedding_threshold: embeddingThreshold });
  }
  const error = saveMutation.error ?? resetMutation.error ?? backfillMutation.error ?? backfillQuery.error;
  return (
    <>
      <div className="config-status">
        <strong>{config.source === "workspace" ? "Workspace custom configuration" : config.source === "system_default" ? "System default configuration" : "Local fallback model"}</strong>
        <dl><div><dt>Model</dt><dd>{config.model || "Local grounded model"}</dd></div><div><dt>Base URL</dt><dd>{config.base_url || "Not applicable"}</dd></div><div><dt>API Key</dt><dd>{config.api_key_mask || "Not configured"}</dd></div></dl>
      </div>
      <form className="model-config-form" onSubmit={save}>
        <label htmlFor="model-base-url">Base URL</label><input id="model-base-url" type="url" required value={baseURL} onChange={(event) => setBaseURL(event.target.value)} />
        <label htmlFor="model-name">Model name</label><input id="model-name" required value={model} onChange={(event) => setModel(event.target.value)} />
        <label htmlFor="model-api-key">API Key</label><input id="model-api-key" type="password" autoComplete="new-password" value={apiKey} onChange={(event) => setAPIKey(event.target.value)} placeholder={config?.source === "workspace" ? "Leave blank to keep the existing key" : "Required for first save"} />
        <h2>Semantic retrieval</h2><p>{config.semantic_enabled ? "Semantic retrieval enabled" : "Semantic retrieval is disabled; lexical retrieval is active."}</p>{config.embedding_health.attempted && <p className={config.embedding_health.success ? "notice success" : "notice error"}>{config.embedding_health.success ? "Last embedding call succeeded." : `Last embedding call failed: ${config.embedding_health.error ?? "unknown error"}`}</p>}
        <label htmlFor="embedding-base-url">Embedding Base URL</label><input id="embedding-base-url" type="url" value={embeddingBaseURL} onChange={(event) => setEmbeddingBaseURL(event.target.value)} />
        <label htmlFor="embedding-model">Embedding model</label><input id="embedding-model" value={embeddingModel} onChange={(event) => setEmbeddingModel(event.target.value)} placeholder="Leave blank to disable" />
        <label htmlFor="embedding-api-key">Embedding API Key</label><input id="embedding-api-key" type="password" autoComplete="new-password" value={embeddingAPIKey} onChange={(event) => setEmbeddingAPIKey(event.target.value)} placeholder={config.embedding_api_key_mask ? "Leave blank to keep the existing key" : "Required when enabling"} />
        <label htmlFor="embedding-threshold">Minimum similarity</label><input id="embedding-threshold" type="number" min="0" max="1" step="0.01" value={embeddingThreshold} onChange={(event) => setEmbeddingThreshold(Number(event.target.value))} />
        <div className="config-actions"><button className="secondary-button" type="button" disabled={!config.semantic_enabled || backfillQuery.data?.status === "running"} onClick={() => backfillMutation.mutate()}>Backfill existing knowledge</button></div>
        {backfillQuery.data && <p>Backfill: {backfillQuery.data.status} · {backfillQuery.data.completed}/{backfillQuery.data.total} complete · {backfillQuery.data.failed} failed</p>}
        {backfillQuery.data?.failures?.length ? <div className="notice error"><strong>Failed chunks</strong><ul>{backfillQuery.data.failures.map((failure) => <li key={failure.chunk_id}>{failure.chunk_id}: {failure.reason}</li>)}</ul><span>Run backfill again to retry.</span></div> : null}
        {backfillQuery.data?.error && <p className="notice error" role="alert">{backfillQuery.data.error}</p>}
        <div className="config-actions"><button type="submit" disabled={saveMutation.isPending || resetMutation.isPending}>{saveMutation.isPending ? "Saving…" : "Save configuration"}</button><button className="secondary-button" type="button" disabled={saveMutation.isPending || resetMutation.isPending || config?.source !== "workspace"} onClick={() => resetMutation.mutate()}>{resetMutation.isPending ? "Resetting…" : "Reset to system default"}</button></div>
      </form>
      {notice && <p className="notice success" role="status">{notice}</p>}
      {error && <p className="notice error" role="alert">{error.message}</p>}
    </>
  );
}

