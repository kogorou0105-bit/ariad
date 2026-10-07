/** The browser surface consuming a shared transport contract. */
export type AppSurface = "console" | "widget";

/** Minimal API health response shared by browser applications. */
export interface HealthStatus {
  status: "ok";
  service: "api";
}

/** Every answer attempt ends in exactly one server-owned terminal state. */
export type TerminalDisposition = "answered" | "clarify" | "refused" | "handoff";

export interface SubmitKnowledgeRequest {
  workspace_id: string;
  request_id: string;
  idempotency_key: string;
  title: string;
  text: string;
}

export interface SubmitKnowledgeResponse {
  workspace_id: string;
  request_id: string;
  source_id: string;
  chunk_count: number;
}

export interface SubmitURLRequest {
  workspace_id: string;
  url: string;
  title: string;
  request_id: string;
  idempotency_key: string;
}

export interface SubmitQuestionRequest {
  workspace_id: string;
  agent_id: string;
  conversation_id: string;
  visitor_id: string;
  channel: "widget";
  locale: string;
  request_id: string;
  idempotency_key: string;
  question: string;
}

export interface AnswerCitation {
  citation_id: string;
  evidence_id: string;
  source_id: string;
  chunk_id: string;
  source_title: string;
  quote: string;
}

export interface SubmitQuestionResponse {
  workspace_id: string;
  agent_id: string;
  conversation_id: string;
  message_id: string;
  answer_id: string;
  request_id: string;
  terminal_disposition: TerminalDisposition;
  answer: string;
  citations: AnswerCitation[];
}

export interface ConversationTurn {
  message_id: string;
  message: string;
  message_created_at: string;
  answer_id: string;
  terminal_disposition: TerminalDisposition;
  answer: string;
  answer_created_at: string;
  citations: AnswerCitation[];
}

export interface GetConversationHistoryResponse {
  workspace_id: string;
  conversation_id: string;
  status: "ongoing" | "pending" | "resolved";
  handoff_reason?: string;
  turns: ConversationTurn[];
  human_replies: HumanReply[];
}

export interface HumanReply { reply_id: string; source: "human"; author_id: string; text: string; created_at: string; }
export interface HandoffRequest { workspace_id: string; visitor_id: string; reason: string; }
export interface ReviewQueueItem { conversation_id: string; visitor_id: string; reason: string; requested_by: string; requested_at: string; last_activity_at: string; last_message_text: string; }
export interface ListReviewsResponse { workspace_id: string; reviews: ReviewQueueItem[]; }
export interface ReviewReplyRequest { workspace_id: string; visitor_id: string; text: string; }
export interface ResolveReviewRequest { workspace_id: string; visitor_id: string; }

export interface ConversationSummary {
  conversation_id: string;
  visitor_id: string;
  message_count: number;
  last_activity_at: string;
  last_message_text: string;
}

export interface ListConversationsResponse {
  workspace_id: string;
  conversations: ConversationSummary[];
}

export type ModelConfigSource = "workspace" | "system_default" | "local";

export interface ModelConfigResponse {
  workspace_id: string;
  source: ModelConfigSource;
  base_url: string;
  model: string;
  api_key_mask: string;
}

export interface SaveModelConfigRequest {
  workspace_id: string;
  base_url: string;
  model: string;
  api_key: string;
}

export interface APIErrorResponse {
  error: {
    code: string;
    message: string;
    request_id: string;
  };
}
