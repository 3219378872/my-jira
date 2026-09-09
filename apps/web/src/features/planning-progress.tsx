import { useState } from "react";
import { observer } from "mobx-react-lite";
import { appStore } from "../stores/app-store";
import { dateTime, shortDate } from "../lib/utils";
import { Badge, Select } from "../components/ui";

interface Distribution {
  id: string | null;
  name: string;
  count: number;
  completed: number;
  cancelled: number;
  started: number;
  estimate: number;
  estimate_completed: number;
  color?: string;
}
export interface PlanningProgressData {
  total: number;
  completed: number;
  started: number;
  overdue: number;
  completion_percentage: number;
  estimate?: number;
  estimate_completed?: number;
  assignees?: Distribution[];
  labels?: Distribution[];
  burndown?: {
    date: string;
    remaining: number | null;
    estimate_remaining: number | null;
    ideal: number;
    estimate_ideal: number;
  }[];
  is_snapshot?: boolean;
  snapshot_at?: string | null;
}

export const PlanningProgress = observer(function PlanningProgress({
  data,
}: {
  data: PlanningProgressData;
}) {
  const [metric, setMetric] = useState("count");
  const [dimension, setDimension] = useState("assignees");
  const [showData, setShowData] = useState(false);
  const t = appStore.t;
  const points = data.burndown ?? [];
  const actual = points.map((point) =>
    metric === "count" ? point.remaining : point.estimate_remaining,
  );
  const ideal = points.map((point) =>
    metric === "count" ? point.ideal : point.estimate_ideal,
  );
  const max = Math.max(
    1,
    ...actual.filter((value): value is number => value !== null),
    ...ideal,
  );
  const width = 760;
  const height = 230;
  const x = (index: number) =>
    45 + (index / Math.max(1, points.length - 1)) * (width - 65);
  const y = (value: number) => 15 + (1 - value / max) * (height - 50);
  const path = (values: (number | null)[]) => {
    let pen = false;
    return values
      .map((value, index) => {
        if (value === null) {
          pen = false;
          return "";
        }
        const command = pen ? "L" : "M";
        pen = true;
        return `${command}${x(index)},${y(value)}`;
      })
      .join(" ");
  };
  const distribution =
    dimension === "assignees" ? (data.assignees ?? []) : (data.labels ?? []);
  return (
    <div className="planning-progress-view">
      <div className="settings-toolbar">
        <h2>{t("进度分析", "Progress analysis")}</h2>
        <span className="flex-spacer" />
        <Select
          aria-label={t("进度指标", "Progress metric")}
          value={metric}
          onChange={(event) => setMetric(event.target.value)}
        >
          <option value="count">{t("工作项数量", "Work item count")}</option>
          <option value="estimate">{t("估算点数", "Estimate points")}</option>
        </Select>
      </div>
      {data.is_snapshot && (
        <p className="settings-note">
          <Badge>{t("转移前快照", "Snapshot before transfer")}</Badge>{" "}
          {dateTime(data.snapshot_at ?? "", appStore.locale)}
        </p>
      )}
      <section className="analytics-panel">
        <div className="section-title">
          <h2>{t("燃尽图", "Burndown")}</h2>
          <button onClick={() => setShowData(!showData)}>
            {showData ? t("隐藏数据", "Hide data") : t("查看数据", "View data")}
          </button>
        </div>
        {points.length ? (
          <>
            <svg
              viewBox={`0 0 ${width} ${height}`}
              role="img"
              aria-label={t(
                "剩余工作与理想进度燃尽图",
                "Burndown of remaining work and ideal progress",
              )}
              className="burndown-chart"
            >
              {Array.from({ length: 5 }, (_, index) => {
                const value = (max * index) / 4;
                return (
                  <g key={index}>
                    <line
                      x1={45}
                      x2={width - 20}
                      y1={y(value)}
                      y2={y(value)}
                      stroke="var(--border)"
                    />
                    <text x={38} y={y(value) + 4} textAnchor="end">
                      {Number(value.toFixed(1))}
                    </text>
                  </g>
                );
              })}
              <path
                d={path(ideal)}
                fill="none"
                stroke="var(--text-muted)"
                strokeWidth={2}
                strokeDasharray="5 5"
              />
              <path
                d={path(actual)}
                fill="none"
                stroke="var(--accent)"
                strokeWidth={2.5}
              />
              {points.map((point, index) =>
                actual[index] !== null ? (
                  <circle
                    key={point.date}
                    cx={x(index)}
                    cy={y(actual[index]!)}
                    r={3}
                    fill="var(--accent)"
                  >
                    <title>
                      {point.date}: {actual[index]}
                    </title>
                  </circle>
                ) : null,
              )}
              <text x={45} y={height - 5}>
                {shortDate(points[0].date, appStore.locale)}
              </text>
              <text x={width - 20} y={height - 5} textAnchor="end">
                {shortDate(points[points.length - 1].date, appStore.locale)}
              </text>
            </svg>
            <div className="chart-legend">
              <span>
                <i style={{ background: "var(--accent)" }} />
                {t("实际剩余", "Actual remaining")}
              </span>
              <span>
                <i style={{ background: "var(--text-muted)" }} />
                {t("理想进度", "Ideal progress")}
              </span>
            </div>
            {showData && (
              <div className="progress-data-scroll">
                <table className="analytics-data-table">
                  <thead>
                    <tr>
                      <th>{t("日期", "Date")}</th>
                      <th>{t("剩余", "Remaining")}</th>
                      <th>{t("理想", "Ideal")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {points.map((point, index) => (
                      <tr key={point.date}>
                        <td>{point.date}</td>
                        <td>{actual[index] ?? "—"}</td>
                        <td>{Number(ideal[index].toFixed(2))}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </>
        ) : (
          <p className="settings-note">
            {t(
              "设置开始与结束日期后查看燃尽图。",
              "Set start and end dates to see the burndown chart.",
            )}
          </p>
        )}
      </section>
      <section className="analytics-panel">
        <div className="section-title">
          <h2>{t("工作分布", "Work distribution")}</h2>
          <Select
            aria-label={t("进度分组", "Progress grouping")}
            value={dimension}
            onChange={(event) => setDimension(event.target.value)}
          >
            <option value="assignees">{t("负责人", "Assignees")}</option>
            <option value="labels">{t("标签", "Labels")}</option>
          </Select>
        </div>
        <p className="settings-note">
          {t(
            "有多个负责人或标签的工作项会出现在多个分组中。",
            "Work with multiple assignees or labels appears in each matching group.",
          )}
        </p>
        {distribution.map((entry) => {
          const value = metric === "count" ? entry.count : entry.estimate;
          const completed =
            metric === "count" ? entry.completed : entry.estimate_completed;
          return (
            <div className="planning-distribution-row" key={entry.id ?? "none"}>
              <span>{entry.id ? entry.name : t("未分配", "Unassigned")}</span>
              <div className="progress-track">
                <span
                  style={{
                    width: `${value ? (completed / value) * 100 : 0}%`,
                    background: entry.color || undefined,
                  }}
                />
              </div>
              <strong>
                {completed} / {value}
              </strong>
            </div>
          );
        })}
        {!distribution.length && (
          <p className="settings-note">
            {t("暂无工作项", "No work items yet")}
          </p>
        )}
      </section>
    </div>
  );
});
