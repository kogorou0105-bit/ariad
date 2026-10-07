import type {
  APIErrorResponse,
  AnswerCitation,
  AppSurface,
  ConversationTurn,
  GetConversationHistoryResponse,
  HandoffRequest,
  SubmitQuestionRequest,
  SubmitQuestionResponse,
  VisitorSessionResponse,
} from "@ariad/contracts";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useEffect, useState } from "react";

const surface: AppSurface = "widget";
const workspaceID = "ws_dev";
const agentID = "agent_dev";

function createID(prefix: string): string {
  return `${prefix}_${crypto.randomUUID()}`;
}

const visitorTokenKey = "ariad:visitor_session_token";
const visitorIDKey = "ariad:visitor_id";
const visitorRefreshTokenKey = "ariad:visitor_refresh_token";
const conversationSnapshotKey = "ariad:conversation_snapshot";

function storedConversationSnapshot(): GetConversationHistoryResponse | null {
  const value = localStorage.getItem(conversationSnapshotKey);
  if (!value) return null;
  try { return JSON.parse(value) as GetConversationHistoryResponse; } catch { localStorage.removeItem(conversationSnapshotKey); return null; }
}

function storedConversationID(): string {
  return sessionStorage.getItem("ariad:conversation_id") ?? "";
}

class VisitorAPIError extends Error { constructor(message: string, readonly code: string) { super(message); } }

async function postJSON<Request, Response>(path: string, body: Request, token?: string): Promise<Response> {
  const response = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    body: JSON.stringify(body),
  });
  const responseText = await response.text();
  let payload: unknown;
  try {
    payload = JSON.parse(responseText) as unknown;
  } catch {
    throw new Error(
      response.ok
        ? "The API returned an invalid response."
        : `Request failed (${response.status}).`,
    );
  }
  if (!response.ok) {
    const failure = payload as APIErrorResponse;
    throw new VisitorAPIError(failure.error?.message ?? `Request failed (${response.status})`, failure.error?.code ?? "request_failed");
  }
  return payload as Response;
}

async function getJSON<Response>(path: string, token: string): Promise<Response> {
  const response = await fetch(path, { headers: { Authorization: `Bearer ${token}` } });
  const responseText = await response.text();
  let payload: unknown;
  try {
    payload = JSON.parse(responseText) as unknown;
  } catch {
    throw new Error(
      response.ok
        ? "The API returned an invalid response."
        : `Request failed (${response.status}).`,
    );
  }
  if (!response.ok) {
    const failure = payload as APIErrorResponse;
    throw new VisitorAPIError(failure.error?.message ?? `Request failed (${response.status})`, failure.error?.code ?? "request_failed");
  }
  return payload as Response;
}

function AnswerCard({
  answer,
  citations,
  disposition,
}: {
  answer: string;
  citations: AnswerCitation[];
  disposition: ConversationTurn["terminal_disposition"];
}) {
  return (
    <article className={`answer ${disposition}`}>
      <span className="disposition">AI · {disposition}</span>
      <p>{answer}</p>
      {citations.length > 0 && (
        <div className="citations">
          <strong>Sources</strong>
          {citations.map((citation) => (
            <blockquote key={citation.citation_id}>
              <cite>{citation.source_title}</cite>
              {citation.quote}
            </blockquote>
          ))}
        </div>
      )}
    </article>
  );
}

export function WidgetPreview() {
  const queryClient = useQueryClient();
  const [question, setQuestion] = useState("");
  const [submittedQuestion, setSubmittedQuestion] = useState("");
  const [conversationID, setConversationID] = useState(storedConversationID);
  const [visitorToken, setVisitorToken] = useState(() => localStorage.getItem(visitorTokenKey) ?? "");
  const [visitorRefreshToken, setVisitorRefreshToken] = useState(() => localStorage.getItem(visitorRefreshTokenKey) ?? "");
  const [currentVisitorID, setCurrentVisitorID] = useState(() => localStorage.getItem(visitorIDKey) ?? "");
  const [preservedHistory, setPreservedHistory] = useState<GetConversationHistoryResponse | null>(storedConversationSnapshot);

  const sessionMutation = useMutation({
    mutationFn: () => visitorRefreshToken
      ? postJSON<{ workspace_id: string; refresh_token: string }, VisitorSessionResponse>("/api/v1/visitor/sessions/refresh", { workspace_id: workspaceID, refresh_token: visitorRefreshToken })
      : postJSON<{ workspace_id: string }, VisitorSessionResponse>("/api/v1/visitor/sessions", { workspace_id: workspaceID }),
    onSuccess: (session) => {
	  const createdIdentity = visitorRefreshToken === "";
      localStorage.setItem(visitorTokenKey, session.token);
      localStorage.setItem(visitorIDKey, session.visitor_id);
      if (session.refresh_token) {
        localStorage.setItem(visitorRefreshTokenKey, session.refresh_token);
        setVisitorRefreshToken(session.refresh_token);
		if (createdIdentity) {
		  sessionStorage.removeItem("ariad:conversation_id");
		  setConversationID("");
		}
      }
      setVisitorToken(session.token); setCurrentVisitorID(session.visitor_id);
    },
    onError: (error) => {
      if (error instanceof VisitorAPIError && error.code === "visitor_refresh_invalid") {
        localStorage.removeItem(visitorRefreshTokenKey); localStorage.removeItem(visitorIDKey); setVisitorRefreshToken(""); setCurrentVisitorID("");
      }
    },
  });

  useEffect(() => { if (!visitorToken && !sessionMutation.isPending) sessionMutation.mutate(); }, [visitorToken, sessionMutation]);

  function renewVisitorSession(error: Error | null, history?: GetConversationHistoryResponse) {
    if (!(error instanceof VisitorAPIError) || !["visitor_session_invalid", "visitor_session_expired"].includes(error.code)) return;
    if (history) setPreservedHistory(history);
    localStorage.removeItem(visitorTokenKey); setVisitorToken("");
  }

  const historyQuery = useQuery({
    queryKey: ["conversation", workspaceID, currentVisitorID, conversationID],
    queryFn: async () => {
      try { const history = await getJSON<GetConversationHistoryResponse>(
        `/api/v1/conversations/${encodeURIComponent(conversationID)}?workspace_id=${encodeURIComponent(workspaceID)}`,
        visitorToken,
      ); localStorage.setItem(conversationSnapshotKey, JSON.stringify(history)); return history; } catch (error) { renewVisitorSession(error as Error, queryClient.getQueryData<GetConversationHistoryResponse>(["conversation", workspaceID, currentVisitorID, conversationID]) ?? preservedHistory ?? undefined); throw error; }
    },
    enabled: conversationID !== "" && visitorToken !== "",
  });

  const questionMutation = useMutation({
    mutationFn: (request: SubmitQuestionRequest) =>
      postJSON<SubmitQuestionRequest, SubmitQuestionResponse>("/api/v1/questions", request, visitorToken),
    onSuccess: (response) => {
      sessionStorage.setItem("ariad:conversation_id", response.conversation_id);
      setConversationID(response.conversation_id);
      setPreservedHistory(null);
      localStorage.removeItem(conversationSnapshotKey);
      void queryClient.invalidateQueries({
        queryKey: ["conversation", workspaceID, currentVisitorID, response.conversation_id],
      });
    },
    onError: (error) => renewVisitorSession(error),
  });

  const handoffMutation = useMutation({
    mutationFn: (request: HandoffRequest) => postJSON<HandoffRequest, { status: string }>(
      `/api/v1/conversations/${encodeURIComponent(conversationID)}/handoff`, request, visitorToken,
    ),
    onMutate: async () => {
      const key = ["conversation", workspaceID, currentVisitorID, conversationID];
      await queryClient.cancelQueries({ queryKey: key });
      const previous = queryClient.getQueryData<GetConversationHistoryResponse>(key);
      if (previous) queryClient.setQueryData<GetConversationHistoryResponse>(key, { ...previous, status: "pending", handoff_reason: "visitor_requested" });
      return { key, previous };
    },
    onError: (error, _request, context) => { if (context?.previous) queryClient.setQueryData(context.key, context.previous); renewVisitorSession(error); },
    onSettled: () => queryClient.invalidateQueries({ queryKey: ["conversation", workspaceID, currentVisitorID, conversationID] }),
  });

  function submitQuestion(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmed = question.trim();
    if (!trimmed) return;
    setSubmittedQuestion(trimmed);
    questionMutation.mutate({
      workspace_id: workspaceID,
      agent_id: agentID,
      conversation_id: conversationID,
      channel: "widget",
      locale: navigator.language || "en",
      request_id: createID("req"),
      idempotency_key: createID("ik"),
      question: trimmed,
    });
    setQuestion("");
  }

  const answer = questionMutation.data;
  const visibleHistory = historyQuery.data ?? preservedHistory;
  const turns = visibleHistory?.turns ?? [];
  const humanReplies = visibleHistory?.human_replies ?? [];
  const answerAlreadyLoaded = turns.some((turn) => turn.answer_id === answer?.answer_id);

  return (
    <main className="preview" data-surface={surface}>
      <section className="widget" aria-label="Ariad support preview">
        <header>
          <div className="avatar" aria-hidden="true">A</div>
          <div>
            <strong>Ariad Support</strong>
            <span><i /> Grounded in verified sources</span>
          </div>
        </header>

        <div className="conversation" aria-live="polite">
          <p className="label">Conversation</p>
          <div className="message assistant">
            Ask me about the available knowledge. I’ll cite the source I use and refuse when
            the evidence is insufficient. Knowledge is managed in the Ariad Console.
          </div>

          {historyQuery.isPending && conversationID && (
            <div className="message assistant muted">Loading conversation…</div>
          )}
          {historyQuery.error && (
            <p className="notice error">{historyQuery.error.message}</p>
          )}
          {visibleHistory?.status === "pending" && (
            <p className="handoff-status">You’re in the human support queue. A support specialist will reply here.</p>
          )}
          {turns.map((turn) => (
            <div className="turn" key={turn.message_id}>
              <div className="message visitor">{turn.message}</div>
              <AnswerCard
                answer={turn.answer}
                citations={turn.citations}
                disposition={turn.terminal_disposition}
              />
            </div>
          ))}
          {humanReplies.map((reply) => (
            <article className="answer human-answer" key={reply.reply_id}>
              <span className="disposition">Human support</span>
              <p>{reply.text}</p>
            </article>
          ))}
          {questionMutation.isPending && submittedQuestion && (
            <>
              <div className="message visitor">{submittedQuestion}</div>
              <div className="message assistant muted">Thinking…</div>
            </>
          )}
          {questionMutation.error && (
            <p className="notice error">{questionMutation.error.message}</p>
          )}
          {answer && !questionMutation.isPending && !answerAlreadyLoaded && (
            <div className="turn">
              <div className="message visitor">{submittedQuestion}</div>
              <AnswerCard
                answer={answer.answer}
                citations={answer.citations}
                disposition={answer.terminal_disposition}
              />
            </div>
          )}
        </div>

        <form className="question-form" onSubmit={submitQuestion}>
          <label htmlFor="question" className="sr-only">Ask a question</label>
          <input
            id="question"
            value={question}
            onChange={(event) => setQuestion(event.target.value)}
            placeholder="Ask a question…"
            autoComplete="off"
          />
          <button
            type="submit"
            aria-label="Send question"
            disabled={questionMutation.isPending || !question.trim()}
          >
            →
          </button>
        </form>
        <div className="handoff-action">
          <button type="button" disabled={!conversationID || !visitorToken || handoffMutation.isPending || visibleHistory?.status === "pending"} onClick={() => handoffMutation.mutate({ workspace_id: workspaceID, reason: "visitor_requested" })}>
            {visibleHistory?.status === "pending" ? "Waiting for human support" : handoffMutation.isPending ? "Requesting…" : "Talk to a human"}
          </button>
          {handoffMutation.error && <p className="notice error">{handoffMutation.error.message}</p>}
        </div>
      </section>
    </main>
  );
}
