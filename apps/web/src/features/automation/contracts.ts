export const runKinds = [
  "decompose",
  "schedule",
  "forecast",
  "risk",
  "efficiency",
] as const;
export type RunKind = (typeof runKinds)[number];
export type AutomationTab =
  "runs" | "policy" | "forecasts" | "risks" | "quality" | "improvements";

export interface AutomationPolicy {
  version: number;
  enabled: boolean;
  allowed_kinds: string[];
  allowed_operations: string[];
  allowed_entities?: string[];
  allowed_fields: string[];
  allowed_item_ids: string[];
  allowed_member_ids: string[];
  allowed_page_ids: string[];
  hard_deadline?: string | null;
  max_changes: number;
  budget_calls: number;
  min_interval_seconds: number;
  max_rounds: number;
  [key: string]: unknown;
}

export interface AutomationRun {
  id: string;
  kind: string;
  status: string;
  created_at: string;
  updated_at: string;
  algorithm_version: string;
  policy_version: number;
  input_fingerprint: string;
  cause: string;
  round: number;
  failure?: unknown;
  attempts: number;
  batch_id?: string;
  input?: Record<string, unknown>;
  output?: Record<string, unknown>;
  undo_conflicts?: unknown;
  before_state?: AppliedState[];
  after_state?: AppliedState[];
}

export interface AppliedState {
  item_id: string;
  version: number;
  fingerprint?: string;
  fields: Record<string, unknown>;
  created: boolean;
}

export interface Forecast {
  id?: string;
  as_of: string;
  status: string;
  p50?: string | null;
  p80?: string | null;
  sample_count: number;
  coverage_start?: string;
  remaining: number;
  assumptions?: unknown;
  backtest?: unknown;
  stale?: boolean;
  algorithm_version: string;
  [key: string]: unknown;
}

export interface Risk {
  id: string;
  version: number;
  type: string;
  key: string;
  severity: string;
  status: string;
  reason: string;
  evidence?: unknown;
  item_ids: string[];
  action_item_id?: string;
  first_seen: string;
  last_seen: string;
  history?: unknown;
}

export interface Improvement {
  id: string;
  status: string;
  reason: string;
  baseline?: unknown;
  observed?: unknown;
  window_start: string;
  window_end: string;
  target_metric: string;
  action_item_id?: string;
  [key: string]: unknown;
}

export interface GitHubBinding {
  id: string;
  installation_id: number;
  repository_id: number;
  repository: string;
  active: boolean;
  sync_status: string;
  last_synced_at?: string | null;
  last_error?: string | null;
  next_retry_at?: string | null;
}

export interface GitHubConnection {
  configured: boolean;
  install_url?: string;
  bindings: GitHubBinding[];
  grants?: {
    installation_id: number;
    repositories: { id: number; full_name: string }[];
    expires_at: string;
  }[];
  required_permissions?: Record<string, string>;
}

export interface QualityDimension {
  status?: string;
  findings?: unknown;
  summary?: unknown;
  [key: string]: unknown;
}

export interface QualityReport {
  id: string;
  binding_id: string;
  repository: string;
  commit_sha: string;
  pull_request?: number | null;
  run_id?: number | null;
  run_attempt?: number | null;
  source_revision?: unknown;
  dimensions: {
    code?: QualityDimension;
    documentation?: QualityDimension;
    tests?: QualityDimension;
  };
  created_at: string;
  evidence?: unknown;
  [key: string]: unknown;
}
