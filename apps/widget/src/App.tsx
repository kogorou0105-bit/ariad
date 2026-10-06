import type {
  APIErrorResponse,
  AppSurface,
  SubmitKnowledgeRequest,
  SubmitKnowledgeResponse,
  SubmitQuestionRequest,
  SubmitQuestionResponse,
} from "@ariad/contracts";
import { useMutation } from "@tanstack/react-query";
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

export function WidgetPreview() {
  const [sourceTitle, setSourceTitle] = useState("Product knowledge");
  const [sourceText, setSourceText] = useState("");
  const [question, setQuestion] = useState("");
  const [submittedQuestion, setSubmittedQuestion] = useState("");
  const [conversationID, setConversationID] = useState("");
  const [currentVisitorID] = useState(visitorID);

  const knowledgeMutation = useMutation({
    mutationFn: (request: SubmitKnowledgeRequest) =>
      postJSON<SubmitKnowledgeRequest, SubmitKnowledgeResponse>(
        "/api/v1/knowledge/text",
        request,
      ),
    onSuccess: () => {
      setSourceText("");
    },
  });

  const questionMutation = useMutation({
    mutationFn: (request: SubmitQuestionRequest) =>
      postJSON<SubmitQuestionRequest, SubmitQuestionResponse>("/api/v1/questions", request),
    onSuccess: (response) => {
      setConversationID(response.conversation_id);
    },
  });

  function submitKnowledge(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!sourceText.trim()) return;
    knowledgeMutation.mutate({
      workspace_id: workspaceID,
      request_id: createID("req"),
      idempotency_key: createID("ik"),
      title: sourceTitle.trim() || "Pasted text",
      text: sourceText,
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
            <button type="submit" disabled={knowledgeMutation.isPending || !sourceText.trim()}>
              {knowledgeMutation.isPending ? "Saving…" : "Add knowledge"}
            </button>
          </form>

          {knowledgeMutation.data && (
            <p className="notice success">
              Source ready · {knowledgeMutation.data.chunk_count} chunk
              {knowledgeMutation.data.chunk_count === 1 ? "" : "s"}
            </p>
          )}
          {knowledgeMutation.error && (
            <p className="notice error">{knowledgeMutation.error.message}</p>
          )}

          <p className="label question-label">Conversation</p>
          <div className="message assistant">
            Ask me about the knowledge above. I’ll cite the source I use and refuse when the
            evidence is insufficient.
          </div>

          {submittedQuestion && <div className="message visitor">{submittedQuestion}</div>}
          {questionMutation.isPending && <div className="message assistant muted">Thinking…</div>}
          {questionMutation.error && (
            <p className="notice error">{questionMutation.error.message}</p>
          )}
          {answer && (
            <article className={`answer ${answer.terminal_disposition}`}>
              <span className="disposition">{answer.terminal_disposition}</span>
              <p>{answer.answer}</p>
              {answer.citations.length > 0 && (
                <div className="citations">
                  <strong>Sources</strong>
                  {answer.citations.map((citation) => (
                    <blockquote key={citation.citation_id}>
                      <cite>{citation.source_title}</cite>
                      {citation.quote}
                    </blockquote>
                  ))}
                </div>
              )}
            </article>
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
