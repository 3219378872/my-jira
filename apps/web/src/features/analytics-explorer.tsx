import { useState } from "react";
import { observer } from "mobx-react-lite";
import { Link } from "react-router-dom";
import { BarChart3, Download, Plus, Save, Table2, Trash2 } from "lucide-react";
import { appStore } from "../stores/app-store";
import { api, workspacePath } from "../lib/api";
import { useMutation, useRemote, useScope } from "../lib/hooks";
import {
  AdvancedFilterBuilder,
  type FilterExpression,
} from "../components/advanced-filters";
import {
  Badge,
  Button,
  Confirm,
  EmptyState,
  ErrorBox,
  Field,
  Input,
  Loading,
  Modal,
  Select,
} from "../components/ui";

interface SavedAnalysis {
  id: string;
  name: string;
  description: string;
  query: Record<string, string>;
}
interface AnalysisResult {
  total: number;
  distribution: {
    key: string;
    label: string;
    segment_key: string;
    segment_label: string;
    count: number;
    estimate: number;
    value: number;
  }[];
}
const dimensions = [
  { value: "state", zh: "状态", en: "State" },
  { value: "state_group", zh: "工作阶段", en: "State group" },
  { value: "priority", zh: "优先级", en: "Priority" },
  { value: "project", zh: "项目", en: "Project" },
  { value: "label", zh: "标签", en: "Label" },
  { value: "assignee", zh: "负责人", en: "Assignee" },
  { value: "estimate", zh: "估算", en: "Estimate" },
  { value: "cycle", zh: "周期", en: "Cycle" },
  { value: "module", zh: "模块", en: "Module" },
  { value: "created_at", zh: "创建日期", en: "Created date" },
  { value: "updated_at", zh: "更新日期", en: "Updated date" },
  { value: "completed_at", zh: "完成日期", en: "Completed date" },
  { value: "start_date", zh: "开始日期", en: "Start date" },
  { value: "target_date", zh: "目标日期", en: "Due date" },
];
const chartColors = [
  "#697bd3",
  "#57a394",
  "#c78e59",
  "#aa78b3",
  "#719ec0",
  "#99a85c",
  "#cc7d87",
];

export const AnalyticsExplorer = observer(function AnalyticsExplorer() {
  const { workspace } = useScope();
  const base = workspacePath(workspace.id);
  const analyses = useRemote<SavedAnalysis[]>(`${base}/analyses`);
  const [selected, setSelected] = useState("");
  const [dirty, setDirty] = useState(false);
  const [form, setForm] = useState<Record<string, string>>({
    x_axis: "state_group",
    segment: "",
    metric: "count",
    interval: "day",
    date_field: "created_at",
    from: "",
    to: "",
  });
  const [filter, setFilter] = useState<FilterExpression | null>(null);
  const [showFilter, setShowFilter] = useState(false);
  const [table, setTable] = useState(false);
  const [saveOpen, setSaveOpen] = useState(false);
  const [saveAs, setSaveAs] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [exported, setExported] = useState(false);
  const mutation = useMutation();
  const query = Object.fromEntries(
    Object.entries({
      ...form,
      filter: filter ? JSON.stringify(filter) : "",
    }).filter(([, value]) => value !== ""),
  );
  const current = useRemote<AnalysisResult>(
    selected && !dirty
      ? `${base}/analyses/${selected}/run`
      : `${base}/analytics?${new URLSearchParams(query)}`,
  );
  const t = appStore.t;
  const update = (changes: Record<string, string>) => {
    setForm({ ...form, ...changes });
    setDirty(true);
  };
  const choose = (id: string) => {
    setSelected(id);
    setDirty(false);
    const analysis = analyses.data?.find((item) => item.id === id);
    if (!analysis) return;
    setForm({
      x_axis: "state_group",
      segment: "",
      metric: "count",
      interval: "day",
      date_field: "created_at",
      from: "",
      to: "",
      ...analysis.query,
      filter: "",
    });
    try {
      setFilter(
        analysis.query.filter ? JSON.parse(analysis.query.filter) : null,
      );
    } catch {
      setFilter(null);
    }
    setName(analysis.name);
    setDescription(analysis.description);
  };
  const points = current.data?.distribution ?? [];
  const groups = [
    ...new Map(points.map((point) => [point.key, point.label])).entries(),
  ];
  const segments = [
    ...new Map(
      points.map((point) => [point.segment_key, point.segment_label]),
    ).entries(),
  ];
  const maximum = Math.max(
    1,
    ...groups.map(([key]) =>
      points
        .filter((point) => point.key === key)
        .reduce((sum, point) => sum + point.value, 0),
    ),
  );
  return (
    <section className="analytics-explorer">
      <div className="settings-toolbar">
        <Select
          aria-label={t("已保存的分析", "Saved analysis")}
          value={selected}
          onChange={(event) => choose(event.target.value)}
        >
          <option value="">{t("临时分析", "New analysis")}</option>
          {analyses.data?.map((analysis) => (
            <option key={analysis.id} value={analysis.id}>
              {analysis.name}
            </option>
          ))}
        </Select>
        {dirty && selected && (
          <Badge>{t("有未保存更改", "Unsaved changes")}</Badge>
        )}
        <span className="flex-spacer" />
        <Button size="sm" onClick={() => setTable(!table)}>
          {table ? <BarChart3 size={14} /> : <Table2 size={14} />}
          {table ? t("图表", "Chart") : t("数据表", "Table")}
        </Button>
        <Button
          size="sm"
          busy={mutation.busy}
          onClick={() =>
            mutation.execute(
              async () => {
                await api.post(`${base}/analytics/export`, { query });
                setExported(true);
              },
              t("分析已加入导出队列", "Analysis added to the export queue"),
            )
          }
        >
          <Download size={14} />
          {t("导出 CSV", "Export CSV")}
        </Button>
        {workspace.role === 20 && (
          <>
            <Button
              variant="primary"
              size="sm"
              onClick={() => {
                setSaveAs(false);
                setSaveOpen(true);
              }}
            >
              <Save size={14} />
              {t("保存分析", "Save analysis")}
            </Button>
            {selected && (
              <>
                <Button
                  size="sm"
                  onClick={() => {
                    setSaveAs(true);
                    setSaveOpen(true);
                  }}
                >
                  <Plus size={14} />
                  {t("另存为", "Save as")}
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={t("删除分析", "Delete analysis")}
                  onClick={() => setDeleteOpen(true)}
                >
                  <Trash2 size={14} />
                </Button>
              </>
            )}
          </>
        )}
      </div>
      {exported && (
        <p className="settings-note">
          <Link to={`/w/${workspace.slug}/settings/exports`}>
            {t(
              "查看导出进度与下载文件",
              "View export progress and download files",
            )}{" "}
            →
          </Link>
        </p>
      )}
      <div className="analytics-query-controls">
        <Field label={t("统计维度", "Dimension")}>
          <Select
            value={form.x_axis}
            onChange={(event) =>
              update({
                x_axis: event.target.value,
                ...(form.segment === event.target.value ? { segment: "" } : {}),
              })
            }
          >
            {dimensions.map((dimension) => (
              <option key={dimension.value} value={dimension.value}>
                {t(dimension.zh, dimension.en)}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t("细分维度", "Segment")}>
          <Select
            value={form.segment}
            onChange={(event) => update({ segment: event.target.value })}
          >
            <option value="">{t("无细分", "No segment")}</option>
            {dimensions
              .filter((dimension) => dimension.value !== form.x_axis)
              .map((dimension) => (
                <option key={dimension.value} value={dimension.value}>
                  {t(dimension.zh, dimension.en)}
                </option>
              ))}
          </Select>
        </Field>
        <Field label={t("统计指标", "Metric")}>
          <Select
            value={form.metric}
            onChange={(event) => update({ metric: event.target.value })}
          >
            <option value="count">{t("工作项数量", "Work item count")}</option>
            <option value="estimate">
              {t("估算值合计", "Estimate total")}
            </option>
          </Select>
        </Field>
        <Field label={t("时间粒度", "Interval")}>
          <Select
            value={form.interval}
            onChange={(event) => update({ interval: event.target.value })}
          >
            <option value="day">{t("按日", "Daily")}</option>
            <option value="week">{t("按周", "Weekly")}</option>
            <option value="month">{t("按月", "Monthly")}</option>
          </Select>
        </Field>
        <Field label={t("日期依据", "Date field")}>
          <Select
            value={form.date_field}
            onChange={(event) => update({ date_field: event.target.value })}
          >
            {dimensions
              .filter((dimension) =>
                [
                  "created_at",
                  "updated_at",
                  "completed_at",
                  "start_date",
                  "target_date",
                ].includes(dimension.value),
              )
              .map((dimension) => (
                <option key={dimension.value} value={dimension.value}>
                  {t(dimension.zh, dimension.en)}
                </option>
              ))}
          </Select>
        </Field>
        <Field label={t("开始日期", "From")}>
          <Input
            type="date"
            value={form.from}
            max={form.to || undefined}
            onChange={(event) => update({ from: event.target.value })}
          />
        </Field>
        <Field label={t("结束日期", "To")}>
          <Input
            type="date"
            value={form.to}
            min={form.from || undefined}
            onChange={(event) => update({ to: event.target.value })}
          />
        </Field>
      </div>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => setShowFilter(!showFilter)}
      >
        {showFilter
          ? t("收起筛选", "Hide filters")
          : t("高级筛选", "Advanced filters")}
      </Button>
      {showFilter && (
        <AdvancedFilterBuilder
          value={filter}
          onChange={(value) => {
            setFilter(value);
            setDirty(true);
          }}
        />
      )}
      <ErrorBox
        message={analyses.error || current.error || mutation.error}
        retry={current.refresh}
      />
      {current.loading ? (
        <Loading />
      ) : !points.length ? (
        <EmptyState
          icon={<BarChart3 size={28} />}
          title={t("暂无可分析的数据", "No data for this analysis")}
          description={t(
            "调整维度、日期或筛选条件。",
            "Adjust the dimensions, dates, or filters.",
          )}
        />
      ) : table ? (
        <table className="data-table">
          <thead>
            <tr>
              <th>{t("维度", "Dimension")}</th>
              <th>{t("细分", "Segment")}</th>
              <th>{t("工作项", "Work items")}</th>
              <th>{t("估算", "Estimate")}</th>
            </tr>
          </thead>
          <tbody>
            {points.map((point) => (
              <tr key={`${point.key}:${point.segment_key}`}>
                <td>{point.label || t("未设置", "Not set")}</td>
                <td>{point.segment_label || "—"}</td>
                <td>{point.count}</td>
                <td>{point.estimate}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <div className="analysis-chart">
          <div className="analysis-legend">
            {segments.map(([key, label], index) => (
              <span key={key}>
                <i
                  style={{
                    backgroundColor: chartColors[index % chartColors.length],
                  }}
                />
                {label || t("全部工作", "All work")}
              </span>
            ))}
          </div>
          {groups.map(([key, label]) => {
            const values = points.filter((point) => point.key === key);
            const total = values.reduce((sum, point) => sum + point.value, 0);
            return (
              <div className="analysis-chart-row" key={key}>
                <span title={label}>{label || t("未设置", "Not set")}</span>
                <div className="analysis-chart-track">
                  {values.map((point) => (
                    <i
                      key={point.segment_key}
                      title={`${point.segment_label || label}: ${point.value}`}
                      style={{
                        width: `${(point.value / maximum) * 100}%`,
                        backgroundColor:
                          chartColors[
                            Math.max(
                              0,
                              segments.findIndex(
                                ([segment]) => segment === point.segment_key,
                              ),
                            ) % chartColors.length
                          ],
                      }}
                    />
                  ))}
                </div>
                <strong>{total}</strong>
              </div>
            );
          })}
        </div>
      )}
      <Modal
        open={saveOpen}
        onOpenChange={setSaveOpen}
        title={t("保存分析", "Save analysis")}
      >
        <form
          className="form-stack modal-body"
          onSubmit={(event) => {
            event.preventDefault();
            mutation.execute(
              async () => {
                const body = { name, description, query };
                const result =
                  selected && !saveAs
                    ? await api.patch<SavedAnalysis>(
                        `${base}/analyses/${selected}`,
                        body,
                      )
                    : await api.post<SavedAnalysis>(`${base}/analyses`, body);
                setSelected(result.data.id);
                setDirty(false);
                setSaveOpen(false);
                analyses.refresh();
                current.refresh();
              },
              t("分析已保存", "Analysis saved"),
            );
          }}
        >
          <Field label={t("名称", "Name")}>
            <Input
              value={name}
              onChange={(event) => setName(event.target.value)}
              required
              autoFocus
            />
          </Field>
          <Field label={t("说明", "Description")}>
            <Input
              value={description}
              onChange={(event) => setDescription(event.target.value)}
            />
          </Field>
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button variant="primary" type="submit" busy={mutation.busy}>
              {t("保存", "Save")}
            </Button>
          </div>
        </form>
      </Modal>
      <Confirm
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title={t("删除这份分析？", "Delete this analysis?")}
        description={t(
          "保存的分析条件会被移除，工作项不受影响。",
          "The saved analysis will be removed. Work items are preserved.",
        )}
        busy={mutation.busy}
        onConfirm={() =>
          mutation.execute(async () => {
            await api.delete(`${base}/analyses/${selected}`);
            setSelected("");
            setDeleteOpen(false);
            analyses.refresh();
          })
        }
      />
    </section>
  );
});
