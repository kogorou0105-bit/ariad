import type { GetConversationHistoryResponse, ListReviewsResponse, ResolveReviewRequest, ReviewReplyRequest } from "@ariad/contracts";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams, useSearch } from "@tanstack/react-router";
import { useState } from "react";

import { formatActivity, getJSON, sendJSON, workspaceID } from "../api";

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


