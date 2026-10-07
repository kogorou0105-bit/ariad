import type {
  APIErrorResponse,
  GetConversationHistoryResponse,
  ListConversationsResponse,
} from "@ariad/contracts";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, Outlet, useParams, useSearch } from "@tanstack/react-router";

import type { AppSurface } from "@ariad/contracts";

const surface: AppSurface = "console";
const workspaceID = "ws_dev";

async function getJSON<Response>(path: string): Promise<Response> {
  const response = await fetch(path);
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

function formatActivity(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

export function ConsoleShell() {
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
          <span className="nav-item nav-item-disabled">Evaluations</span>
        </nav>
      </aside>
      <main className="main-content" data-surface={surface}>
        <Outlet />
      </main>
    </div>
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
