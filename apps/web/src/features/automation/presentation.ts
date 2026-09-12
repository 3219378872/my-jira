import type { AutomationRun, RunKind } from "./contracts";

export const kindLabels: Record<string, [string, string]> = {
  decompose: ["需求拆解与估算", "PRD decomposition"],
  schedule: ["智能排期", "Scheduling"],
  forecast: ["交付预测", "Delivery forecast"],
  risk: ["风险预警", "Risk detection"],
  quality: ["代码、文档与测试", "Code, documentation & tests"],
  efficiency: ["效率改善", "Process improvement"],
};

export const statusLabels: Record<string, [string, string]> = {
  queued: ["排队中", "Queued"],
  running: ["运行中", "Running"],
  applied: ["已应用", "Applied"],
  partial: ["部分完成", "Partially completed"],
  completed: ["已完成", "Completed"],
  blocked: ["受阻", "Blocked"],
  failed: ["失败", "Failed"],
  cancelled: ["已取消", "Cancelled"],
  undone: ["已撤销", "Undone"],
  passed: ["通过", "Passed"],
  skipped: ["已跳过", "Skipped"],
  missing: ["缺失", "Missing"],
  unknown: ["未知", "Unknown"],
  open: ["待处理", "Open"],
  acknowledged: ["已确认", "Acknowledged"],
  resolved: ["已解除", "Resolved"],
  observing: ["观察中", "Observing"],
  improved: ["有改善", "Improved"],
  no_improvement: ["无改善", "No improvement"],
  insufficient_evidence: ["证据不足", "Insufficient evidence"],
  insufficient_data: ["历史数据不足", "Insufficient history"],
  cold_start: ["历史数据不足", "Insufficient history"],
  ready: ["已生成", "Ready"],
  stale: ["已过期", "Stale"],
  synced: ["已同步", "Synced"],
  pending: ["待同步", "Pending"],
  revoked: ["授权已撤销", "Revoked"],
  disabled: ["已停用", "Disabled"],
  active: ["有效", "Active"],
  idle: ["等待同步", "Awaiting sync"],
  syncing: ["同步中", "Syncing"],
  retrying: ["等待重试", "Retry pending"],
  high: ["高", "High"],
  medium: ["中", "Medium"],
  low: ["低", "Low"],
  critical: ["严重", "Critical"],
};

export function statusTone(
  status: string,
): "positive" | "negative" | "warning" | "neutral" {
  if (
    [
      "passed",
      "applied",
      "completed",
      "resolved",
      "improved",
      "ready",
      "synced",
      "active",
    ].includes(status)
  )
    return "positive";
  if (["failed", "blocked", "revoked", "critical", "high"].includes(status))
    return "negative";
  if (
    [
      "partial",
      "missing",
      "skipped",
      "unknown",
      "stale",
      "insufficient_data",
      "cold_start",
      "insufficient_evidence",
      "no_improvement",
    ].includes(status)
  )
    return "warning";
  return "neutral";
}

export function availableRunActions(
  run: Pick<AutomationRun, "status" | "batch_id">,
) {
  return {
    cancel: ["queued", "running"].includes(run.status),
    retry: ["failed", "blocked", "cancelled"].includes(run.status),
    undo:
      ["applied", "partial", "completed"].includes(run.status) &&
      !!run.batch_id,
  };
}

export function parseIDs(value: string): string[] {
  return [
    ...new Set(
      value
        .split(/[\s,，]+/)
        .map((id) => id.trim())
        .filter(Boolean),
    ),
  ];
}

export function qualityStatus(value: unknown): string {
  return typeof value === "string" &&
    ["passed", "failed", "skipped", "missing", "unknown"].includes(value)
    ? value
    : "unknown";
}

export function parsePinnedSource(values: {
  commit: string;
  pullRequest: string;
  run: string;
  attempt: string;
}): Record<string, string | number> {
  const source: Record<string, string | number> = {};
  const commit = values.commit.trim();
  if (commit && !/^(?:[0-9a-f]{40}|[0-9a-f]{64})$/i.test(commit))
    throw new Error("Commit SHA must contain 40 or 64 hexadecimal characters.");
  if (commit) source.commit_sha = commit;
  for (const [key, value] of [
    ["pull_request", values.pullRequest],
    ["run_id", values.run],
    ["run_attempt", values.attempt],
  ]) {
    if (!value.trim()) continue;
    const number = Number(value);
    if (
      !/^\d+$/.test(value.trim()) ||
      !Number.isSafeInteger(number) ||
      number < 1
    )
      throw new Error(`${key} must be a positive integer.`);
    source[key] = number;
  }
  if (source.run_attempt && !source.run_id)
    throw new Error("Select an Actions run before specifying its attempt.");
  return source;
}

export function runRequest(
  kind: RunKind,
  source: {
    mode: "page" | "text";
    pageID: string;
    revision: string;
    text: string;
    key?: string;
  },
  idempotencyKey: string,
  schedule?: { start: string; end: string; taskIDs: string },
) {
  const body: {
    kind: RunKind;
    idempotency_key: string;
    cause: string;
    start_date?: string;
    end_date?: string;
    task_ids?: string[];
    source?: {
      page_id?: string;
      revision?: number;
      text?: string;
      key?: string;
    };
  } = { kind, idempotency_key: idempotencyKey, cause: "manual" };
  if (kind === "schedule" && schedule) {
    if (schedule.start && schedule.end && schedule.end < schedule.start)
      throw new Error(
        "The scheduling window must end on or after its start date.",
      );
    if (schedule.start) body.start_date = schedule.start;
    if (schedule.end) body.end_date = schedule.end;
    const ids = parseIDs(schedule.taskIDs);
    if (ids.length) body.task_ids = ids;
  }
  if (kind !== "decompose") return body;
  if (source.mode === "page") {
    if (
      !source.pageID ||
      !/^\d+$/.test(source.revision) ||
      Number(source.revision) < 1
    )
      throw new Error("Select a document and its saved version.");
    body.source = { page_id: source.pageID, revision: Number(source.revision) };
  } else {
    if (!source.text.trim()) throw new Error("Enter or upload the PRD text.");
    if (new TextEncoder().encode(source.text).length > 50000)
      throw new Error("PRD text must not exceed 50,000 UTF-8 bytes.");
    if (source.key && new TextEncoder().encode(source.key).length > 160)
      throw new Error("The PRD source key must not exceed 160 UTF-8 bytes.");
    body.source = { text: source.text };
    if (source.key?.trim()) body.source.key = source.key.trim();
  }
  return body;
}

export function displayValue(value: unknown): string {
  if (value == null || value === "") return "—";
  if (
    typeof value === "string" ||
    typeof value === "number" ||
    typeof value === "boolean"
  )
    return String(value);
  return JSON.stringify(value, null, 2);
}
