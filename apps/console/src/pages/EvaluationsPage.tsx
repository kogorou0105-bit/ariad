import type { EvaluationRun, EvaluationRunRequest, EvaluationTestCaseRequest, EvaluationTestSet, EvaluationTestSetRequest, ListEvaluationRunsResponse, ListEvaluationTestSetsResponse, ListKnowledgeSourcesResponse } from "@ariad/contracts";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { type FormEvent, useState } from "react";

import { APIRequestError, formatActivity, getJSON, sendJSON, workspaceID } from "../api";

const setsKey = ["evaluation-test-sets", workspaceID];

export function EvaluationsPage() {
  const client = useQueryClient();
  const [selectedID, setSelectedID] = useState("");
  const [newName, setNewName] = useState("");
  const [question, setQuestion] = useState("");
  const [sourceID, setSourceID] = useState("");
  const [note, setNote] = useState("");
  const [editing, setEditing] = useState<string | null>(null);
  const [topK, setTopK] = useState(5);
  const [threshold, setThreshold] = useState(0.35);
  const [latest, setLatest] = useState<EvaluationRun | null>(null);
  const sets = useQuery({ queryKey: setsKey, queryFn: () => getJSON<ListEvaluationTestSetsResponse>(`/api/v1/evaluation/test-sets?workspace_id=${workspaceID}`) });
  const sources = useQuery({ queryKey: ["knowledge-sources", workspaceID], queryFn: () => getJSON<ListKnowledgeSourcesResponse>(`/api/v1/knowledge/sources?workspace_id=${workspaceID}`) });
  const selected = sets.data?.test_sets.find((item) => item.set_id === selectedID) ?? sets.data?.test_sets[0];
  const history = useQuery({ queryKey: ["evaluation-runs", workspaceID, selected?.set_id], enabled: Boolean(selected), queryFn: () => getJSON<ListEvaluationRunsResponse>(`/api/v1/evaluation/test-sets/${selected!.set_id}/runs?workspace_id=${workspaceID}`) });
  const refresh = () => void client.invalidateQueries({ queryKey: setsKey });
  const createSet = useMutation({ mutationFn: (body: EvaluationTestSetRequest) => sendJSON<EvaluationTestSetRequest, EvaluationTestSet>("/api/v1/evaluation/test-sets", "POST", body), onSuccess: (value) => { setNewName(""); setSelectedID(value.set_id); refresh(); } });
  const renameSet = useMutation({ mutationFn: (body: EvaluationTestSetRequest) => sendJSON<EvaluationTestSetRequest, EvaluationTestSet>(`/api/v1/evaluation/test-sets/${selected!.set_id}`, "PUT", body), onSuccess: refresh });
  const deleteSet = useMutation({ mutationFn: () => sendJSON<never, { deleted: boolean }>(`/api/v1/evaluation/test-sets/${selected!.set_id}?workspace_id=${workspaceID}`, "DELETE"), onSuccess: () => { setSelectedID(""); setLatest(null); refresh(); } });
  const saveCase = useMutation({ mutationFn: (body: EvaluationTestCaseRequest) => sendJSON<EvaluationTestCaseRequest, unknown>(editing ? `/api/v1/evaluation/test-sets/${selected!.set_id}/cases/${editing}` : `/api/v1/evaluation/test-sets/${selected!.set_id}/cases`, editing ? "PUT" : "POST", body), onSuccess: () => { setQuestion(""); setSourceID(""); setNote(""); setEditing(null); refresh(); } });
  const deleteCase = useMutation({ mutationFn: (caseID: string) => sendJSON<never, unknown>(`/api/v1/evaluation/test-sets/${selected!.set_id}/cases/${caseID}?workspace_id=${workspaceID}`, "DELETE"), onSuccess: refresh });
  const run = useMutation({ mutationFn: (body: EvaluationRunRequest) => sendJSON<EvaluationRunRequest, EvaluationRun>(`/api/v1/evaluation/test-sets/${selected!.set_id}/runs`, "POST", body), onSuccess: (value) => { setLatest(value); void client.invalidateQueries({ queryKey: ["evaluation-runs", workspaceID, selected?.set_id] }); } });
  const runError = run.error instanceof APIRequestError ? run.error : null;
  const activeRun = latest ?? history.data?.runs[0] ?? null;

  function submitCase(event: FormEvent) { event.preventDefault(); if (!selected) return; saveCase.mutate({ workspace_id: workspaceID, question: question.trim(), expected_source_id: sourceID, note: note.trim() }); }
  function editCase(item: EvaluationTestSet["cases"][number]) { setEditing(item.case_id); setQuestion(item.question); setSourceID(item.expected_source_id); setNote(item.note); }
  return <section className="evaluations-page"><header className="page-header"><div><div className="eyebrow">Workspace / Evaluations</div><h1>Retrieval quality</h1><p className="lede-small">Run repeatable questions against the current knowledge index. Evaluation never calls the answer model.</p></div></header>
    <div className="evaluation-layout"><aside className="evaluation-sets"><form onSubmit={(event) => { event.preventDefault(); createSet.mutate({ workspace_id: workspaceID, name: newName.trim() }); }}><input aria-label="New test set name" value={newName} onChange={(event) => setNewName(event.target.value)} placeholder="New test set"/><button disabled={!newName.trim() || createSet.isPending}>Add</button></form>{sets.isPending ? <p>Loading…</p> : sets.data?.test_sets.length === 0 ? <p>No test sets yet.</p> : sets.data?.test_sets.map((item) => <button type="button" className={selected?.set_id === item.set_id ? "is-selected" : ""} onClick={() => { setSelectedID(item.set_id); setLatest(null); }} key={item.set_id}><strong>{item.name}</strong><span>{item.cases.length} cases</span></button>)}</aside>
      <div className="evaluation-workspace">{selected ? <><div className="evaluation-toolbar"><div><h2>{selected.name}</h2><span>{selected.cases.length} test cases</span></div><div><button type="button" onClick={() => { const name = window.prompt("Rename test set", selected.name); if (name?.trim()) renameSet.mutate({ workspace_id: workspaceID, name: name.trim() }); }}>Rename</button><button type="button" className="danger-button" onClick={() => { if (window.confirm("Delete this test set? Saved run snapshots will be retained in the database.")) deleteSet.mutate(); }}>Delete</button></div></div>
        {sources.data?.sources.length === 0 ? <p className="empty-state">No knowledge sources exist. <Link to="/knowledge">Add knowledge first →</Link></p> : <form className="evaluation-case-form" onSubmit={submitCase}><label>Question<textarea value={question} onChange={(event) => setQuestion(event.target.value)} required /></label><label>Expected source<select value={sourceID} onChange={(event) => setSourceID(event.target.value)} required><option value="">Choose a source…</option>{sources.data?.sources.map((source) => <option value={source.source_id} key={source.source_id}>{source.title}</option>)}</select></label><label>Note (optional)<input value={note} onChange={(event) => setNote(event.target.value)} /></label><div><button disabled={!question.trim() || !sourceID}>{editing ? "Save case" : "Add case"}</button>{editing && <button type="button" onClick={() => { setEditing(null); setQuestion(""); setSourceID(""); setNote(""); }}>Cancel</button>}</div></form>}
        {selected.cases.length === 0 ? <p className="empty-state">This test set is empty. Add a question and its expected knowledge source.</p> : <div className="evaluation-cases">{selected.cases.map((item) => { const source = sources.data?.sources.find((entry) => entry.source_id === item.expected_source_id); return <article key={item.case_id}><div><strong>{item.question}</strong><span>{source?.title ?? "Source missing"}{item.note ? ` · ${item.note}` : ""}</span></div><button type="button" onClick={() => editCase(item)}>Edit</button><button type="button" onClick={() => deleteCase.mutate(item.case_id)}>Delete</button></article>; })}</div>}
        <div className="evaluation-run-panel"><div className="playground-controls"><label>Top-k <strong>{topK}</strong><input type="range" min="1" max="20" value={topK} onChange={(event) => setTopK(Number(event.target.value))}/></label><label>Minimum relevance <strong>{threshold.toFixed(2)}</strong><input type="range" min="0" max="1" step="0.05" value={threshold} onChange={(event) => setThreshold(Number(event.target.value))}/></label></div><button type="button" disabled={run.isPending || selected.cases.length === 0} onClick={() => run.mutate({ workspace_id: workspaceID, top_k: topK, threshold })}>{run.isPending ? "Running…" : "Run evaluation"}</button></div>
        {runError?.code === "embedding_not_configured" && <p className="notice error">No embedding model key is configured. <Link to="/settings/model">Configure semantic retrieval →</Link></p>}{runError?.code === "no_knowledge" && <p className="notice error">No knowledge sources are available.</p>}{run.error && !["embedding_not_configured", "no_knowledge"].includes(runError?.code ?? "") && <p className="notice error">{run.error.message}</p>}
        {activeRun && <RunReport run={activeRun} />}
        <div className="evaluation-history"><h3>Run history</h3>{history.data?.runs.length ? history.data.runs.map((item) => <button type="button" key={item.run_id} onClick={() => setLatest(item)}><strong>{item.evaluable_count === 0 ? "N/A" : `${(item.pass_rate * 100).toFixed(0)}%`}</strong><span>{item.passed_count}/{item.evaluable_count} evaluable passed{item.source_missing_count ? ` · ${item.source_missing_count} source missing` : ""} · top {item.top_k} · ≥ {item.threshold.toFixed(2)} · {formatActivity(item.created_at)}</span></button>) : <p>No runs yet.</p>}</div>
      </> : <p className="empty-state">Create a test set to start measuring retrieval quality.</p>}</div></div>
  </section>;
}

function RunReport({ run }: { run: EvaluationRun }) {
  const failures = run.results.filter((item) => item.outcome !== "passed");
  return <section className="evaluation-report"><header><div><span className="source-type">Latest result</span><h2>{run.evaluable_count === 0 ? "No evaluable cases" : `${(run.pass_rate * 100).toFixed(0)}% pass rate`}</h2></div><span>{run.passed_count} of {run.evaluable_count} evaluable passed{run.source_missing_count ? ` · ${run.source_missing_count} source missing` : ""} · {run.total_count} total · top {run.top_k} · ≥ {run.threshold.toFixed(2)}</span></header>{failures.length === 0 ? <p className="notice success">Every test case passed.</p> : failures.map((item) => <article key={item.case_id}><strong>{item.question}</strong><span className={`status-pill ${item.outcome === "source_missing" ? "status-processing" : "status-failed"}`}>{item.outcome === "source_missing" ? "Source missing" : "Failed"}</span><p>Expected: {item.expected_source_title || item.expected_source_id}</p><p>Actual: {item.hits.length ? item.hits.map((hit) => `${hit.source_title} ${(hit.score * 100).toFixed(1)}%`).join(", ") : "No matches"}</p></article>)}</section>;
}
