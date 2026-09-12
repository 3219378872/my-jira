import { useEffect, useState, type FormEvent } from "react";
import { observer } from "mobx-react-lite";
import { Link } from "react-router-dom";
import {
  ExternalLink,
  FileCode2,
  FileSearch,
  GitBranch,
  GitPullRequest,
  Link2,
  RefreshCw,
  ShieldOff,
  TestTubeDiagonal,
} from "lucide-react";
import {
  Button,
  EmptyState,
  ErrorBox,
  Field,
  Input,
  Loading,
  Modal,
  Select,
} from "../../components/ui";
import { api } from "../../lib/api";
import { useMutation, useRemote } from "../../lib/hooks";
import { appStore } from "../../stores/app-store";
import type {
  GitHubBinding,
  GitHubConnection,
  QualityReport,
} from "./contracts";
import { displayValue, parsePinnedSource, qualityStatus } from "./presentation";
import { Evidence, Facts, formatTime, StatusBadge } from "./shared";

export const QualityPanel = observer(function QualityPanel({
  base,
  route,
  canManage,
  refreshKey,
}: {
  base: string;
  route: string;
  canManage: boolean;
  refreshKey: number;
}) {
  const t = appStore.t;
  const connection = useRemote<GitHubConnection>(`${base}/github`, refreshKey);
  const reports = useRemote<QualityReport[]>(
    `${base}/quality/reports`,
    refreshKey,
  );
  const mutation = useMutation();
  const [binding, setBinding] = useState("");
  const [selectedGrant, setSelectedGrant] = useState("");
  const [repository, setRepository] = useState("");
  const [selectedReportID, setSelectedReportID] = useState<string | null>(null);
  const [syncing, setSyncing] = useState<GitHubBinding | null>(null);
  const [disconnecting, setDisconnecting] = useState<GitHubBinding | null>(
    null,
  );
  const [queuedID, setQueuedID] = useState("");
  const [queuedAt, setQueuedAt] = useState(0);
  const [queuedBindingID, setQueuedBindingID] = useState("");
  const grant = connection.data?.grants?.find(
    (item) => String(item.installation_id) === selectedGrant,
  );
  const grants = (connection.data?.grants ?? []).filter(
    (item) => new Date(item.expires_at).getTime() > Date.now(),
  );
  const pending =
    !!queuedAt ||
    connection.data?.bindings?.some((item) =>
      ["queued", "running", "syncing", "retrying"].includes(item.sync_status),
    );
  const refresh = () => {
    connection.refresh();
    reports.refresh();
  };
  useEffect(() => {
    if (!pending) return;
    const timer = window.setInterval(() => {
      connection.refresh();
      reports.refresh();
    }, 5000);
    return () => window.clearInterval(timer);
  }, [pending, connection.refresh, reports.refresh]);
  useEffect(() => {
    if (
      queuedAt &&
      (reports.data?.some(
        (report) =>
          report.binding_id === queuedBindingID &&
          new Date(report.created_at).getTime() >= queuedAt,
      ) ||
        connection.data?.bindings?.some(
          (item) =>
            item.id === queuedBindingID &&
            (["failed", "revoked", "disabled"].includes(item.sync_status) ||
              (item.sync_status === "synced" &&
                new Date(item.last_synced_at ?? "").getTime() >= queuedAt)),
        ))
    )
      setQueuedAt(0);
  }, [queuedAt, queuedBindingID, reports.data, connection.data]);
  const connect = () =>
    mutation.execute(async () => {
      const response = await api.post<{
        install_url: string;
        expires_at: string;
      }>(`${base}/github/connect`);
      const target = new URL(response.data.install_url);
      if (target.protocol !== "https:" || target.hostname !== "github.com")
        throw new Error(
          t(
            "GitHub 授权地址无效。",
            "The GitHub authorization URL is invalid.",
          ),
        );
      window.location.assign(target.href);
    });
  const bind = (event: FormEvent) => {
    event.preventDefault();
    mutation.execute(
      async () => {
        await api.post(`${base}/github/bindings`, {
          installation_id: Number(selectedGrant),
          repository,
        });
        setSelectedGrant("");
        setRepository("");
        refresh();
      },
      t("GitHub 仓库已绑定", "GitHub repository bound"),
    );
  };
  const visibleReports = (reports.data ?? []).filter(
    (report) => !binding || report.binding_id === binding,
  );
  if (
    [connection.errorStatus, reports.errorStatus].some((status) =>
      [401, 403, 404].includes(status ?? 0),
    )
  )
    return (
      <ErrorBox message={connection.error || reports.error} retry={refresh} />
    );
  return (
    <div className="automation-stack">
      <div className="automation-section-heading">
        <div>
          <h2>
            {t("代码、文档与测试质量", "Code, documentation, and test quality")}
          </h2>
          <p>
            {t(
              "报告固定到仓库、提交、PR、Actions 运行和文档修订。失败、跳过、缺失与未知分别显示。",
              "Reports are pinned to repository, commit, PR, Actions run, and document revisions. Failed, skipped, missing, and unknown evidence remain distinct.",
            )}
          </p>
        </div>
        <Button size="sm" onClick={refresh}>
          <RefreshCw size={14} />
          {t("刷新", "Refresh")}
        </Button>
      </div>
      <ErrorBox
        message={connection.error || reports.error || mutation.error}
        retry={connection.error || reports.error ? refresh : undefined}
      />
      {connection.loading && !connection.data ? (
        <Loading />
      ) : (
        connection.data && (
          <section className="automation-card">
            <div className="automation-section-heading">
              <div className="automation-inline">
                <GitBranch size={18} />
                <h3>{t("GitHub 仓库连接", "GitHub repository connections")}</h3>
              </div>
              {canManage && (
                <Button
                  size="sm"
                  onClick={connect}
                  busy={mutation.busy}
                  disabled={!connection.data.configured}
                >
                  <Link2 size={14} />
                  {t("连接 GitHub App", "Connect GitHub App")}
                </Button>
              )}
            </div>
            {!connection.data.configured && (
              <p className="automation-notice">
                {t(
                  "实例尚未配置 GitHub App。管理员完成实例配置后，项目管理员可授权指定仓库。",
                  "The instance GitHub App is not configured. Once configured, a project administrator can authorize selected repositories.",
                )}
              </p>
            )}
            {!canManage && (
              <p className="text-muted">
                {t(
                  "成员可查看质量证据。连接、同步与撤销由项目管理员管理。",
                  "Members can review quality evidence. Project administrators manage connections, synchronization, and revocation.",
                )}
              </p>
            )}
            <p className="text-muted">
              {t(
                "仅分析已授权源码和已有 Actions 产物；平台不执行仓库代码。",
                "Analyze authorized source and existing Actions artifacts. Repository code is not executed by this platform.",
              )}
            </p>
            {connection.data.required_permissions && (
              <Facts
                entries={Object.entries(
                  connection.data.required_permissions,
                ).map(([permission, access]) => [permission, access])}
              />
            )}
            {canManage && !!grants.length && (
              <form className="automation-binding-form" onSubmit={bind}>
                <h4>
                  {t(
                    "选择已授权安装中的仓库",
                    "Choose a repository from an authorized installation",
                  )}
                </h4>
                <div className="automation-form-grid">
                  <Field
                    label={t("GitHub App 安装", "GitHub App installation")}
                  >
                    <Select
                      required
                      value={selectedGrant}
                      onChange={(event) => {
                        setSelectedGrant(event.target.value);
                        setRepository("");
                      }}
                    >
                      <option value="">
                        {t("选择安装", "Choose installation")}
                      </option>
                      {grants.map((item) => (
                        <option
                          key={item.installation_id}
                          value={item.installation_id}
                        >
                          #{item.installation_id} ·{" "}
                          {t("授权到期", "Grant expires")}{" "}
                          {formatTime(item.expires_at)}
                        </option>
                      ))}
                    </Select>
                  </Field>
                  <Field label={t("仓库", "Repository")}>
                    <Select
                      required
                      disabled={!grant}
                      value={repository}
                      onChange={(event) => setRepository(event.target.value)}
                    >
                      <option value="">
                        {t("选择仓库", "Choose repository")}
                      </option>
                      {grant?.repositories?.map((item) => (
                        <option key={item.id} value={item.full_name}>
                          {item.full_name}
                        </option>
                      ))}
                    </Select>
                  </Field>
                </div>
                <Button
                  type="submit"
                  size="sm"
                  variant="primary"
                  busy={mutation.busy}
                  disabled={!repository || !selectedGrant}
                >
                  {t("绑定到此项目", "Bind to this project")}
                </Button>
              </form>
            )}
            {!connection.data.bindings?.length ? (
              <p className="automation-empty-line">
                {t(
                  "尚未绑定仓库。授权后可选择本项目要分析的仓库。",
                  "No repository is bound. Authorize GitHub, then select the repositories to analyze for this project.",
                )}
              </p>
            ) : (
              <div className="automation-bindings">
                {connection.data.bindings.map((item) => (
                  <article key={item.id} className="automation-binding">
                    <div className="automation-section-heading">
                      <div>
                        <strong>{item.repository}</strong>
                        <p className="text-muted">
                          {t("安装", "Installation")} #{item.installation_id}
                        </p>
                      </div>
                      <StatusBadge
                        status={
                          item.active ? item.sync_status || "active" : "revoked"
                        }
                      />
                    </div>
                    <Facts
                      entries={[
                        [
                          t("最近同步", "Last sync"),
                          formatTime(item.last_synced_at),
                        ],
                        [
                          t("下次补偿同步", "Next retry"),
                          formatTime(item.next_retry_at),
                        ],
                      ]}
                    />
                    {item.last_error && (
                      <div className="automation-failure">
                        {item.last_error}
                      </div>
                    )}
                    {!item.active && (
                      <p className="text-muted">
                        {t(
                          "连接已停用，后续数据读取与自动动作已停止。重新授权后才能同步。",
                          "The connection is disabled. Further data reads and automatic actions are stopped until reauthorized.",
                        )}
                      </p>
                    )}
                    {canManage && item.active && (
                      <div className="automation-inline">
                        <Button
                          size="sm"
                          onClick={() => setSyncing(item)}
                          disabled={mutation.busy}
                        >
                          <RefreshCw size={13} />
                          {t("同步并分析", "Sync and analyze")}
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => setDisconnecting(item)}
                          disabled={mutation.busy}
                        >
                          <ShieldOff size={13} />
                          {t("停止连接", "Disconnect")}
                        </Button>
                      </div>
                    )}
                  </article>
                ))}
              </div>
            )}
          </section>
        )
      )}
      {queuedID && (
        <div className="automation-notice" role="status">
          <span>
            {t("已提交分析任务", "Analysis request submitted")}: {queuedID}.{" "}
            {t(
              "同步状态和报告将在任务完成后更新。",
              "Sync status and reports update when processing completes.",
            )}
          </span>
          <Button size="sm" variant="ghost" onClick={() => setQueuedID("")}>
            {t("收起", "Dismiss")}
          </Button>
        </div>
      )}
      <section className="automation-card">
        <div className="automation-section-heading">
          <h3>
            {t("固定来源的质量报告", "Quality reports with pinned sources")}
          </h3>
          <Field label={t("筛选仓库", "Filter repository")}>
            <Select
              value={binding}
              onChange={(event) => setBinding(event.target.value)}
            >
              <option value="">{t("全部仓库", "All repositories")}</option>
              {connection.data?.bindings?.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.repository}
                </option>
              ))}
            </Select>
          </Field>
        </div>
        {reports.loading && !reports.data ? (
          <Loading />
        ) : !visibleReports.length ? (
          <EmptyState
            icon={<FileSearch size={24} />}
            title={t("尚无符合条件的质量报告", "No matching quality reports")}
            description={t(
              "绑定仓库并同步已有提交与 Actions 产物后，报告会显示三个维度的证据。缺少产物不会显示为通过。",
              "Bind a repository and synchronize commits and Actions artifacts to produce evidence for all three dimensions. Missing artifacts will not appear as a pass.",
            )}
          />
        ) : (
          <div className="automation-table-wrap">
            <table className="automation-table">
              <thead>
                <tr>
                  <th>{t("仓库 / 提交", "Repository / commit")}</th>
                  <th>{t("代码", "Code")}</th>
                  <th>{t("文档", "Documentation")}</th>
                  <th>{t("测试", "Tests")}</th>
                  <th>{t("时间", "Time")}</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {visibleReports.map((report) => (
                  <tr key={report.id}>
                    <td>
                      <strong>{report.repository}</strong>
                      <small>
                        {report.commit_sha?.slice(0, 12) ||
                          t("提交未知", "Unknown commit")}
                        {report.pull_request
                          ? ` · PR #${report.pull_request}`
                          : ""}
                      </small>
                    </td>
                    <td>
                      <StatusBadge
                        status={qualityStatus(report.dimensions?.code?.status)}
                      />
                    </td>
                    <td>
                      <StatusBadge
                        status={qualityStatus(
                          report.dimensions?.documentation?.status,
                        )}
                      />
                    </td>
                    <td>
                      <StatusBadge
                        status={qualityStatus(report.dimensions?.tests?.status)}
                      />
                    </td>
                    <td>{formatTime(report.created_at)}</td>
                    <td>
                      <Button
                        size="sm"
                        onClick={() => setSelectedReportID(report.id)}
                      >
                        {t("证据详情", "Review evidence")}
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
      {syncing && (
        <SyncRepository
          base={base}
          binding={syncing}
          onClose={() => setSyncing(null)}
          onQueued={(id) => {
            setQueuedBindingID(syncing.id);
            setSyncing(null);
            setQueuedID(id);
            setQueuedAt(Date.now() - 1000);
            refresh();
          }}
        />
      )}
      <Modal
        open={!!disconnecting}
        onOpenChange={(open) => {
          if (!open && !mutation.busy) setDisconnecting(null);
        }}
        title={t("停止仓库连接", "Disconnect repository")}
        description={t(
          `停止读取 ${disconnecting?.repository ?? ""}，并停止该连接的后续自动动作。已有报告保留其来源记录。`,
          `Stop reading ${disconnecting?.repository ?? ""} and stop further automatic actions for this connection. Existing reports retain their source records.`,
        )}
      >
        <ErrorBox message={mutation.error} />
        <div className="automation-form-actions">
          <Button
            disabled={mutation.busy}
            onClick={() => setDisconnecting(null)}
          >
            {t("取消", "Cancel")}
          </Button>
          <Button
            variant="danger"
            busy={mutation.busy}
            onClick={() =>
              mutation.execute(async () => {
                await api.delete(
                  `${base}/github/bindings/${disconnecting!.id}`,
                );
                setDisconnecting(null);
                refresh();
              })
            }
          >
            {t("停止连接", "Disconnect")}
          </Button>
        </div>
      </Modal>
      <QualityDetails
        base={base}
        route={route}
        reportID={selectedReportID}
        onClose={() => setSelectedReportID(null)}
      />
    </div>
  );
});

const SyncRepository = observer(function SyncRepository({
  base,
  binding,
  onClose,
  onQueued,
}: {
  base: string;
  binding: GitHubBinding;
  onClose: () => void;
  onQueued: (id: string) => void;
}) {
  const t = appStore.t;
  const [form, setForm] = useState({
    commit: "",
    pullRequest: "",
    run: "",
    attempt: "",
  });
  const mutation = useMutation();
  const submit = (event: FormEvent) => {
    event.preventDefault();
    mutation.execute(async () => {
      const response = await api.post<{ id: string; status: string }>(
        `${base}/github/bindings/${binding.id}/sync`,
        parsePinnedSource(form),
      );
      onQueued(response.data.id);
    });
  };
  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (!open && !mutation.busy) onClose();
      }}
      title={t("同步并分析仓库", "Sync and analyze repository")}
      description={binding.repository}
    >
      <form className="automation-stack" onSubmit={submit}>
        <ErrorBox message={mutation.error} />
        <p className="automation-notice">
          {t(
            "留空将同步最近完成的 Actions 运行；没有运行时使用默认分支提交。生成的报告会保存解析后的准确来源。",
            "Leave references empty to use the latest completed Actions run, or the default branch commit when no run exists. The report retains the resolved source references.",
          )}
        </p>
        <Field
          label={t("固定 Commit SHA（可选）", "Pin commit SHA (optional)")}
        >
          <Input
            value={form.commit}
            placeholder={t(
              "完整 40 或 64 位 SHA",
              "Full 40- or 64-character SHA",
            )}
            onChange={(event) =>
              setForm({ ...form, commit: event.target.value })
            }
          />
        </Field>
        <div className="automation-form-grid">
          <Field label={t("Pull Request 编号", "Pull request number")}>
            <Input
              type="number"
              min={1}
              step={1}
              value={form.pullRequest}
              onChange={(event) =>
                setForm({ ...form, pullRequest: event.target.value })
              }
            />
          </Field>
          <Field label={t("Actions run ID", "Actions run ID")}>
            <Input
              type="number"
              min={1}
              step={1}
              value={form.run}
              onChange={(event) =>
                setForm({ ...form, run: event.target.value })
              }
            />
          </Field>
          <Field label={t("运行尝试次数", "Run attempt")}>
            <Input
              type="number"
              min={1}
              step={1}
              value={form.attempt}
              onChange={(event) =>
                setForm({ ...form, attempt: event.target.value })
              }
            />
          </Field>
        </div>
        <div className="automation-form-actions">
          <Button type="button" onClick={onClose} disabled={mutation.busy}>
            {t("取消", "Cancel")}
          </Button>
          <Button type="submit" variant="primary" busy={mutation.busy}>
            <RefreshCw size={14} />
            {t("排队分析", "Queue analysis")}
          </Button>
        </div>
      </form>
    </Modal>
  );
});

const QualityDetails = observer(function QualityDetails({
  base,
  route,
  reportID,
  onClose,
}: {
  base: string;
  route: string;
  reportID: string | null;
  onClose: () => void;
}) {
  const t = appStore.t;
  const detail = useRemote<QualityReport>(
    reportID ? `${base}/quality/reports/${reportID}` : null,
  );
  const report = detail.data;
  const dimensions = [
    { key: "code", zh: "代码质量", en: "Code quality", icon: FileCode2 },
    {
      key: "documentation",
      zh: "文档质量",
      en: "Documentation quality",
      icon: FileSearch,
    },
    {
      key: "tests",
      zh: "测试质量",
      en: "Test quality",
      icon: TestTubeDiagonal,
    },
  ] as const;
  const repairActions =
    report && Array.isArray(report.actions)
      ? report.actions.filter(
          (
            entry,
          ): entry is {
            finding_id: string;
            run_id: string;
            item_ids: string[];
            status: string;
            updated_at: string;
          } => !!entry && typeof entry === "object" && "status" in entry,
        )
      : [];
  return (
    <Modal
      open={!!reportID}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      title={t("质量报告与来源证据", "Quality report and source evidence")}
      className="automation-run-modal"
    >
      <ErrorBox message={detail.error} retry={detail.refresh} />
      {detail.loading && !report ? (
        <Loading />
      ) : (
        report &&
        ![401, 403, 404].includes(detail.errorStatus ?? 0) && (
          <div className="automation-stack">
            <div className="automation-inline">
              <GitPullRequest size={19} />
              <h2>{report.repository}</h2>
            </div>
            <Facts
              entries={[
                [t("Commit SHA", "Commit SHA"), report.commit_sha],
                [t("Pull Request", "Pull request"), report.pull_request],
                [t("Actions 运行", "Actions run"), report.run_id],
                [t("运行尝试", "Run attempt"), report.run_attempt],
                [t("分析时间", "Analyzed"), formatTime(report.created_at)],
                [t("来源指纹", "Source fingerprint"), report.source_revision],
              ]}
            />
            <Evidence
              title={t(
                "文档与需求来源修订",
                "Document and requirement source revisions",
              )}
              value={
                report.evidence &&
                typeof report.evidence === "object" &&
                "documents" in report.evidence
                  ? report.evidence.documents
                  : null
              }
              open
            />
            {dimensions.map(({ key, zh, en, icon: Icon }) => {
              const dimension = report.dimensions?.[key];
              return (
                <section className="automation-card" key={key}>
                  <div className="automation-section-heading">
                    <div className="automation-inline">
                      <Icon size={17} />
                      <h3>{t(zh, en)}</h3>
                    </div>
                    <StatusBadge status={qualityStatus(dimension?.status)} />
                  </div>
                  {!dimension && (
                    <p className="text-muted">
                      {t(
                        "该维度没有可用证据。",
                        "No evidence is available for this dimension.",
                      )}
                    </p>
                  )}
                  <Findings
                    value={
                      Array.isArray(report.findings)
                        ? report.findings.filter(
                            (finding: unknown) =>
                              !!finding &&
                              typeof finding === "object" &&
                              "dimension" in finding &&
                              finding.dimension === key,
                          )
                        : dimension?.findings
                    }
                    report={report}
                  />
                  <Evidence
                    title={t("摘要与度量", "Summary and metrics")}
                    value={dimension?.summary}
                    open
                  />
                  <Evidence
                    title={t("完整维度证据", "Complete dimension evidence")}
                    value={dimension}
                  />
                </section>
              );
            })}
            {!!repairActions.length && (
              <section className="automation-card">
                <h3>{t("策略内修复行动", "Repair actions under policy")}</h3>
                {repairActions.map((action, index) => (
                  <div key={`${action.finding_id}-${index}`}>
                    <div className="automation-inline">
                      <StatusBadge status={action.status} />
                      {action.run_id && (
                        <Link
                          to={`${route}/automation?tab=runs&run=${encodeURIComponent(action.run_id)}`}
                        >
                          {t("关联运行", "Related run")}:{" "}
                          {action.run_id.slice(0, 8)}
                        </Link>
                      )}
                      <span className="text-muted">
                        {formatTime(action.updated_at)}
                      </span>
                    </div>
                    <div className="automation-item-links">
                      {action.item_ids?.map((id) => (
                        <LinkToAction key={id} route={route} id={id} />
                      ))}
                    </div>
                  </div>
                ))}
              </section>
            )}
            <Evidence
              title={t(
                "产物版本、来源映射与分析证据",
                "Artifact versions, source mapping, and analysis evidence",
              )}
              value={report.evidence}
              open
            />
            <Evidence title={t("完整报告", "Complete report")} value={report} />
          </div>
        )
      )}
    </Modal>
  );
});

function LinkToAction({ route, id }: { route: string; id: string }) {
  return (
    <Link to={`${route}/issues/${encodeURIComponent(id)}`}>
      {appStore.t("修复任务", "Repair task")} {id.slice(0, 8)}
    </Link>
  );
}

function Findings({
  value,
  report,
}: {
  value: unknown;
  report: QualityReport;
}) {
  if (!Array.isArray(value))
    return (
      <Evidence
        title={appStore.t("发现与定位", "Findings and locations")}
        value={value}
        open
      />
    );
  if (!value.length)
    return (
      <p className="text-muted">
        {appStore.t(
          "此维度没有记录的发现；请结合证据状态判断覆盖范围。",
          "There are no recorded findings in this dimension. Use its evidence status to judge coverage.",
        )}
      </p>
    );
  return (
    <div className="automation-findings">
      {value.map((finding: unknown, index) => {
        const entry =
          finding && typeof finding === "object"
            ? (finding as Record<string, unknown>)
            : { message: finding };
        const path =
          typeof (entry.path ?? entry.file ?? entry.file_path) === "string"
            ? String(entry.path ?? entry.file ?? entry.file_path)
            : "";
        const line = Number(entry.line ?? entry.start_line);
        const canLink =
          /^[\w.-]+\/[\w.-]+$/.test(report.repository) &&
          /^[0-9a-f]{40,64}$/i.test(report.commit_sha ?? "") &&
          path &&
          !path.startsWith("/") &&
          !path.split("/").includes("..");
        const location = canLink
          ? `https://github.com/${report.repository}/blob/${report.commit_sha}/${path.split("/").map(encodeURIComponent).join("/")}${Number.isSafeInteger(line) && line > 0 ? `#L${line}` : ""}`
          : "";
        return (
          <article key={index}>
            <p>
              {displayValue(
                entry.message ?? entry.reason ?? entry.title ?? finding,
              )}
            </p>
            {path &&
              (location ? (
                <a href={location} target="_blank" rel="noreferrer">
                  <ExternalLink size={12} />
                  {path}
                  {line > 0 ? `:${line}` : ""}
                </a>
              ) : (
                <code>{path}</code>
              ))}
            <Evidence
              title={appStore.t("定位证据", "Location evidence")}
              value={entry}
            />
          </article>
        );
      })}
    </div>
  );
}
