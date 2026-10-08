import type { GetConversationHistoryResponse, ListConversationsResponse, ListVisitorsResponse } from "@ariad/contracts";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams, useSearch } from "@tanstack/react-router";
import { useState } from "react";

import { formatActivity, getJSON, workspaceID } from "../api";

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

