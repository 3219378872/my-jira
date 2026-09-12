import { useState } from "react";
import { observer } from "mobx-react-lite";
import { Link } from "react-router-dom";
import {
  Bell,
  ChartNoAxesCombined,
  ExternalLink,
  ListChecks,
  RefreshCw,
  ShieldAlert,
  TrendingUp,
} from "lucide-react";
import {
  Button,
  EmptyState,
  ErrorBox,
  Field,
  Loading,
  Select,
} from "../../components/ui";
import { api } from "../../lib/api";
import { useMutation, useRemote } from "../../lib/hooks";
import { appStore } from "../../stores/app-store";
import type { Forecast, Improvement, Risk } from "./contracts";
import { Evidence, Facts, formatDay, formatTime, StatusBadge } from "./shared";

interface AnalyticsProps {
  base: string;
  route: string;
  refreshKey: number;
  canManage: boolean;
  canEdit: boolean;
}

export const ForecastPanel = observer(function ForecastPanel({
  base,
  refreshKey,
}: AnalyticsProps) {
  const t = appStore.t;
  const forecasts = useRemote<Forecast[]>(
    `${base}/automation/forecasts`,
    refreshKey,
  );
  const [selected, setSelected] = useState<string | null>(null);
  const current =
    forecasts.data?.find(
      (forecast) => (forecast.id ?? forecast.as_of) === selected,
    ) ?? forecasts.data?.[0];
  const insufficient = current
    ? !current.p50 ||
      !current.p80 ||
      ["insufficient_data", "cold_start", "insufficient_history"].includes(
        current.status,
      )
    : true;
  if ([401, 403, 404].includes(forecasts.errorStatus ?? 0))
    return <ErrorBox message={forecasts.error} retry={forecasts.refresh} />;
  return (
    <div className="automation-stack">
      <div className="automation-section-heading">
        <div>
          <h2>{t("交付预测", "Delivery forecasts")}</h2>
          <p>
            {t(
              "P50 表示一半模拟可在该日前完成；P80 表示八成。预测与 Story 承诺分别保存。",
              "P50 is the date by which half of simulations finish; P80 covers 80%. Forecasts and story commitments are retained separately.",
            )}
          </p>
        </div>
        <Button size="sm" onClick={forecasts.refresh}>
          <RefreshCw size={14} />
          {t("刷新", "Refresh")}
        </Button>
      </div>
      <ErrorBox message={forecasts.error} retry={forecasts.refresh} />
      {forecasts.loading && !forecasts.data ? (
        <Loading />
      ) : !current ? (
        <EmptyState
          icon={<ChartNoAxesCombined size={24} />}
          title={t("尚无预测快照", "No forecast snapshots yet")}
          description={t(
            "从运行页执行交付预测。缺少历史时将保留数据不足的分析结果。",
            "Run a delivery forecast from the runs tab. Insufficient history is saved as an explicit result.",
          )}
        />
      ) : (
        <>
          {current.stale && (
            <p className="automation-notice">
              {t(
                "该预测已过期；当前保留上次可用结果。请查看最新运行的失败原因并重新计算。",
                "This forecast is stale; the last available result is retained. Check the latest run failure and recalculate.",
              )}
            </p>
          )}
          <section className="automation-card">
            <div className="automation-section-heading">
              <h3>
                {t("固定于", "As of")} {formatTime(current.as_of)}
              </h3>
              <StatusBadge status={current.status} />
            </div>
            {insufficient && (
              <div className="automation-notice">
                <strong>{t("历史数据不足", "Insufficient history")}</strong>
                <span>
                  {t(
                    "需至少 20 个已完成样本和四周历史。工时与日历形成的排期估计会单独保留，不能视为有历史支持的概率预测。",
                    "At least 20 completed samples and four weeks of history are required. Effort and calendar estimates remain separate from a historical probability forecast.",
                  )}
                </span>
              </div>
            )}
            <div className="automation-metrics">
              <div>
                <span>{t("P50 交付日期", "P50 delivery date")}</span>
                <strong>{insufficient ? "—" : formatDay(current.p50)}</strong>
              </div>
              <div>
                <span>{t("P80 交付日期", "P80 delivery date")}</span>
                <strong>{insufficient ? "—" : formatDay(current.p80)}</strong>
              </div>
              <div>
                <span>{t("已完成样本", "Completed samples")}</span>
                <strong>{current.sample_count ?? 0}</strong>
              </div>
              <div>
                <span>{t("剩余范围", "Remaining scope")}</span>
                <strong>{current.remaining ?? "—"}</strong>
              </div>
            </div>
            <Facts
              entries={[
                [
                  t("历史覆盖起点", "History coverage starts"),
                  formatTime(current.coverage_start),
                ],
                [t("算法版本", "Algorithm version"), current.algorithm_version],
                [t("快照时间", "Snapshot time"), formatTime(current.as_of)],
              ]}
            />
            <Evidence
              title={t("假设与样本边界", "Assumptions and sample boundaries")}
              value={current.assumptions}
              open
            />
            <Evidence
              title={t(
                "按时间切分的回测与基线比较",
                "Temporal backtest and baseline comparison",
              )}
              value={current.backtest}
              open
            />
            <Evidence
              title={t("完整预测快照", "Complete forecast snapshot")}
              value={current}
            />
          </section>
          <section className="automation-card">
            <h3>{t("历史预测与比较", "Forecast history and comparison")}</h3>
            <div className="automation-table-wrap">
              <table className="automation-table">
                <thead>
                  <tr>
                    <th>{t("预测时刻", "Forecast time")}</th>
                    <th>P50</th>
                    <th>P80</th>
                    <th>{t("样本", "Samples")}</th>
                    <th>{t("状态", "Status")}</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {forecasts.data?.map((forecast, index) => (
                    <tr
                      key={forecast.id ?? `${forecast.as_of}-${index}`}
                      className={current === forecast ? "is-selected" : ""}
                    >
                      <td>{formatTime(forecast.as_of)}</td>
                      <td>{formatDay(forecast.p50)}</td>
                      <td>{formatDay(forecast.p80)}</td>
                      <td>{forecast.sample_count}</td>
                      <td>
                        <StatusBadge
                          status={forecast.stale ? "stale" : forecast.status}
                        />
                      </td>
                      <td>
                        <Button
                          size="sm"
                          onClick={() =>
                            setSelected(forecast.id ?? forecast.as_of)
                          }
                        >
                          {t("查看快照", "View snapshot")}
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p className="text-muted">
              {t(
                "真实预测准确性需持续积累实际交付历史；缺失的回测指标保持未知。",
                "Real forecast accuracy requires accumulated delivery history. Missing backtest metrics remain unknown.",
              )}
            </p>
          </section>
        </>
      )}
    </div>
  );
});

const riskLabels: Record<string, [string, string]> = {
  deadline: ["交付延期", "Delivery delay"],
  forecast_delay: ["预测延期", "Forecast delay"],
  dependency: ["依赖阻塞", "Dependency blockage"],
  blocked: ["依赖阻塞", "Blocked work"],
  overload: ["容量不足", "Capacity shortage"],
  capacity: ["容量不足", "Capacity shortage"],
  skill_gap: ["技能缺口", "Skill gap"],
  scope: ["范围变化", "Scope change"],
  stale: ["长期停滞", "Stalled work"],
  quality: ["质量发现", "Quality findings"],
  over_capacity: ["容量超限", "Capacity exceeded"],
  skill_mismatch: ["技能不匹配", "Skill mismatch"],
  member_removed: ["负责人已移除", "Assignee removed"],
  commitment: ["交付承诺冲突", "Delivery commitment conflict"],
  no_working_days: ["没有可工作日期", "No working dates"],
  unknown_capacity: ["容量未知", "Unknown capacity"],
  unknown_estimate: ["工时估算未知", "Unknown effort estimate"],
  date_order: ["日期顺序冲突", "Date order conflict"],
  overdue: ["执行计划已逾期", "Execution plan overdue"],
  stagnation: ["长期停滞", "Stalled work"],
  scope_growth: ["范围增长", "Scope growth"],
  rework: ["返工增加", "Increased rework"],
};

export const RiskPanel = observer(function RiskPanel({
  base,
  route,
  canManage,
  canEdit,
  refreshKey,
  inboxRoute,
}: AnalyticsProps & { inboxRoute: string }) {
  const t = appStore.t;
  const risks = useRemote<Risk[]>(`${base}/automation/risks`, refreshKey);
  const [status, setStatus] = useState("");
  const mutation = useMutation();
  const visible = (risks.data ?? []).filter(
    (risk) => !status || risk.status === status,
  );
  const update = (risk: Risk, next: string) =>
    mutation.execute(async () => {
      await api.patch(`${base}/automation/risks/${risk.id}`, {
        status: next,
        version: risk.version,
      });
      risks.refresh();
    });
  const action = (risk: Risk) =>
    mutation.execute(async () => {
      await api.post(`${base}/automation/risks/${risk.id}/action`);
      risks.refresh();
    });
  if ([401, 403, 404].includes(risks.errorStatus ?? 0))
    return <ErrorBox message={risks.error} retry={risks.refresh} />;
  return (
    <div className="automation-stack">
      <div className="automation-section-heading">
        <div>
          <h2>{t("风险与应对", "Risks and responses")}</h2>
          <p>
            {t(
              "同一原因持续跟踪。应对行动进入统一工作项，无法解决的风险保留原因。",
              "Track each cause continuously. Response actions become work items; unresolved risks retain their reasons.",
            )}
          </p>
        </div>
        <div className="automation-inline">
          <Link className="button button-secondary button-sm" to={inboxRoute}>
            <Bell size={14} />
            {t("站内通知", "Inbox notifications")}
          </Link>
          <Button size="sm" onClick={risks.refresh}>
            <RefreshCw size={14} />
            {t("刷新", "Refresh")}
          </Button>
        </div>
      </div>
      <Field label={t("风险状态", "Risk status")}>
        <Select
          value={status}
          onChange={(event) => setStatus(event.target.value)}
        >
          <option value="">{t("全部风险", "All risks")}</option>
          <option value="open">{t("待处理", "Open")}</option>
          <option value="acknowledged">{t("已确认", "Acknowledged")}</option>
          <option value="resolved">{t("已解除", "Resolved")}</option>
        </Select>
      </Field>
      <ErrorBox
        message={risks.error || mutation.error}
        retry={risks.error ? risks.refresh : undefined}
      />
      {risks.loading && !risks.data ? (
        <Loading />
      ) : !visible.length ? (
        <EmptyState
          icon={<ShieldAlert size={24} />}
          title={t("没有符合条件的风险记录", "No matching risk records")}
          description={t(
            "先运行风险分析。没有记录仅表示当前没有保存的发现，不能证明项目不存在风险。",
            "Run risk detection first. An empty record set means there are no saved findings, and does not establish an absence of risk.",
          )}
        />
      ) : (
        visible.map((risk) => (
          <article className="automation-card" key={risk.id}>
            <div className="automation-section-heading">
              <div>
                <h3>
                  {riskLabels[risk.type]
                    ? t(...riskLabels[risk.type])
                    : risk.type}
                </h3>
                <p>{risk.reason}</p>
              </div>
              <div className="automation-inline">
                <StatusBadge status={risk.severity} />
                <StatusBadge status={risk.status} />
              </div>
            </div>
            <Facts
              entries={[
                [t("首次发现", "First seen"), formatTime(risk.first_seen)],
                [t("最近检查", "Last checked"), formatTime(risk.last_seen)],
              ]}
            />
            {!!risk.item_ids?.length && (
              <div className="automation-item-links">
                {risk.item_ids.map((id) => (
                  <Link to={`${route}/issues/${id}`} key={id}>
                    <ExternalLink size={12} />
                    {t("工作项", "Work item")} {id.slice(0, 8)}
                  </Link>
                ))}
              </div>
            )}
            <Evidence
              title={t("原因与证据", "Cause and evidence")}
              value={risk.evidence}
              open
            />
            <Evidence
              title={t(
                "风险变化与处置历史",
                "Risk lifecycle and response history",
              )}
              value={risk.history}
            />
            <div className="automation-form-actions">
              {risk.action_item_id ? (
                <Link
                  className="button button-secondary button-sm"
                  to={`${route}/issues/${risk.action_item_id}`}
                >
                  <ListChecks size={14} />
                  {t("打开应对任务", "Open response task")}
                </Link>
              ) : (
                canManage &&
                risk.status !== "resolved" && (
                  <Button
                    size="sm"
                    busy={mutation.busy}
                    onClick={() => action(risk)}
                  >
                    {t("创建应对任务", "Create response task")}
                  </Button>
                )
              )}
              {canEdit && (
                <>
                  {risk.status === "open" && (
                    <Button
                      size="sm"
                      busy={mutation.busy}
                      onClick={() => update(risk, "acknowledged")}
                    >
                      {t("确认风险", "Acknowledge risk")}
                    </Button>
                  )}
                  {risk.status !== "resolved" && (
                    <Button
                      size="sm"
                      busy={mutation.busy}
                      onClick={() => update(risk, "resolved")}
                    >
                      {t("标记解除", "Resolve risk")}
                    </Button>
                  )}
                  {risk.status === "resolved" && (
                    <Button
                      size="sm"
                      busy={mutation.busy}
                      onClick={() => update(risk, "open")}
                    >
                      {t("重新开启", "Reopen")}
                    </Button>
                  )}
                </>
              )}
            </div>
          </article>
        ))
      )}
    </div>
  );
});

export const ImprovementPanel = observer(function ImprovementPanel({
  base,
  route,
  canManage,
  canEdit,
  refreshKey,
}: AnalyticsProps) {
  const t = appStore.t;
  const improvements = useRemote<Improvement[]>(
    `${base}/automation/improvements`,
    refreshKey,
  );
  const mutation = useMutation();
  const act = (improvement: Improvement, action: "observe" | "action") =>
    mutation.execute(async () => {
      await api.post(
        `${base}/automation/improvements/${improvement.id}/${action}`,
      );
      improvements.refresh();
    });
  if ([401, 403, 404].includes(improvements.errorStatus ?? 0))
    return (
      <ErrorBox message={improvements.error} retry={improvements.refresh} />
    );
  return (
    <div className="automation-stack">
      <div className="automation-section-heading">
        <div>
          <h2>{t("效率改善与回看", "Process improvements and follow-up")}</h2>
          <p>
            {t(
              "固定团队流程指标的基线和观察窗口，记录执行行动及后续结果。流转时间不等于个人实际投入工时。",
              "Pin team process metrics and an observation window, then record actions and later outcomes. Cycle time is distinct from individual effort.",
            )}
          </p>
        </div>
        <Button size="sm" onClick={improvements.refresh}>
          <RefreshCw size={14} />
          {t("刷新", "Refresh")}
        </Button>
      </div>
      <ErrorBox
        message={improvements.error || mutation.error}
        retry={improvements.error ? improvements.refresh : undefined}
      />
      {improvements.loading && !improvements.data ? (
        <Loading />
      ) : !improvements.data?.length ? (
        <EmptyState
          icon={<TrendingUp size={24} />}
          title={t("尚无改善观察", "No improvement observations yet")}
          description={t(
            "从运行页分析效率，生成具体行动、指标基线和观察窗口。",
            "Analyze process efficiency from the runs tab to create actions, a metric baseline, and an observation window.",
          )}
        />
      ) : (
        improvements.data.map((improvement) => (
          <article className="automation-card" key={improvement.id}>
            <div className="automation-section-heading">
              <div>
                <h3>{improvement.reason}</h3>
                <p>
                  {t("目标指标", "Target metric")}: {improvement.target_metric}
                </p>
              </div>
              <StatusBadge status={improvement.status} />
            </div>
            <Facts
              entries={[
                [
                  t("观察开始", "Observation starts"),
                  formatTime(improvement.window_start),
                ],
                [
                  t("观察结束", "Observation ends"),
                  formatTime(improvement.window_end),
                ],
              ]}
            />
            <div className="automation-form-grid">
              <section>
                <h4>{t("行动前基线", "Baseline before action")}</h4>
                <Evidence
                  title={t(
                    "基线指标与数据范围",
                    "Baseline metrics and data scope",
                  )}
                  value={improvement.baseline}
                  open
                />
              </section>
              <section>
                <h4>{t("后续观测", "Follow-up observation")}</h4>
                {improvement.observed ? (
                  <Evidence
                    title={t(
                      "指标变化与解释",
                      "Metric changes and explanation",
                    )}
                    value={improvement.observed}
                    open
                  />
                ) : (
                  <p className="text-muted">
                    {t(
                      "尚未记录观测结果。",
                      "No observation has been recorded yet.",
                    )}
                  </p>
                )}
              </section>
            </div>
            <Evidence
              title={t(
                "行动、前置条件与完整记录",
                "Action, prerequisites, and full record",
              )}
              value={improvement}
            />
            <div className="automation-form-actions">
              {improvement.action_item_id ? (
                <Link
                  className="button button-secondary button-sm"
                  to={`${route}/issues/${improvement.action_item_id}`}
                >
                  <ListChecks size={14} />
                  {t("打开改善任务", "Open improvement task")}
                </Link>
              ) : (
                canManage && (
                  <Button
                    size="sm"
                    busy={mutation.busy}
                    onClick={() => act(improvement, "action")}
                  >
                    {t("创建改善任务", "Create improvement task")}
                  </Button>
                )
              )}
              {canEdit && (
                <Button
                  size="sm"
                  busy={mutation.busy}
                  onClick={() => act(improvement, "observe")}
                >
                  <RefreshCw size={14} />
                  {t("记录当前观测", "Record current observation")}
                </Button>
              )}
            </div>
            <p className="text-muted">
              {t(
                "结果区分观察中、有改善、无改善和证据不足。范围变化和数据缺口会影响解释。",
                "Outcomes distinguish observing, improved, no improvement, and insufficient evidence. Scope changes and data gaps affect interpretation.",
              )}
            </p>
          </article>
        ))
      )}
    </div>
  );
});
