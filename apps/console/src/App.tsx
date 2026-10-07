import type {
  APIErrorResponse,
  GetConversationHistoryResponse,
  ListConversationsResponse,
  ModelConfigResponse,
  SaveModelConfigRequest,
} from "@ariad/contracts";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, Outlet, useParams, useSearch } from "@tanstack/react-router";
import { type FormEvent, useEffect, useState } from "react";

import type { AppSurface } from "@ariad/contracts";

const surface: AppSurface = "console";
const workspaceID = "ws_dev";
const adminTokenStorageKey = "ariad:admin_token";
const adminUnauthorizedEvent = "ariad:admin-unauthorized";

async function requestJSON<Response>(
  path: string,
  token: string | null,
  clearTokenOnUnauthorized: boolean,
  init?: RequestInit,
): Promise<Response> {
  const headers = new Headers(init?.headers);
  if (token) headers.set("Authorization", `Bearer ${token}`);
  const response = await fetch(path, {
    ...init,
    headers,
  });
  if (response.status === 401 && clearTokenOnUnauthorized) {
    sessionStorage.removeItem(adminTokenStorageKey);
    window.dispatchEvent(new Event(adminUnauthorizedEvent));
  }
  const responseText = await response.text();
  let payload: unknown;
  try {
    payload = JSON.parse(responseText) as unknown;
  } catch {
    throw new Error(response.ok ? "The API returned an invalid response." : `Request failed (${response.status}).`);
  }
  if (!response.ok) {
    const failure = payload as APIErrorResponse;
    throw new Error(failure.error?.message ?? `Request failed (${response.status})`);
  }
  return payload as Response;
}

function getJSON<Response>(path: string): Promise<Response> {
  return requestJSON(path, sessionStorage.getItem(adminTokenStorageKey), true);
}

function sendJSON<Request, Response>(path: string, method: "PUT" | "DELETE", body?: Request): Promise<Response> {
  return requestJSON(path, sessionStorage.getItem(adminTokenStorageKey), true, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

function formatActivity(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

export function ConsoleShell() {
  const queryClient = useQueryClient();
  const [authenticated, setAuthenticated] = useState(
    () => sessionStorage.getItem(adminTokenStorageKey) !== null,
  );

  useEffect(() => {
    const handleUnauthorized = () => {
      queryClient.clear();
      setAuthenticated(false);
    };
    window.addEventListener(adminUnauthorizedEvent, handleUnauthorized);
    return () => window.removeEventListener(adminUnauthorizedEvent, handleUnauthorized);
  }, [queryClient]);

  if (!authenticated) {
    return <AdminLoginPage onLogin={() => setAuthenticated(true)} />;
  }

  function logout() {
    sessionStorage.removeItem(adminTokenStorageKey);
    queryClient.clear();
    setAuthenticated(false);
  }

  return (
    <div className="console-shell">
      <aside className="sidebar">
        <a className="brand" href="/" aria-label="Ariad home">
          <span className="brand-mark">A</span>
          <span>Ariad</span>
        </a>
        <nav aria-label="Primary navigation">
          <span className="nav-label">Workspace</span>
          <Link className="nav-item" activeOptions={{ exact: true }} activeProps={{ className: "nav-item nav-item-active" }} to="/">Overview</Link>
          <span className="nav-item nav-item-disabled">Agents</span>
          <span className="nav-item nav-item-disabled">Knowledge</span>
          <Link className="nav-item" activeProps={{ className: "nav-item nav-item-active" }} to="/conversations">Conversations</Link>
          <Link className="nav-item" activeProps={{ className: "nav-item nav-item-active" }} to="/settings/model">Model configuration</Link>
          <span className="nav-item nav-item-disabled">Evaluations</span>
        </nav>
        <button className="logout-button" type="button" onClick={logout}>Log out</button>
      </aside>
      <main className="main-content" data-surface={surface}>
        <Outlet />
      </main>
    </div>
  );
}

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
  const [notice, setNotice] = useState("");
  const saveMutation = useMutation({
    mutationFn: (input: SaveModelConfigRequest) => sendJSON<SaveModelConfigRequest, ModelConfigResponse>(path, "PUT", input),
    onSuccess: (response) => {
      queryClient.setQueryData(["model-config", workspaceID], response);
      setBaseURL(response.base_url);
      setModel(response.model);
      setAPIKey("");
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
      setNotice("Workspace configuration cleared. System default restored.");
    },
    onMutate: () => setNotice(""),
  });

  function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    saveMutation.mutate({ workspace_id: workspaceID, base_url: baseURL, model, api_key: apiKey });
  }
  const error = saveMutation.error ?? resetMutation.error;
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
        <div className="config-actions"><button type="submit" disabled={saveMutation.isPending || resetMutation.isPending}>{saveMutation.isPending ? "Saving…" : "Save configuration"}</button><button className="secondary-button" type="button" disabled={saveMutation.isPending || resetMutation.isPending || config?.source !== "workspace"} onClick={() => resetMutation.mutate()}>{resetMutation.isPending ? "Resetting…" : "Reset to system default"}</button></div>
      </form>
      {notice && <p className="notice success" role="status">{notice}</p>}
      {error && <p className="notice error" role="alert">{error.message}</p>}
    </>
  );
}

function AdminLoginPage({ onLogin }: { onLogin: () => void }) {
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmed = token.trim();
    if (!trimmed) return;
    setError("");
    setPending(true);
    try {
      await requestJSON<ListConversationsResponse>(
        `/api/v1/conversations?workspace_id=${encodeURIComponent(workspaceID)}`,
        trimmed,
        false,
      );
      sessionStorage.setItem(adminTokenStorageKey, trimmed);
      onLogin();
    } catch (loginError) {
      setError(loginError instanceof Error ? loginError.message : "Could not sign in.");
    } finally {
      setPending(false);
    }
  }

  return (
    <main className="login-page" data-surface={surface}>
      <form className="login-card" onSubmit={(event) => void submit(event)}>
        <span className="brand-mark">A</span>
        <div className="eyebrow">Ariad Console</div>
        <h1>Administrator access</h1>
        <p>Enter the management token configured for this Ariad server.</p>
        <label htmlFor="admin-token">Management token</label>
        <input
          id="admin-token"
          type="password"
          autoComplete="current-password"
          autoFocus
          value={token}
          onChange={(event) => setToken(event.target.value)}
        />
        {error && <p className="login-error" role="alert">{error}</p>}
        <button type="submit" disabled={pending || !token.trim()}>
          {pending ? "Checking…" : "Continue"}
        </button>
      </form>
    </main>
  );
}

export function ConversationsPage() {
  const queryClient = useQueryClient();
  const conversationsQuery = useQuery({
    queryKey: ["conversations", workspaceID],
    queryFn: () => getJSON<ListConversationsResponse>(`/api/v1/conversations?workspace_id=${encodeURIComponent(workspaceID)}`),
  });
  const conversations = conversationsQuery.data?.conversations ?? [];

  return (
    <section className="conversations-page">
      <header className="page-header">
        <div><div className="eyebrow">Workspace / Conversations</div><h1>Conversations</h1></div>
        <button type="button" onClick={() => void queryClient.invalidateQueries({ queryKey: ["conversations", workspaceID] })} disabled={conversationsQuery.isFetching}>
          {conversationsQuery.isFetching ? "Refreshing…" : "Refresh"}
        </button>
      </header>
      {conversationsQuery.error && <p className="notice error">{conversationsQuery.error.message}</p>}
      {conversationsQuery.isPending ? <p className="empty-state">Loading conversations…</p> : conversations.length === 0 ? (
        <p className="empty-state">No conversations yet.</p>
      ) : (
        <div className="conversation-list">
          {conversations.map((conversation) => (
            <Link
              className="conversation-row"
              key={conversation.conversation_id}
              to="/conversations/$conversationId"
              params={{ conversationId: conversation.conversation_id }}
              search={{ visitorId: conversation.visitor_id }}
            >
              <div><strong>{conversation.visitor_id}</strong><p>{conversation.last_message_text}</p></div>
              <div className="conversation-meta"><span>{conversation.message_count} {conversation.message_count === 1 ? "message" : "messages"}</span><time dateTime={conversation.last_activity_at}>{formatActivity(conversation.last_activity_at)}</time></div>
            </Link>
          ))}
        </div>
      )}
    </section>
  );
}

export function ConversationDetailPage() {
  const { conversationId } = useParams({ from: "/conversations/$conversationId" });
  const { visitorId } = useSearch({ from: "/conversations/$conversationId" });
  const historyQuery = useQuery({
    queryKey: ["conversation", workspaceID, visitorId, conversationId],
    queryFn: () => getJSON<GetConversationHistoryResponse>(
      `/api/v1/conversations/${encodeURIComponent(conversationId)}?workspace_id=${encodeURIComponent(workspaceID)}&visitor_id=${encodeURIComponent(visitorId)}`,
    ),
    enabled: visitorId !== "",
  });
  const turns = historyQuery.data?.turns ?? [];

  return (
    <section className="conversation-detail">
      <Link className="back-link" to="/conversations">← All conversations</Link>
      <div className="eyebrow">Conversation</div>
      <h1>{visitorId || "Unknown visitor"}</h1>
      <p className="conversation-id">{conversationId}</p>
      {!visitorId && <p className="notice error">This conversation link is missing its visitor ID.</p>}
      {historyQuery.error && <p className="notice error">{historyQuery.error.message}</p>}
      {historyQuery.isPending && visitorId && <p className="empty-state">Loading conversation…</p>}
      <div className="turn-list">
        {turns.map((turn) => (
          <article className="detail-turn" key={turn.message_id}>
            <div className="detail-message"><span>Visitor</span><p>{turn.message}</p><time dateTime={turn.message_created_at}>{formatActivity(turn.message_created_at)}</time></div>
            <div className="detail-answer"><span>Ariad · {turn.terminal_disposition}</span><p>{turn.answer}</p>
              {turn.citations.length > 0 && <div className="detail-citations"><strong>Sources</strong>{turn.citations.map((citation) => <blockquote key={citation.citation_id}><cite>{citation.source_title}</cite>{citation.quote}</blockquote>)}</div>}
            </div>
          </article>
        ))}
      </div>
    </section>
  );
}

export function OverviewPage() {
  return (
    <section className="overview">
      <div className="eyebrow">Foundation / Console</div>
      <h1>Answers people can verify.</h1>
      <p className="lede">
        Connect knowledge, inspect every decision, and publish an agent that
        knows when not to answer.
      </p>
      <div className="status-card">
        <div><span className="status-dot" aria-hidden="true" /><strong>Console runtime ready</strong></div>
        <p>Router and server-state boundaries are initialized.</p>
      </div>
    </section>
  );
}
