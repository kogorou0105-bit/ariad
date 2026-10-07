import type {
  APIErrorResponse,
  GetConversationHistoryResponse,
  ListConversationsResponse,
  ListReviewsResponse,
  ModelConfigResponse,
  SaveModelConfigRequest,
  ReviewReplyRequest,
  ResolveReviewRequest,
  AdministratorCredentialsRequest,
  AdministratorLoginResponse,
  AdministratorSummary,
  ListAdministratorsResponse,
  ChangeAdministratorPasswordRequest,
  ListVisitorsResponse,
  EmbeddingBackfillResponse,
  KnowledgeSource,
  ListKnowledgeSourcesResponse,
  UploadKnowledgeFilesResponse,
} from "@ariad/contracts";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, Outlet, useParams, useSearch } from "@tanstack/react-router";
import { type FormEvent, useEffect, useState } from "react";

import type { AppSurface } from "@ariad/contracts";

const surface: AppSurface = "console";
const workspaceID = "ws_dev";
const adminTokenStorageKey = "ariad:admin_session";
const administratorIDStorageKey = "ariad:administrator_id";
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
    sessionStorage.removeItem(administratorIDStorageKey);
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

function sendJSON<Request, Response>(path: string, method: "POST" | "PUT" | "DELETE", body?: Request): Promise<Response> {
  return requestJSON(path, sessionStorage.getItem(adminTokenStorageKey), true, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

function uploadFiles(files: File[]): Promise<UploadKnowledgeFilesResponse> {
  const body = new FormData(); body.set("workspace_id", workspaceID); files.forEach((file) => body.append("files", file));
  return requestJSON("/api/v1/knowledge/files", sessionStorage.getItem(adminTokenStorageKey), true, { method: "POST", body });
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

  async function logout() {
    try { await sendJSON<never, { logged_out: boolean }>("/api/v1/admin/logout", "POST"); } finally {
      sessionStorage.removeItem(adminTokenStorageKey);
      sessionStorage.removeItem(administratorIDStorageKey);
      queryClient.clear();
      setAuthenticated(false);
    }
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
          <Link className="nav-item" activeProps={{ className: "nav-item nav-item-active" }} to="/knowledge">Knowledge</Link>
          <Link className="nav-item" activeProps={{ className: "nav-item nav-item-active" }} to="/conversations" search={{ visitorId: "" }}>Conversations</Link>
          <Link className="nav-item" activeProps={{ className: "nav-item nav-item-active" }} to="/visitors">Visitors</Link>
          <Link className="nav-item" activeProps={{ className: "nav-item nav-item-active" }} to="/reviews">Review queue</Link>
          <Link className="nav-item" activeProps={{ className: "nav-item nav-item-active" }} to="/settings/model">Model configuration</Link>
          <Link className="nav-item" activeProps={{ className: "nav-item nav-item-active" }} to="/settings/administrators">Administrators</Link>
          <span className="nav-item nav-item-disabled">Evaluations</span>
        </nav>
        <button className="logout-button" type="button" onClick={() => void logout()}>Log out</button>
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

function AdminLoginPage({ onLogin }: { onLogin: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmed = username.trim();
    if (!trimmed || !password) return;
    setError("");
    setPending(true);
    try {
      const login = await requestJSON<AdministratorLoginResponse>("/api/v1/admin/login", null, false, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username: trimmed, password } satisfies AdministratorCredentialsRequest) });
      sessionStorage.setItem(adminTokenStorageKey, login.token);
      sessionStorage.setItem(administratorIDStorageKey, login.administrator.administrator_id);
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
        <p>Sign in with your administrator account. On first startup, find the generated admin password in the API startup log.</p>
        <label htmlFor="admin-username">Username</label>
        <input
          id="admin-username"
          autoComplete="username"
          autoFocus
          value={username}
          onChange={(event) => setUsername(event.target.value)}
        />
        <label htmlFor="admin-password">Password</label>
        <input id="admin-password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} />
        {error && <p className="login-error" role="alert">{error}</p>}
        <button type="submit" disabled={pending || !username.trim() || !password}>
          {pending ? "Checking…" : "Continue"}
        </button>
      </form>
    </main>
  );
}

export function AdministratorsPage() {
  const queryClient = useQueryClient();
  const currentAdministratorID = sessionStorage.getItem(administratorIDStorageKey);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
	const [currentPassword, setCurrentPassword] = useState("");
	const [newPassword, setNewPassword] = useState("");
  const [created, setCreated] = useState<AdministratorSummary | null>(null);
	const administratorsQuery = useQuery({ queryKey: ["administrators"], queryFn: () => getJSON<ListAdministratorsResponse>("/api/v1/admin/accounts") });
	const mutation = useMutation({ mutationFn: (body: AdministratorCredentialsRequest) => sendJSON<AdministratorCredentialsRequest, AdministratorSummary>("/api/v1/admin/accounts", "POST", body), onSuccess: (administrator) => { setCreated(administrator); setUsername(""); setPassword(""); void queryClient.invalidateQueries({ queryKey: ["administrators"] }); } });
	const deleteMutation = useMutation({ mutationFn: (administratorID: string) => sendJSON<never, { deleted: boolean }>(`/api/v1/admin/accounts/${encodeURIComponent(administratorID)}`, "DELETE"), onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["administrators"] }) });
	const passwordMutation = useMutation({ mutationFn: (body: ChangeAdministratorPasswordRequest) => sendJSON<ChangeAdministratorPasswordRequest, { password_changed: boolean }>("/api/v1/admin/password", "POST", body), onSuccess: () => { sessionStorage.removeItem(adminTokenStorageKey); sessionStorage.removeItem(administratorIDStorageKey); window.dispatchEvent(new Event(adminUnauthorizedEvent)); } });
	return <section className="model-config-page"><div className="eyebrow">Workspace / Settings</div><h1>Administrators</h1><p className="lede">Create a separate account for each person who manages Ariad.</p>{administratorsQuery.isPending ? <p className="empty-state">Loading administrators…</p> : <div className="conversation-list">{administratorsQuery.data?.administrators.map((administrator) => <div className="conversation-row" key={administrator.administrator_id}><strong>{administrator.username}{administrator.administrator_id === currentAdministratorID ? " (you)" : ""}</strong><button type="button" disabled={administrator.administrator_id === currentAdministratorID || deleteMutation.isPending} onClick={() => deleteMutation.mutate(administrator.administrator_id)}>Delete</button></div>)}</div>}<form className="model-config-form" onSubmit={(event) => { event.preventDefault(); setCreated(null); mutation.mutate({ username, password }); }}><h2>Create administrator</h2><label htmlFor="new-admin-username">Username</label><input id="new-admin-username" autoComplete="off" required value={username} onChange={(event) => setUsername(event.target.value)} /><label htmlFor="new-admin-password">Initial password</label><input id="new-admin-password" type="password" minLength={12} autoComplete="new-password" required value={password} onChange={(event) => setPassword(event.target.value)} /><button type="submit" disabled={mutation.isPending}>{mutation.isPending ? "Creating…" : "Create administrator"}</button></form>{created && <p className="notice success">Administrator {created.username} created.</p>}<form className="model-config-form" onSubmit={(event) => { event.preventDefault(); passwordMutation.mutate({ current_password: currentPassword, new_password: newPassword }); }}><h2>Change my password</h2><label htmlFor="current-admin-password">Current password</label><input id="current-admin-password" type="password" autoComplete="current-password" required value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} /><label htmlFor="changed-admin-password">New password</label><input id="changed-admin-password" type="password" minLength={12} autoComplete="new-password" required value={newPassword} onChange={(event) => setNewPassword(event.target.value)} /><button type="submit" disabled={passwordMutation.isPending}>Change password and sign out</button></form>{(mutation.error || deleteMutation.error || passwordMutation.error || administratorsQuery.error) && <p className="notice error">{(mutation.error ?? deleteMutation.error ?? passwordMutation.error ?? administratorsQuery.error)?.message}</p>}</section>;
}

export function ConversationsPage() {
  const queryClient = useQueryClient();
  const { visitorId } = useSearch({ from: "/conversations" });
  const [visitorFilter, setVisitorFilter] = useState(visitorId);
  const conversationsQuery = useQuery({
    queryKey: ["conversations", workspaceID, visitorFilter],
    queryFn: () => getJSON<ListConversationsResponse>(`/api/v1/conversations?workspace_id=${encodeURIComponent(workspaceID)}${visitorFilter ? `&visitor_id=${encodeURIComponent(visitorFilter)}` : ""}`),
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
      <label htmlFor="visitor-filter">Filter by visitor</label><input id="visitor-filter" value={visitorFilter} onChange={(event) => setVisitorFilter(event.target.value.trim())} placeholder="visitor_…" />
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
              <div className="conversation-meta"><span>{conversation.status} · {conversation.message_count} {conversation.message_count === 1 ? "turn" : "turns"}</span><time dateTime={conversation.started_at}>Started {formatActivity(conversation.started_at)}</time><time dateTime={conversation.last_activity_at}>Active {formatActivity(conversation.last_activity_at)}</time></div>
            </Link>
          ))}
        </div>
      )}
    </section>
  );
}

export function VisitorsPage() {
  const visitorsQuery = useQuery({ queryKey: ["visitors", workspaceID], queryFn: () => getJSON<ListVisitorsResponse>(`/api/v1/visitors?workspace_id=${encodeURIComponent(workspaceID)}`) });
  return <section className="conversations-page"><header className="page-header"><div><div className="eyebrow">Workspace / Visitors</div><h1>Visitors</h1></div></header>{visitorsQuery.error && <p className="notice error">{visitorsQuery.error.message}</p>}{visitorsQuery.isPending ? <p className="empty-state">Loading visitors…</p> : visitorsQuery.data?.visitors.length === 0 ? <p className="empty-state">No visitors yet.</p> : <div className="conversation-list">{visitorsQuery.data?.visitors.map((visitor) => <Link className="conversation-row" key={visitor.visitor_id} to="/conversations" search={{ visitorId: visitor.visitor_id }}><div><strong>{visitor.visitor_id}</strong><p>First seen {formatActivity(visitor.first_seen_at)}</p></div><div className="conversation-meta"><span>{visitor.conversation_count} conversations · {visitor.total_turn_count} turns</span><time dateTime={visitor.last_activity_at}>{formatActivity(visitor.last_activity_at)}</time></div></Link>)}</div>}</section>;
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
      <Link className="back-link" to="/conversations" search={{ visitorId: "" }}>← All conversations</Link>
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

export function ReviewQueuePage() {
  const queryClient = useQueryClient();
  const reviewsQuery = useQuery({
    queryKey: ["reviews", workspaceID],
    queryFn: () => getJSON<ListReviewsResponse>(`/api/v1/reviews?workspace_id=${encodeURIComponent(workspaceID)}`),
  });
  const reviews = reviewsQuery.data?.reviews ?? [];
  return (
    <section className="conversations-page">
      <header className="page-header"><div><div className="eyebrow">Workspace / Human handoff</div><h1>Review queue</h1></div><button type="button" onClick={() => void queryClient.invalidateQueries({ queryKey: ["reviews", workspaceID] })}>Refresh</button></header>
      {reviewsQuery.error && <p className="notice error" role="alert">{reviewsQuery.error.message}</p>}
      {reviewsQuery.isPending ? <p className="empty-state">Loading review queue…</p> : reviews.length === 0 ? <p className="empty-state">No conversations are waiting for human support.</p> : (
        <div className="conversation-list">{reviews.map((item) => <Link className="conversation-row" key={item.conversation_id} to="/reviews/$conversationId" params={{ conversationId: item.conversation_id }} search={{ visitorId: item.visitor_id }}><div><strong>{item.visitor_id}</strong><p>{item.last_message_text || "No message preview"}</p><small>Reason: {item.reason} · requested by {item.requested_by}</small></div><div className="conversation-meta"><span>Pending</span><time dateTime={item.last_activity_at}>{formatActivity(item.last_activity_at)}</time></div></Link>)}</div>
      )}
    </section>
  );
}

export function ReviewDetailPage() {
  const queryClient = useQueryClient();
  const { conversationId } = useParams({ from: "/reviews/$conversationId" });
  const { visitorId } = useSearch({ from: "/reviews/$conversationId" });
  const [replyText, setReplyText] = useState("");
  const historyKey = ["conversation", workspaceID, visitorId, conversationId];
  const historyQuery = useQuery({ queryKey: historyKey, queryFn: () => getJSON<GetConversationHistoryResponse>(`/api/v1/conversations/${encodeURIComponent(conversationId)}?workspace_id=${encodeURIComponent(workspaceID)}&visitor_id=${encodeURIComponent(visitorId)}`), enabled: visitorId !== "" });
  const replyMutation = useMutation({ mutationFn: (body: ReviewReplyRequest) => sendJSON<ReviewReplyRequest, unknown>(`/api/v1/reviews/${encodeURIComponent(conversationId)}/replies`, "POST", body), onSuccess: () => { setReplyText(""); void queryClient.invalidateQueries({ queryKey: historyKey }); void queryClient.invalidateQueries({ queryKey: ["reviews", workspaceID] }); } });
  const resolveMutation = useMutation({ mutationFn: () => sendJSON<ResolveReviewRequest, unknown>(`/api/v1/reviews/${encodeURIComponent(conversationId)}/resolve`, "POST", { workspace_id: workspaceID, visitor_id: visitorId }), onSuccess: () => { void queryClient.invalidateQueries({ queryKey: historyKey }); void queryClient.invalidateQueries({ queryKey: ["reviews", workspaceID] }); } });
  const history = historyQuery.data;
  return (
    <section className="conversation-detail">
      <Link className="back-link" to="/reviews">← Review queue</Link><div className="eyebrow">Human handoff · {history?.status ?? "loading"}</div><h1>{visitorId}</h1><p className="conversation-id">{conversationId}</p>
      {!visitorId && <p className="notice error">This review link is missing its visitor ID.</p>}{historyQuery.error && <p className="notice error">{historyQuery.error.message}</p>}{historyQuery.isPending && visitorId && <p className="empty-state">Loading conversation…</p>}
      <div className="turn-list">{history?.turns.map((turn) => <article className="detail-turn" key={turn.message_id}><div className="detail-message"><span>Visitor</span><p>{turn.message}</p><time dateTime={turn.message_created_at}>{formatActivity(turn.message_created_at)}</time></div><div className="detail-answer"><span>AI · {turn.terminal_disposition}</span><p>{turn.answer}</p>{turn.citations.length > 0 && <div className="detail-citations"><strong>Sources</strong>{turn.citations.map((citation) => <blockquote key={citation.citation_id}><cite>{citation.source_title}</cite>{citation.quote}</blockquote>)}</div>}</div></article>)}</div>
      {history?.human_replies.map((reply) => <article className="human-review-reply" key={reply.reply_id}><span>Human support · {reply.author_id}</span><p>{reply.text}</p><time dateTime={reply.created_at}>{formatActivity(reply.created_at)}</time></article>)}
      {history?.status === "pending" && <form className="review-reply-form" onSubmit={(event) => { event.preventDefault(); replyMutation.mutate({ workspace_id: workspaceID, visitor_id: visitorId, text: replyText }); }}><label htmlFor="human-reply">Reply as human support</label><textarea id="human-reply" value={replyText} onChange={(event) => setReplyText(event.target.value)} required /><div className="config-actions"><button type="submit" disabled={replyMutation.isPending || !replyText.trim()}>Send reply</button><button className="secondary-button" type="button" disabled={resolveMutation.isPending} onClick={() => resolveMutation.mutate()}>Mark resolved</button></div></form>}
      {history?.status === "pending" && <p className="review-hint">After replying, mark the conversation resolved when no further human action is needed.</p>}
      {(replyMutation.error || resolveMutation.error) && <p className="notice error">{(replyMutation.error ?? resolveMutation.error)?.message}</p>}
      {history?.status === "resolved" && <p className="notice success">This handoff is resolved.</p>}
    </section>
  );
}

function sourceLabel(source: KnowledgeSource): string {
  if (source.type === "file") return source.file_name ?? source.title;
  if (source.type === "url") return source.source_url ?? source.title;
  return source.title;
}

export function KnowledgeSourcesPage() {
  const queryClient = useQueryClient();
  const [dragging, setDragging] = useState(false);
  const query = useQuery({ queryKey: ["knowledge-sources", workspaceID], queryFn: () => getJSON<ListKnowledgeSourcesResponse>(`/api/v1/knowledge/sources?workspace_id=${encodeURIComponent(workspaceID)}`) });
  const upload = useMutation({ mutationFn: uploadFiles, onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["knowledge-sources", workspaceID] }) });
  const select = (files: FileList | null) => { if (files?.length) upload.mutate(Array.from(files)); };
  return <section className="conversations-page knowledge-page">
    <header className="page-header"><div><div className="eyebrow">Workspace / Knowledge</div><h1>Knowledge sources</h1><p className="lede-small">Upload PDF, DOCX, TXT, or Markdown files. Maximum 10 MiB per file and 50 MiB per upload.</p></div></header>
    <label className={`upload-dropzone ${dragging ? "is-dragging" : ""}`} onDragEnter={(event) => { event.preventDefault(); setDragging(true); }} onDragOver={(event) => event.preventDefault()} onDragLeave={() => setDragging(false)} onDrop={(event) => { event.preventDefault(); setDragging(false); select(event.dataTransfer.files); }}>
      <input type="file" multiple accept=".pdf,.docx,.txt,.md,.markdown" onChange={(event) => select(event.target.files)} disabled={upload.isPending} />
      <strong>{upload.isPending ? "Uploading and parsing…" : "Drop documents here or click to choose"}</strong><span>Single and multiple file upload supported</span>
    </label>
    {upload.error && <p className="notice error" role="alert">{upload.error.message}</p>}
    {upload.data && <p className="notice success">Processed {upload.data.sources.length} file(s). Failed files remain available to inspect and retry.</p>}
    {query.error && <p className="notice error">{query.error.message}</p>}
    {query.isPending ? <p className="empty-state">Loading knowledge sources…</p> : query.data?.sources.length === 0 ? <p className="empty-state">No knowledge sources yet.</p> : <div className="source-table">
      <div className="source-table-head"><span>Source</span><span>Status</span><span>Chunks</span><span>Created</span></div>
      {query.data?.sources.map((source) => <Link className="source-row" key={source.source_id} to="/knowledge/$sourceId" params={{ sourceId: source.source_id }}><div><span className="source-type">{source.type}</span><strong>{sourceLabel(source)}</strong></div><span className={`status-pill status-${source.status}`}>{source.status}</span><span>{source.chunk_count}</span><time dateTime={source.created_at}>{formatActivity(source.created_at)}</time></Link>)}
    </div>}
  </section>;
}

export function KnowledgeSourceDetailPage() {
  const queryClient = useQueryClient(); const { sourceId } = useParams({ from: "/knowledge/$sourceId" });
  const query = useQuery({ queryKey: ["knowledge-source", workspaceID, sourceId], queryFn: () => getJSON<KnowledgeSource>(`/api/v1/knowledge/sources/${encodeURIComponent(sourceId)}`) });
  const reprocess = useMutation({ mutationFn: () => sendJSON<never, KnowledgeSource>(`/api/v1/knowledge/sources/${encodeURIComponent(sourceId)}/reprocess`, "POST"), onSuccess: (data) => { queryClient.setQueryData(["knowledge-source", workspaceID, sourceId], data); void queryClient.invalidateQueries({ queryKey: ["knowledge-sources", workspaceID] }); } });
  const remove = useMutation({ mutationFn: () => sendJSON<never, { deleted: boolean }>(`/api/v1/knowledge/sources/${encodeURIComponent(sourceId)}`, "DELETE"), onSuccess: () => { window.location.assign("/knowledge"); } });
  const source = query.data;
  return <section className="conversation-detail"><Link className="back-link" to="/knowledge">← Knowledge sources</Link><div className="eyebrow">Knowledge source</div><h1>{source?.title ?? "Loading…"}</h1>
    {query.error && <p className="notice error">{query.error.message}</p>}{source && <><dl className="source-details"><div><dt>Type</dt><dd>{source.type}</dd></div><div><dt>Status</dt><dd><span className={`status-pill status-${source.status}`}>{source.status}</span></dd></div><div><dt>Chunks</dt><dd>{source.chunk_count}</dd></div><div><dt>Created</dt><dd>{formatActivity(source.created_at)}</dd></div>{source.source_url && <div><dt>URL</dt><dd>{source.source_url}</dd></div>}{source.file_name && <div><dt>File</dt><dd>{source.file_name} · {((source.file_size ?? 0) / 1024).toFixed(1)} KiB</dd></div>}</dl>
    {source.error && <p className="notice error"><strong>Processing failed:</strong> {source.error}</p>}<div className="config-actions">{source.type !== "text" && <button type="button" disabled={reprocess.isPending} onClick={() => reprocess.mutate()}>{reprocess.isPending ? "Processing…" : "Reprocess"}</button>}<button className="danger-button" type="button" disabled={remove.isPending} onClick={() => { if (window.confirm("Delete this knowledge source and all of its chunks? Retrieval will change immediately.")) remove.mutate(); }}>Delete source</button></div>{(reprocess.error || remove.error) && <p className="notice error">{(reprocess.error ?? remove.error)?.message}</p>}</>}
  </section>;
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
