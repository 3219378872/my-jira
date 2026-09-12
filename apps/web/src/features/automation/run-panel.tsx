import {
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
  type FormEvent,
} from "react";
import { observer } from "mobx-react-lite";
import { useSearchParams } from "react-router-dom";
import {
  CalendarClock,
  ChartNoAxesCombined,
  CircleStop,
  FileText,
  GitPullRequest,
  History,
  Play,
  RefreshCw,
  ShieldAlert,
  Sparkles,
  Undo2,
  Upload,
} from "lucide-react";
import {
  Badge,
  Button,
  EmptyState,
  ErrorBox,
  Field,
  Input,
  Loading,
  Modal,
  Select,
} from "../../components/ui";
import { api, errorMessage } from "../../lib/api";
import { useMutation, useRemote } from "../../lib/hooks";
import { appStore } from "../../stores/app-store";
import type { PageDocument } from "../../types";
import type {
  AppliedState,
  AutomationPolicy,
  AutomationRun,
  RunKind,
} from "./contracts";
import {
  availableRunActions,
  displayValue,
  kindLabels,
  statusLabels,
  runRequest,
} from "./presentation";
import { Evidence, Facts, formatTime, StatusBadge } from "./shared";

const capabilities = [
  {
    kind: "decompose",
    icon: FileText,
    zh: "固定 PRD 版本，生成故事、验收条件、任务、依赖与分钟级估算。",
    en: "Pin a PRD revision and derive stories, acceptance criteria, tasks, dependencies, and effort in minutes.",
  },
  {
    kind: "schedule",
    icon: CalendarClock,
    zh: "按技能、工时、日历、容量和依赖安排未锁定工作。",
    en: "Schedule unlocked work using skills, effort, calendars, capacity, and dependencies.",
  },
  {
    kind: "forecast",
    icon: ChartNoAxesCombined,
    zh: "从历史吞吐生成交付日期分布，保留假设与数据不足状态。",
    en: "Forecast delivery from historical throughput with explicit assumptions and data sufficiency.",
  },
  {
    kind: "risk",
    icon: ShieldAlert,
    zh: "识别延期、阻塞、负载和质量风险，追踪应对与解除。",
    en: "Track deadline, dependency, capacity, and quality risks through response and resolution.",
  },
  {
    kind: "quality",
    icon: GitPullRequest,
    zh: "分析授权 GitHub 仓库、文档版本与 Actions 测试证据。",
    en: "Analyze authorized GitHub repositories, document revisions, and Actions test evidence.",
  },
  {
    kind: "efficiency",
    icon: Sparkles,
    zh: "识别流程瓶颈，固定改善基线和观察窗口，回看行动效果。",
    en: "Identify process bottlenecks, record a baseline and observation window, and measure outcomes.",
  },
] as const;

export const RunPanel = observer(function RunPanel({
  base,
  policy,
  canManage,
  canRun,
  onQuality,
  onPolicy,
  refreshKey,
}: {
  base: string;
  policy: Pick<AutomationPolicy, "enabled" | "allowed_kinds"> | null;
  canManage: boolean;
  canRun: boolean;
  onQuality: () => void;
  onPolicy: () => void;
  refreshKey: number;
}) {
  const t = appStore.t;
  const runs = useRemote<AutomationRun[]>(
    `${base}/automation/runs`,
    refreshKey,
  );
  const [kind, setKind] = useState<RunKind | null>(null);
  const [params, setParams] = useSearchParams();
  const selectedID = params.get("run");
  const setSelectedID = (id: string | null) => {
    const next = new URLSearchParams(params);
    if (id) next.set("run", id);
    else next.delete("run");
    setParams(next, { replace: true });
  };
  const [filter, setFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const hasPending = runs.data?.some((run) =>
    ["queued", "running"].includes(run.status),
  );
  useEffect(() => {
    if (!hasPending) return;
    const timer = window.setInterval(runs.refresh, 4000);
    return () => window.clearInterval(timer);
  }, [hasPending, runs.refresh]);
  const visible = (runs.data ?? []).filter(
    (run) =>
      (!filter || run.kind === filter) &&
      (!statusFilter || run.status === statusFilter),
  );
  if ([401, 403, 404].includes(runs.errorStatus ?? 0))
    return <ErrorBox message={runs.error} retry={runs.refresh} />;
  return (
    <div className="automation-stack">
      {policy && !policy.enabled && (
        <div className="automation-notice">
          <span>
            {t(
              "自动应用当前已停用。启用策略后可运行项目自动化；历史结果仍可查看。",
              "Automatic application is disabled. Enable the project policy to run automation; historical results remain available.",
            )}
          </span>
          {canManage && (
            <Button size="sm" onClick={onPolicy}>
              {t("配置策略", "Configure policy")}
            </Button>
          )}
        </div>
      )}
      <div className="automation-capabilities">
        {capabilities.map(({ kind: capability, icon: Icon, zh, en }) => (
          <section className="automation-capability" key={capability}>
            <Icon size={20} />
            <h3>{t(...kindLabels[capability])}</h3>
            <p>{t(zh, en)}</p>
            <Button
              size="sm"
              disabled={
                capability !== "quality" &&
                (!canRun ||
                  !policy?.enabled ||
                  !policy?.allowed_kinds.includes(capability))
              }
              onClick={() =>
                capability === "quality" ? onQuality() : setKind(capability)
              }
            >
              {capability === "quality" ? (
                t("查看与分析", "Review & analyze")
              ) : (
                <>
                  <Play size={12} />
                  {t("开始运行", "Start run")}
                </>
              )}
            </Button>
            {capability !== "quality" &&
              policy?.enabled &&
              !policy.allowed_kinds.includes(capability) && (
                <span className="text-muted">
                  {t("策略未允许此能力", "Not allowed by policy")}
                </span>
              )}
          </section>
        ))}
      </div>
      <section className="automation-card">
        <div className="automation-section-heading">
          <div>
            <h2>{t("运行记录", "Run history")}</h2>
            <p>
              {t(
                "每次运行保留固定输入、来源版本、执行结果与可撤销的变更依据。",
                "Each run retains its input snapshot, source versions, outcome, and applied-change evidence.",
              )}
            </p>
          </div>
          <Button
            size="sm"
            onClick={runs.refresh}
            busy={runs.loading && !!runs.data}
          >
            <RefreshCw size={14} />
            {t("刷新", "Refresh")}
          </Button>
        </div>
        <div className="automation-inline">
          <Field label={t("能力", "Capability")}>
            <Select
              value={filter}
              onChange={(event) => setFilter(event.target.value)}
            >
              <option value="">{t("全部能力", "All capabilities")}</option>
              {Object.entries(kindLabels).map(([value, labels]) => (
                <option key={value} value={value}>
                  {t(...labels)}
                </option>
              ))}
            </Select>
          </Field>
          <Field label={t("状态", "Status")}>
            <Select
              value={statusFilter}
              onChange={(event) => setStatusFilter(event.target.value)}
            >
              <option value="">{t("全部状态", "All statuses")}</option>
              {[
                "queued",
                "running",
                "applied",
                "partial",
                "completed",
                "blocked",
                "failed",
                "cancelled",
                "undone",
              ].map((value) => (
                <option key={value} value={value}>
                  {statusLabels[value] ? t(...statusLabels[value]) : value}
                </option>
              ))}
            </Select>
          </Field>
        </div>
        <ErrorBox message={runs.error} retry={runs.refresh} />
        {runs.loading && !runs.data ? (
          <Loading />
        ) : !visible.length ? (
          <EmptyState
            icon={<History size={24} />}
            title={t("没有符合条件的运行", "No matching runs")}
            description={t(
              "运行一项已授权能力后，可在此追踪排队、执行、部分完成或受阻的原因。",
              "Start an allowed capability to track its queue, execution, partial results, and blockers.",
            )}
          />
        ) : (
          <div className="automation-table-wrap">
            <table className="automation-table">
              <thead>
                <tr>
                  <th>{t("能力与运行", "Capability and run")}</th>
                  <th>{t("状态", "Status")}</th>
                  <th>{t("策略 / 尝试", "Policy / attempts")}</th>
                  <th>{t("时间", "Time")}</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {visible.map((run) => (
                  <tr key={run.id}>
                    <td>
                      <strong>
                        {kindLabels[run.kind]
                          ? t(...kindLabels[run.kind])
                          : run.kind}
                      </strong>
                      <small>{run.id}</small>
                    </td>
                    <td>
                      <StatusBadge status={run.status} />
                    </td>
                    <td>
                      v{run.policy_version} · {run.attempts ?? 0}
                    </td>
                    <td>{formatTime(run.created_at)}</td>
                    <td>
                      <Button size="sm" onClick={() => setSelectedID(run.id)}>
                        {t("查看详情", "View details")}
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
      {kind && (
        <LaunchRun
          key={kind}
          base={base}
          kind={kind}
          onClose={() => setKind(null)}
          onCreated={(run) => {
            setKind(null);
            setSelectedID(run.id);
            runs.refresh();
          }}
        />
      )}
      <RunDetails
        base={base}
        runID={selectedID}
        canManage={canManage}
        onClose={() => setSelectedID(null)}
        onChanged={runs.refresh}
      />
    </div>
  );
});

const LaunchRun = observer(function LaunchRun({
  base,
  kind,
  onClose,
  onCreated,
}: {
  base: string;
  kind: RunKind;
  onClose: () => void;
  onCreated: (run: AutomationRun) => void;
}) {
  const t = appStore.t;
  const [mode, setMode] = useState<"page" | "text">("page");
  const [pageID, setPageID] = useState("");
  const [revision, setRevision] = useState("");
  const [text, setText] = useState("");
  const [sourceKey, setSourceKey] = useState("");
  const [fileName, setFileName] = useState("");
  const [schedule, setSchedule] = useState({ start: "", end: "", taskIDs: "" });
  const idempotencyKey = useRef(crypto.randomUUID());
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const mutation = useMutation();
  const pages = useRemote<PageDocument[]>(
    kind === "decompose" ? `${base}/pages` : null,
  );
  const versions = useRemote<
    { id: string; version: number; name: string; created_at: string }[]
  >(kind === "decompose" && pageID ? `${base}/pages/${pageID}/versions` : null);
  const selectedPage = pages.data?.find((page) => page.id === pageID);
  const revisions = [
    ...new Set([
      ...(selectedPage ? [selectedPage.version] : []),
      ...(versions.data ?? []).map((version) => version.version),
    ]),
  ].sort((left, right) => right - left);
  const edited = () => {
    idempotencyKey.current = crypto.randomUUID();
    mutation.setError("");
  };
  const upload = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;
    try {
      if (!/\.(md|markdown|txt)$/i.test(file.name))
        throw new Error(
          t(
            "请选择 Markdown 或纯文本文件。",
            "Choose a Markdown or plain text file.",
          ),
        );
      if (file.size > 1024 * 1024)
        throw new Error(
          t("文件不能超过 1 MB。", "The file must not exceed 1 MB."),
        );
      const value = await file.text();
      if (new TextEncoder().encode(value).length > 50000)
        throw new Error(
          t(
            "PRD 的 UTF-8 文本不能超过 50,000 字节。",
            "PRD text must not exceed 50,000 UTF-8 bytes.",
          ),
        );
      edited();
      setText(value);
      setFileName(file.name);
      if (!sourceKey) setSourceKey(file.name);
    } catch (error) {
      mutation.setError(errorMessage(error));
    }
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    mutation.execute(async () => {
      const response = await api.post<AutomationRun>(
        `${base}/automation/runs`,
        runRequest(
          kind,
          { mode, pageID, revision, text, key: sourceKey },
          idempotencyKey.current,
          schedule,
        ),
      );
      if (mounted.current) onCreated(response.data);
    });
  };
  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (!open && !mutation.busy) onClose();
      }}
      title={t(...kindLabels[kind])}
      description={t(
        "固定当前输入和授权，运行后按项目策略自动应用有效变更。",
        "Capture the current input and authorization, then apply valid changes under the project policy.",
      )}
    >
      <form className="automation-stack" onSubmit={submit}>
        <ErrorBox message={mutation.error} />
        {kind === "decompose" ? (
          <>
            <Field label={t("需求来源", "Requirement source")}>
              <Select
                value={mode}
                onChange={(event) => {
                  setMode(event.target.value as "page" | "text");
                  edited();
                }}
              >
                <option value="page">
                  {t("项目文档的固定版本", "Saved project document revision")}
                </option>
                <option value="text">
                  {t("上传或粘贴 PRD", "Upload or paste a PRD")}
                </option>
              </Select>
            </Field>
            {mode === "page" ? (
              <>
                <ErrorBox
                  message={pages.error || versions.error}
                  retry={() => {
                    pages.refresh();
                    versions.refresh();
                  }}
                />
                <Field label={t("PRD 文档", "PRD document")}>
                  <Select
                    required
                    value={pageID}
                    disabled={pages.loading}
                    onChange={(event) => {
                      const id = event.target.value;
                      setPageID(id);
                      setRevision(
                        String(
                          pages.data?.find((page) => page.id === id)?.version ??
                            "",
                        ),
                      );
                      edited();
                    }}
                  >
                    <option value="">
                      {t("选择文档", "Choose a document")}
                    </option>
                    {(pages.data ?? []).map((page) => (
                      <option key={page.id} value={page.id}>
                        {page.name}
                        {page.is_private ? t("（私有）", " (private)") : ""}
                      </option>
                    ))}
                  </Select>
                </Field>
                <Field
                  label={t("来源版本", "Source revision")}
                  hint={t(
                    "后续文档修改不会改变本次运行的固定输入。",
                    "Later edits will not change this run's pinned input.",
                  )}
                >
                  <Select
                    required
                    value={revision}
                    disabled={!pageID || versions.loading}
                    onChange={(event) => {
                      setRevision(event.target.value);
                      edited();
                    }}
                  >
                    <option value="">
                      {t("选择已保存版本", "Choose a saved revision")}
                    </option>
                    {revisions.map((value) => (
                      <option key={value} value={value}>
                        v{value}
                        {value === selectedPage?.version
                          ? t(" · 当前", " · current")
                          : ""}
                      </option>
                    ))}
                  </Select>
                </Field>
              </>
            ) : (
              <>
                <Field
                  label={t("上传 Markdown / 文本", "Upload Markdown / text")}
                  hint={t(
                    "最多 50,000 UTF-8 字节。文件将作为运行输入保存。",
                    "Up to 50,000 UTF-8 bytes. The text is retained in the run input.",
                  )}
                >
                  <Input
                    type="file"
                    accept=".md,.markdown,.txt,text/plain,text/markdown"
                    onChange={upload}
                  />
                </Field>
                {fileName && (
                  <span className="automation-inline text-muted">
                    <Upload size={13} />
                    {fileName}
                  </span>
                )}
                <Field label={t("PRD 文本", "PRD text")}>
                  <textarea
                    className="input automation-textarea"
                    rows={9}
                    maxLength={50000}
                    required
                    value={text}
                    onChange={(event) => {
                      setText(event.target.value);
                      edited();
                    }}
                  />
                </Field>
                <Field
                  label={t("来源标识", "Source key")}
                  hint={t(
                    "后续修订使用相同标识，以便生成增量差异。",
                    "Reuse this key for later revisions to generate incremental changes.",
                  )}
                >
                  <Input
                    value={sourceKey}
                    required
                    maxLength={160}
                    onChange={(event) => {
                      setSourceKey(event.target.value);
                      edited();
                    }}
                    placeholder={t(
                      "例如：客户门户 PRD",
                      "e.g. customer-portal-prd",
                    )}
                  />
                </Field>
              </>
            )}
          </>
        ) : (
          <p className="automation-notice">
            {kind === "schedule"
              ? t(
                  "运行将检查完整授权工作集合的工时、依赖、技能、日历和可用容量。无法满足约束的工作会记录明确原因。",
                  "The run checks effort, dependencies, skills, calendars, and available capacity across the authorized work scope. Unschedulable work retains a clear reason.",
                )
              : kind === "forecast"
                ? t(
                    "少于 20 个完成样本或不足四周历史时，将显示数据不足。预测保留 P50/P80、假设与回测依据。",
                    "Fewer than 20 completed samples or four weeks of history produces an insufficient-history result. Forecasts retain P50/P80, assumptions, and backtest evidence.",
                  )
                : kind === "risk"
                  ? t(
                      "当前预测、依赖、负载和质量证据将用于更新风险及站内通知。同原因风险保留持续记录。",
                      "Current forecasts, dependencies, workload, and quality evidence update risks and inbox notifications. Repeated causes retain one continuing record.",
                    )
                  : t(
                      "分析团队流程瓶颈并固定基线与观察窗口；后续观测独立记录，数据不足时保留证据不足状态。",
                      "Analyze team process bottlenecks and pin a baseline and observation window. Later observations are recorded separately, including insufficient evidence.",
                    )}
          </p>
        )}
        {kind === "schedule" && (
          <section className="automation-stack">
            <div className="automation-form-grid">
              <Field
                label={t(
                  "排期窗口开始（可选）",
                  "Scheduling window starts (optional)",
                )}
              >
                <Input
                  type="date"
                  value={schedule.start}
                  onChange={(event) => {
                    setSchedule({ ...schedule, start: event.target.value });
                    edited();
                  }}
                />
              </Field>
              <Field
                label={t(
                  "排期窗口结束（可选）",
                  "Scheduling window ends (optional)",
                )}
              >
                <Input
                  type="date"
                  min={schedule.start || undefined}
                  value={schedule.end}
                  onChange={(event) => {
                    setSchedule({ ...schedule, end: event.target.value });
                    edited();
                  }}
                />
              </Field>
            </div>
            <Field
              label={t(
                "选定工作项 ID（可选）",
                "Selected work item IDs (optional)",
              )}
              hint={t(
                "留空处理策略范围中的可排期工作。每行一个 ID 或逗号分隔。",
                "Leave empty for schedulable work in policy scope. Use one ID per line or comma-separated IDs.",
              )}
            >
              <textarea
                className="input automation-textarea"
                rows={3}
                value={schedule.taskIDs}
                onChange={(event) => {
                  setSchedule({ ...schedule, taskIDs: event.target.value });
                  edited();
                }}
              />
            </Field>
          </section>
        )}
        <div className="automation-form-actions">
          <Button type="button" onClick={onClose} disabled={mutation.busy}>
            {t("取消", "Cancel")}
          </Button>
          <Button type="submit" variant="primary" busy={mutation.busy}>
            <Play size={14} />
            {t("提交运行", "Submit run")}
          </Button>
        </div>
      </form>
    </Modal>
  );
});

const RunDetails = observer(function RunDetails({
  base,
  runID,
  canManage,
  onClose,
  onChanged,
}: {
  base: string;
  runID: string | null;
  canManage: boolean;
  onClose: () => void;
  onChanged: () => void;
}) {
  const t = appStore.t;
  const detail = useRemote<AutomationRun>(
    runID ? `${base}/automation/runs/${runID}` : null,
  );
  const mutation = useMutation();
  const [undoOpen, setUndoOpen] = useState(false);
  const run = detail.data;
  useEffect(() => {
    setUndoOpen(false);
    mutation.setError("");
  }, [runID]);
  useEffect(() => {
    if (!run || !["queued", "running"].includes(run.status)) return;
    const timer = window.setInterval(detail.refresh, 3000);
    return () => window.clearInterval(timer);
  }, [run?.status, detail.refresh]);
  const action = (name: "cancel" | "retry" | "undo") =>
    mutation.execute(async () => {
      try {
        await api.post(`${base}/automation/runs/${runID}/${name}`);
        setUndoOpen(false);
      } finally {
        detail.refresh();
        onChanged();
      }
    });
  const actions = run
    ? availableRunActions(run)
    : { cancel: false, retry: false, undo: false };
  return (
    <Modal
      open={!!runID}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      title={t("运行详情", "Run details")}
      className="automation-run-modal"
    >
      <ErrorBox
        message={detail.error || mutation.error}
        retry={detail.error ? detail.refresh : undefined}
      />
      {detail.loading && !run ? (
        <Loading />
      ) : (
        run &&
        ![401, 403, 404].includes(detail.errorStatus ?? 0) && (
          <div className="automation-stack">
            <div className="automation-section-heading">
              <div>
                <h2>
                  {kindLabels[run.kind] ? t(...kindLabels[run.kind]) : run.kind}
                </h2>
                <small className="text-muted">{run.id}</small>
              </div>
              <StatusBadge status={run.status} />
            </div>
            {run.failure ? (
              <div className="automation-failure" role="status">
                <strong>{t("失败或受阻原因", "Failure or blocker")}</strong>
                <pre>{displayValue(run.failure)}</pre>
              </div>
            ) : null}
            <Facts
              entries={[
                [t("创建时间", "Created"), formatTime(run.created_at)],
                [t("更新时间", "Updated"), formatTime(run.updated_at)],
                [t("策略版本", "Policy version"), run.policy_version],
                [
                  t("算法 / 模型版本", "Algorithm / model version"),
                  run.algorithm_version,
                ],
                [t("尝试次数", "Attempts"), run.attempts],
                [t("原因链", "Cause"), run.cause],
                [t("自动调整轮次", "Adjustment round"), run.round],
                [t("变更批次", "Change batch"), run.batch_id],
              ]}
            />
            <section>
              <h3>{t("固定输入与来源", "Pinned input and sources")}</h3>
              <Facts
                entries={[
                  [t("固定时间", "As of"), run.input?.as_of],
                  [
                    t("项目修订", "Project revision"),
                    run.input?.project_revision,
                  ],
                  [t("输入指纹", "Input fingerprint"), run.input_fingerprint],
                ]}
              />
              <Evidence
                title={t("来源版本与原文", "Source revision and text")}
                value={run.input?.source}
                open
              />
              <Evidence
                title={t("完整输入快照", "Complete input snapshot")}
                value={run.input}
              />
            </section>
            <section>
              <h3>{t("结果与变更", "Results and changes")}</h3>
              {run.output?.summary != null && (
                <p>{displayValue(run.output.summary)}</p>
              )}
              <AppliedChanges
                before={run.before_state}
                after={run.after_state}
              />
              {!run.after_state?.length && (
                <Evidence
                  title={t(
                    "拟议业务命令（尚无应用差异）",
                    "Proposed commands (no applied diff recorded)",
                  )}
                  value={run.output?.commands}
                  open
                />
              )}
              <Evidence
                title={t(
                  "发现、约束与未解决问题",
                  "Findings, constraints, and unresolved questions",
                )}
                value={run.output?.findings}
                open
              />
              <Evidence
                title={t("分析结果与证据", "Analysis results and evidence")}
                value={run.output?.results}
                open
              />
              <Evidence
                title={t("完整输出", "Complete output")}
                value={run.output}
              />
            </section>
            <Evidence
              title={t(
                "撤销冲突（保留后续修改）",
                "Undo conflicts (later changes are retained)",
              )}
              value={run.undo_conflicts}
              open
            />
            {canManage && (
              <div className="automation-form-actions">
                <Button
                  size="sm"
                  onClick={detail.refresh}
                  disabled={mutation.busy}
                >
                  <RefreshCw size={14} />
                  {t("刷新", "Refresh")}
                </Button>
                {actions.cancel && (
                  <Button
                    size="sm"
                    onClick={() => action("cancel")}
                    busy={mutation.busy}
                  >
                    <CircleStop size={14} />
                    {t("取消运行", "Cancel run")}
                  </Button>
                )}
                {actions.retry && (
                  <Button
                    size="sm"
                    onClick={() => action("retry")}
                    busy={mutation.busy}
                  >
                    <RefreshCw size={14} />
                    {t("按固定输入重试", "Retry pinned input")}
                  </Button>
                )}
                {actions.undo && (
                  <Button
                    size="sm"
                    onClick={() => setUndoOpen(true)}
                    disabled={mutation.busy}
                  >
                    <Undo2 size={14} />
                    {t("撤销变更批次", "Undo change batch")}
                  </Button>
                )}
              </div>
            )}
            {actions.retry && (
              <p className="text-muted">
                {t(
                  "重试使用原始快照；输入已变化时会返回冲突。重新发起运行会固定最新输入。",
                  "Retry uses the original snapshot and may conflict with newer edits. A new run captures the latest input.",
                )}
              </p>
            )}
            {undoOpen && (
              <div className="automation-undo">
                <strong>{t("受保护撤销", "Guarded undo")}</strong>
                <p>
                  {t(
                    "服务器会检查批次应用后的编辑、评论和关联变化。存在冲突时保留后续内容并返回具体原因。",
                    "The server checks edits, comments, and relationship changes made after the batch. Conflicts preserve later content and return specific reasons.",
                  )}
                </p>
                <div className="automation-inline">
                  <Button
                    onClick={() => setUndoOpen(false)}
                    disabled={mutation.busy}
                  >
                    {t("保留变更", "Keep changes")}
                  </Button>
                  <Button
                    variant="danger"
                    onClick={() => action("undo")}
                    busy={mutation.busy}
                  >
                    {t("检查并撤销", "Check and undo")}
                  </Button>
                </div>
              </div>
            )}
          </div>
        )
      )}
    </Modal>
  );
});

function AppliedChanges({
  before = [],
  after = [],
}: {
  before?: AppliedState[];
  after?: AppliedState[];
}) {
  const t = appStore.t;
  if (!after.length) return null;
  const previous = new Map(before.map((state) => [state.item_id, state]));
  return (
    <div className="automation-table-wrap">
      <table className="automation-table automation-diff">
        <thead>
          <tr>
            <th>{t("对象 / 操作", "Object / operation")}</th>
            <th>{t("变更前", "Before")}</th>
            <th>{t("变更后 / 命令", "After / command")}</th>
          </tr>
        </thead>
        <tbody>
          {after.map((state) => {
            const earlier = previous.get(state.item_id);
            return (
              <tr key={state.item_id}>
                <td>
                  <Badge>
                    {state.created ? t("新建", "Create") : t("更新", "Update")}
                  </Badge>
                  <small>{state.item_id}</small>
                  <small>
                    {earlier
                      ? `v${earlier.version} → v${state.version}`
                      : `v${state.version}`}
                  </small>
                </td>
                <td>
                  <pre>
                    {state.created
                      ? t(
                          "新建对象，无先前版本",
                          "New object; no previous revision",
                        )
                      : earlier
                        ? displayValue(earlier.fields)
                        : t(
                            "未提供变更前证据",
                            "Before-state evidence unavailable",
                          )}
                  </pre>
                </td>
                <td>
                  <pre>{displayValue(state.fields)}</pre>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
