import type { KnowledgeSource, ListKnowledgeSourcesResponse } from "@ariad/contracts";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { useState } from "react";

import { formatActivity, getJSON, sendJSON, uploadFiles, workspaceID } from "../api";

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


