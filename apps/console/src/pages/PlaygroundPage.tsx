import type { PlaygroundAskRequest, PlaygroundAskResponse } from "@ariad/contracts";
import { useMutation } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";

import { APIRequestError, playgroundHistoryStorageKey, sendJSON, workspaceID } from "../api";

interface PlaygroundHistoryItem { id: string; asked_at: string; question: string; top_k: number; threshold: number; response: PlaygroundAskResponse; }

function readPlaygroundHistory(): PlaygroundHistoryItem[] {
  try { const value = JSON.parse(localStorage.getItem(playgroundHistoryStorageKey) ?? "[]") as unknown; return Array.isArray(value) ? value.slice(0, 20) as PlaygroundHistoryItem[] : []; } catch { return []; }
}

function PlaygroundResult({ item }: { item: PlaygroundHistoryItem }) {
  return <article className="playground-result"><header><div><span className="source-type">{item.response.outcome === "answered" ? "Answer" : "No matches"}</span><h2>{item.question}</h2></div><div className="playground-metrics"><strong>{item.response.hit_count} hits</strong><span>{item.response.duration_ms} ms total</span><span>{item.response.retrieval_duration_ms} ms retrieval</span><span>top {item.top_k} · ≥ {item.threshold.toFixed(2)}</span></div></header>
    {item.response.outcome === "no_matches" ? <p className="empty-state">No knowledge chunks met this relevance threshold. Lower the threshold or try another question.</p> : <p className="playground-answer">{item.response.answer}</p>}
    {item.response.hits.length > 0 && <div className="playground-hits">{item.response.hits.map((hit, index) => <details key={hit.evidence_id} open={index === 0}><summary><span><strong>{hit.source_title}</strong><small>{hit.chunk_id}</small></span><b>{(hit.score * 100).toFixed(1)}%</b></summary><p>{hit.text}</p></details>)}</div>}
  </article>;
}

export function PlaygroundPage() {
  const [question, setQuestion] = useState(""); const [topK, setTopK] = useState(5); const [threshold, setThreshold] = useState(0.35); const [history, setHistory] = useState<PlaygroundHistoryItem[]>(readPlaygroundHistory); const [selected, setSelected] = useState<PlaygroundHistoryItem | null>(history[0] ?? null);
  const ask = useMutation({ mutationFn: (body: PlaygroundAskRequest) => sendJSON<PlaygroundAskRequest, PlaygroundAskResponse>("/api/v1/playground/ask", "POST", body), onSuccess: (response) => { const item = { id: crypto.randomUUID(), asked_at: new Date().toISOString(), question: question.trim(), top_k: topK, threshold, response }; const next = [item, ...history].slice(0, 20); setHistory(next); setSelected(item); localStorage.setItem(playgroundHistoryStorageKey, JSON.stringify(next)); } });
  const error = ask.error instanceof APIRequestError ? ask.error : null;
  return <section className="playground-page"><header className="page-header"><div><div className="eyebrow">Workspace / Playground</div><h1>Test an answer</h1><p className="lede-small">Tune retrieval for this run without changing workspace settings. History stays in this browser.</p></div></header>
    <div className="playground-grid"><form className="playground-form" onSubmit={(event) => { event.preventDefault(); ask.mutate({ workspace_id: workspaceID, question: question.trim(), top_k: topK, threshold, locale: navigator.language }); }}><label htmlFor="playground-question">Question</label><textarea id="playground-question" value={question} onChange={(event) => setQuestion(event.target.value)} placeholder="Ask something from your knowledge base…" required />
      <div className="playground-controls"><label htmlFor="playground-top-k">Top-k <strong>{topK}</strong><input id="playground-top-k" type="range" min="1" max="20" value={topK} onChange={(event) => setTopK(Number(event.target.value))} /></label><label htmlFor="playground-threshold">Minimum relevance <strong>{threshold.toFixed(2)}</strong><input id="playground-threshold" type="range" min="0" max="1" step="0.05" value={threshold} onChange={(event) => setThreshold(Number(event.target.value))} /></label></div>
      <button type="submit" disabled={ask.isPending || !question.trim()}>{ask.isPending ? "Testing…" : "Run test"}</button>{error?.code === "model_not_configured" && <p className="notice error">No model API key is configured. <Link to="/settings/model">Configure a model →</Link></p>}{error?.code === "no_knowledge" && <p className="notice error">No ready knowledge is available. <Link to="/knowledge">Add a knowledge source →</Link></p>}{ask.error && !["model_not_configured", "no_knowledge"].includes(error?.code ?? "") && <p className="notice error">{ask.error.message}</p>}</form>
      <aside className="playground-history"><div><strong>Recent tests</strong>{history.length > 0 && <button type="button" onClick={() => { setHistory([]); setSelected(null); localStorage.removeItem(playgroundHistoryStorageKey); }}>Clear</button>}</div>{history.length === 0 ? <p>No tests yet.</p> : history.map((item) => <button className={selected?.id === item.id ? "is-selected" : ""} type="button" key={item.id} onClick={() => { setSelected(item); setQuestion(item.question); setTopK(item.top_k); setThreshold(item.threshold); }}><strong>{item.question}</strong><span>{item.response.hit_count} hits · {item.response.duration_ms} ms · {new Date(item.asked_at).toLocaleString()}</span></button>)}</aside></div>
    {selected && <PlaygroundResult item={selected} />}
  </section>;
}


