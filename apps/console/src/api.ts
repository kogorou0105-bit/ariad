import type { APIErrorResponse, AppSurface, UploadKnowledgeFilesResponse } from "@ariad/contracts";

export const surface: AppSurface = "console";
export const workspaceID = "ws_dev";
export const adminTokenStorageKey = "ariad:admin_session";
export const administratorIDStorageKey = "ariad:administrator_id";
export const adminUnauthorizedEvent = "ariad:admin-unauthorized";
export const playgroundHistoryStorageKey = "ariad:playground-history:v1";

export class APIRequestError extends Error { constructor(message: string, readonly code: string) { super(message); } }

export async function requestJSON<Response>(
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
    throw new APIRequestError(failure.error?.message ?? `Request failed (${response.status})`, failure.error?.code ?? "request_failed");
  }
  return payload as Response;
}

export function getJSON<Response>(path: string): Promise<Response> {
  return requestJSON(path, sessionStorage.getItem(adminTokenStorageKey), true);
}

export function sendJSON<Request, Response>(path: string, method: "POST" | "PUT" | "DELETE", body?: Request): Promise<Response> {
  return requestJSON(path, sessionStorage.getItem(adminTokenStorageKey), true, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

export function uploadFiles(files: File[]): Promise<UploadKnowledgeFilesResponse> {
  const body = new FormData(); body.set("workspace_id", workspaceID); files.forEach((file) => body.append("files", file));
  return requestJSON("/api/v1/knowledge/files", sessionStorage.getItem(adminTokenStorageKey), true, { method: "POST", body });
}

export function formatActivity(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}


