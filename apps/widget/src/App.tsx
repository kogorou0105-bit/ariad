import type {
  APIErrorResponse,
  AnswerCitation,
  AppSurface,
  ConversationTurn,
  GetConversationHistoryResponse,
  SubmitKnowledgeRequest,
  SubmitKnowledgeResponse,
  SubmitQuestionRequest,
  SubmitQuestionResponse,
  SubmitURLRequest,
} from "@ariad/contracts";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";

const surface: AppSurface = "widget";
const workspaceID = "ws_dev";
const agentID = "agent_dev";

function createID(prefix: string): string {
  return `${prefix}_${crypto.randomUUID()}`;
}

function visitorID(): string {
  const storageKey = "ariad:visitor_id";
  const existing = sessionStorage.getItem(storageKey);
  if (existing) return existing;
  const created = createID("visitor");
  sessionStorage.setItem(storageKey, created);
  return created;
}

function storedConversationID(): string {
  return sessionStorage.getItem("ariad:conversation_id") ?? "";
}

async function postJSON<Request, Response>(path: string, body: Request): Promise<Response> {
  const response = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
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
    throw new Error(failure.error?.message ?? `Request failed (${response.status})`);
  }
  return payload as Response;
}

async function getJSON<Response>(path: string): Promise<Response> {
  const response = await fetch(path);
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
    throw new Error(failure.error?.message ?? `Request failed (${response.status})`);
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
      <span className="disposition">{disposition}</span>
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
  const [sourceTitle, setSourceTitle] = useState("Product knowledge");
  const [sourceText, setSourceText] = useState("");
  const [sourceURL, setSourceURL] = useState("");
  const [sourceResult, setSourceResult] =
    useState<SubmitKnowledgeResponse | null>(null);
  const [question, setQuestion] = useState("");
  const [submittedQuestion, setSubmittedQuestion] = useState("");
  const [conversationID, setConversationID] = useState(storedConversationID);
  const [currentVisitorID] = useState(visitorID);

  const historyQuery = useQuery({
    queryKey: ["conversation", workspaceID, currentVisitorID, conversationID],
    queryFn: () =>
      getJSON<GetConversationHistoryResponse>(
        `/api/v1/conversations/${encodeURIComponent(conversationID)}` +
          `?workspace_id=${encodeURIComponent(workspaceID)}` +
          `&visitor_id=${encodeURIComponent(currentVisitorID)}`,
      ),
    enabled: conversationID !== "",
  });

  const knowledgeMutation = useMutation({
    mutationFn: (request: SubmitKnowledgeRequest) =>
      postJSON<SubmitKnowledgeRequest, SubmitKnowledgeResponse>(
        "/api/v1/knowledge/text",
        request,
      ),
    onMutate: () => setSourceResult(null),
    onSuccess: (response) => {
      setSourceText("");
      setSourceResult(response);
    },
  });

  const urlMutation = useMutation({
    mutationFn: (request: SubmitURLRequest) =>
      postJSON<SubmitURLRequest, SubmitKnowledgeResponse>("/api/v1/ingestion/url", request),
    onMutate: () => setSourceResult(null),
    onSuccess: (response) => {
      setSourceURL("");
      setSourceResult(response);
    },
  });

  const questionMutation = useMutation({
    mutationFn: (request: SubmitQuestionRequest) =>
      postJSON<SubmitQuestionRequest, SubmitQuestionResponse>("/api/v1/questions", request),
    onSuccess: (response) => {
      sessionStorage.setItem("ariad:conversation_id", response.conversation_id);
      setConversationID(response.conversation_id);
      void queryClient.invalidateQueries({
        queryKey: ["conversation", workspaceID, currentVisitorID, response.conversation_id],
      });
    },
  });

  function submitKnowledge(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!sourceText.trim()) return;
    urlMutation.reset();
    knowledgeMutation.mutate({
      workspace_id: workspaceID,
      request_id: createID("req"),
      idempotency_key: createID("ik"),
      title: sourceTitle.trim() || "Pasted text",
      text: sourceText,
    });
  }

  function submitURL(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmed = sourceURL.trim();
    if (!trimmed) return;
    knowledgeMutation.reset();
    urlMutation.mutate({
      workspace_id: workspaceID,
      url: trimmed,
      title: sourceTitle.trim(),
      request_id: createID("req"),
      idempotency_key: createID("ik"),
    });
  }

  function submitQuestion(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmed = question.trim();
    if (!trimmed) return;
    setSubmittedQuestion(trimmed);
    questionMutation.mutate({
      workspace_id: workspaceID,
      agent_id: agentID,
      conversation_id: conversationID,
      visitor_id: currentVisitorID,
      channel: "widget",
      locale: navigator.language || "en",
      request_id: createID("req"),
      idempotency_key: createID("ik"),
      question: trimmed,
    });
    setQuestion("");
  }

  const answer = questionMutation.data;
  const turns = historyQuery.data?.turns ?? [];
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
          <p className="label">Knowledge</p>
          <form className="knowledge-form" onSubmit={submitKnowledge}>
            <label htmlFor="source-title">Source title</label>
            <input
              id="source-title"
              value={sourceTitle}
              onChange={(event) => setSourceTitle(event.target.value)}
            />
            <label htmlFor="source-text">Paste knowledge</label>
            <textarea
              id="source-text"
              value={sourceText}
              onChange={(event) => setSourceText(event.target.value)}
              placeholder="Paste facts the assistant may use…"
              rows={5}
            />
            <button
              type="submit"
              disabled={knowledgeMutation.isPending || urlMutation.isPending || !sourceText.trim()}
            >
              {knowledgeMutation.isPending ? "Saving…" : "Add knowledge"}
            </button>
          </form>

          <form className="knowledge-form url-form" onSubmit={submitURL}>
            <label htmlFor="source-url">Or add a webpage URL</label>
            <input
              id="source-url"
              type="url"
              value={sourceURL}
              onChange={(event) => setSourceURL(event.target.value)}
              placeholder="https://example.com/help"
            />
            <button
              type="submit"
              disabled={urlMutation.isPending || knowledgeMutation.isPending || !sourceURL.trim()}
            >
              {urlMutation.isPending ? "Fetching…" : "Add URL"}
            </button>
          </form>

          {sourceResult && (
            <p className="notice success">
              Source ready · {sourceResult.chunk_count} chunk
              {sourceResult.chunk_count === 1 ? "" : "s"}
            </p>
          )}
          {knowledgeMutation.error && (
            <p className="notice error">{knowledgeMutation.error.message}</p>
          )}
          {urlMutation.error && <p className="notice error">{urlMutation.error.message}</p>}

          <p className="label question-label">Conversation</p>
          <div className="message assistant">
            Ask me about the knowledge above. I’ll cite the source I use and refuse when the
            evidence is insufficient.
          </div>

          {historyQuery.isPending && conversationID && (
            <div className="message assistant muted">Loading conversation…</div>
          )}
          {historyQuery.error && (
            <p className="notice error">{historyQuery.error.message}</p>
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
      </section>
    </main>
  );
}
