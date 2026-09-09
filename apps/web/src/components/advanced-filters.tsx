import { useEffect, useState } from "react";
import { observer } from "mobx-react-lite";
import { Plus, X } from "lucide-react";
import { appStore } from "../stores/app-store";
import { useDebouncedValue, useRemote, useScope } from "../lib/hooks";
import { api, projectPath, workspacePath } from "../lib/api";
import { memberName, priorities } from "../lib/utils";
import {
  Button,
  ErrorBox,
  Input,
  MultiSelect,
  Select,
  priorityLabels,
} from "./ui";
import type { Cycle, EstimateScheme, Module, WorkItem } from "../types";

export type FilterExpression =
  | { and: FilterExpression[] }
  | { or: FilterExpression[] }
  | { not: FilterExpression }
  | { field: string; op?: string; value?: unknown };
type FilterOption = { value: string; label: string; color?: string };
type FilterField = {
  key: string;
  zh: string;
  en: string;
  kind: "choice" | "association" | "text" | "number" | "date";
};
const fields: FilterField[] = [
  { key: "name", zh: "标题", en: "Title", kind: "text" },
  { key: "priority", zh: "优先级", en: "Priority", kind: "choice" },
  { key: "state_id", zh: "状态", en: "State", kind: "choice" },
  { key: "state_group", zh: "工作阶段", en: "State group", kind: "choice" },
  { key: "project_id", zh: "项目", en: "Project", kind: "choice" },
  { key: "assignee_id", zh: "负责人", en: "Assignee", kind: "association" },
  { key: "created_by", zh: "创建人", en: "Creator", kind: "choice" },
  { key: "label_id", zh: "标签", en: "Label", kind: "association" },
  { key: "cycle_id", zh: "迭代周期", en: "Cycle", kind: "choice" },
  { key: "module_id", zh: "功能模块", en: "Module", kind: "association" },
  { key: "parent_id", zh: "父工作项", en: "Parent work item", kind: "choice" },
  {
    key: "estimate_point_id",
    zh: "估算选项",
    en: "Estimate point",
    kind: "choice",
  },
  {
    key: "subscriber_id",
    zh: "订阅成员",
    en: "Subscriber",
    kind: "association",
  },
  {
    key: "mention_id",
    zh: "提及成员",
    en: "Mentioned member",
    kind: "association",
  },
  { key: "estimate", zh: "估算数值", en: "Estimate", kind: "number" },
  { key: "sequence_id", zh: "编号", en: "Sequence", kind: "number" },
  { key: "start_date", zh: "开始日期", en: "Start date", kind: "date" },
  { key: "target_date", zh: "目标日期", en: "Due date", kind: "date" },
  { key: "created_at", zh: "创建日期", en: "Created date", kind: "date" },
  { key: "updated_at", zh: "更新日期", en: "Updated date", kind: "date" },
  { key: "completed_at", zh: "完成日期", en: "Completed date", kind: "date" },
];
const operatorLabels: Record<string, [string, string]> = {
  eq: ["是", "is"],
  ne: ["不是", "is not"],
  in: ["包含任一", "is any of"],
  not_in: ["不包含", "is none of"],
  all: ["包含全部", "contains all"],
  is_empty: ["为空", "is empty"],
  not_empty: ["不为空", "is not empty"],
  contains: ["包含文字", "contains"],
  starts_with: ["开头为", "starts with"],
  gt: ["晚于 / 大于", "after / greater than"],
  gte: ["不早于 / 至少", "on or after / at least"],
  lt: ["早于 / 小于", "before / less than"],
  lte: ["不晚于 / 最多", "on or before / at most"],
};
const newCondition = (): FilterExpression => ({
  field: "priority",
  op: "in",
  value: ["high"],
});

export const AdvancedFilterBuilder = observer(function AdvancedFilterBuilder({
  value,
  onChange,
}: {
  value: FilterExpression | null;
  onChange: (value: FilterExpression | null) => void;
}) {
  const { workspace, project } = useScope();
  const t = appStore.t;
  const projectIDs = project
    ? [project.id]
    : appStore.workspaceProjects(workspace.id).map((item) => item.id);
  const projectKey = projectIDs.join(",");
  const [planningOptions, setPlanningOptions] = useState<
    Record<string, FilterOption[]>
  >({});
  const [optionError, setOptionError] = useState("");
  useEffect(() => {
    const projects = project
      ? [project]
      : appStore.workspaceProjects(workspace.id);
    for (const item of projects)
      appStore.loadProjectResources(workspace.id, item.id).catch(() => {});
  }, [workspace.id, projectKey]);
  useEffect(() => {
    const controller = new AbortController();
    setPlanningOptions({});
    setOptionError("");
    void Promise.allSettled(
      projectIDs.map(async (id) => {
        const base = projectPath(workspace.id, id);
        const [cycles, modules, estimates] = await Promise.all([
          api.get<Cycle[]>(`${base}/cycles`, controller.signal),
          api.get<Module[]>(`${base}/modules`, controller.signal),
          api.get<EstimateScheme[]>(`${base}/estimates`, controller.signal),
        ]);
        const prefix =
          projectIDs.length > 1
            ? `${appStore.projects.get(id)?.identifier ?? ""} · `
            : "";
        return {
          cycle_id: cycles.data.map((item) => ({
            value: item.id,
            label: `${prefix}${item.name}`,
          })),
          module_id: modules.data.map((item) => ({
            value: item.id,
            label: `${prefix}${item.name}`,
          })),
          estimate_point_id: estimates.data.flatMap((scheme) =>
            scheme.points.map((point) => ({
              value: point.id,
              label: `${prefix}${scheme.name} · ${point.label}`,
            })),
          ),
        };
      }),
    ).then((results) => {
      if (controller.signal.aborted) return;
      const available = results.flatMap((result) =>
        result.status === "fulfilled" ? [result.value] : [],
      );
      setPlanningOptions(
        Object.fromEntries(
          ["cycle_id", "module_id", "estimate_point_id"].map((key) => [
            key,
            available.flatMap((result) => result[key as keyof typeof result]),
          ]),
        ),
      );
      if (results.some((result) => result.status === "rejected"))
        setOptionError(
          t(
            "部分项目的筛选选项加载失败，请重新打开筛选器重试。",
            "Some project filter options could not load. Reopen the filters to retry.",
          ),
        );
    });
    return () => controller.abort();
  }, [workspace.id, projectKey]);
  const states = projectIDs.flatMap((id) => appStore.states.get(id) ?? []);
  const labels = [
    ...new Map(
      projectIDs
        .flatMap((id) => appStore.labels.get(id) ?? [])
        .map((label) => [label.id, label]),
    ).values(),
  ];
  const members = [
    ...new Map(
      projectIDs
        .flatMap((id) => appStore.members.get(id) ?? [])
        .map((member) => [member.user_id, member]),
    ).values(),
  ];
  const memberOptions = [
    { value: "me", label: t("我", "Me") },
    ...members.map((member) => ({
      value: member.user_id,
      label: memberName(member),
    })),
  ];
  const options: Record<string, FilterOption[]> = {
    priority: priorities.map((priority) => ({
      value: priority,
      label: t(...priorityLabels[priority]),
    })),
    state_id: states.map((state) => ({
      value: state.id,
      label: state.name,
      color: state.color,
    })),
    state_group: [
      { value: "backlog", label: t("待整理", "Backlog") },
      { value: "unstarted", label: t("待开始", "Unstarted") },
      { value: "started", label: t("进行中", "Started") },
      { value: "completed", label: t("已完成", "Completed") },
      { value: "cancelled", label: t("已取消", "Cancelled") },
    ],
    project_id: appStore
      .workspaceProjects(workspace.id)
      .map((item) => ({ value: item.id, label: item.name })),
    assignee_id: memberOptions,
    created_by: memberOptions,
    subscriber_id: memberOptions,
    mention_id: memberOptions,
    label_id: labels.map((label) => ({
      value: label.id,
      label: label.name,
      color: label.color,
    })),
    ...planningOptions,
  };
  return (
    <div className="advanced-filter-builder">
      <ErrorBox message={optionError} />
      {value ? (
        <FilterNode
          node={value}
          onChange={onChange}
          options={options}
          depth={0}
        />
      ) : (
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => onChange({ and: [newCondition()] })}
        >
          <Plus size={13} />
          {t("添加高级筛选条件", "Add advanced filters")}
        </Button>
      )}
    </div>
  );
});

const FilterNode = observer(function FilterNode({
  node,
  onChange,
  options,
  depth,
}: {
  node: FilterExpression;
  onChange: (value: FilterExpression | null) => void;
  options: Record<string, FilterOption[]>;
  depth: number;
}) {
  const t = appStore.t;
  const { workspace, project } = useScope();
  const parentField = "field" in node && node.field === "parent_id";
  const [parentSearch, setParentSearch] = useState("");
  const debouncedParentSearch = useDebouncedValue(parentSearch);
  const [parentCursor, setParentCursor] = useState<string | null>(null);
  const [parentItems, setParentItems] = useState<WorkItem[]>([]);
  const parentQuery = new URLSearchParams({
    limit: "50",
    search: debouncedParentSearch,
  });
  if (parentCursor) parentQuery.set("cursor", parentCursor);
  const parentResults = useRemote<WorkItem[]>(
    parentField
      ? `${project ? projectPath(workspace.id, project.id) : workspacePath(workspace.id)}/issues?${parentQuery}`
      : null,
  );
  useEffect(() => {
    setParentCursor(null);
    setParentItems([]);
  }, [workspace.id, project?.id, parentField, debouncedParentSearch]);
  useEffect(() => {
    if (parentResults.data)
      setParentItems((current) => [
        ...new Map(
          [...(parentCursor ? current : []), ...parentResults.data!].map(
            (item) => [item.id, item],
          ),
        ).values(),
      ]);
  }, [parentResults.data, parentCursor]);
  if (!("field" in node)) {
    const mode = "and" in node ? "and" : "or" in node ? "or" : "not";
    const children =
      "and" in node ? node.and : "or" in node ? node.or : [node.not];
    const change = (next: FilterExpression[]) =>
      onChange(
        next.length === 0
          ? null
          : mode === "and"
            ? { and: next }
            : mode === "or"
              ? { or: next }
              : { not: next.length === 1 ? next[0] : { and: next } },
      );
    return (
      <div className="filter-expression-group">
        <div className="filter-group-heading">
          <Select
            aria-label={t("筛选逻辑", "Filter logic")}
            value={mode}
            onChange={(event) =>
              onChange(
                event.target.value === "and"
                  ? { and: children }
                  : event.target.value === "or"
                    ? { or: children }
                    : { not: { and: children } },
              )
            }
          >
            <option value="and">
              {t("满足全部条件", "Match all conditions")}
            </option>
            <option value="or">
              {t("满足任一条件", "Match any condition")}
            </option>
            <option value="not">
              {t("排除以下条件", "Exclude these conditions")}
            </option>
          </Select>
          <span className="flex-spacer" />
          <Button
            type="button"
            variant="ghost"
            size="icon"
            aria-label={t("移除条件组", "Remove filter group")}
            onClick={() => onChange(null)}
          >
            <X size={13} />
          </Button>
        </div>
        {children.map((child, index) => (
          <FilterNode
            key={index}
            node={child}
            options={options}
            depth={depth + 1}
            onChange={(value) =>
              change(
                value
                  ? children.map((entry, position) =>
                      position === index ? value : entry,
                    )
                  : children.filter((_, position) => position !== index),
              )
            }
          />
        ))}
        <div className="inline-actions">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => change([...children, newCondition()])}
          >
            <Plus size={12} />
            {t("添加条件", "Add condition")}
          </Button>
          {depth < 3 && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => change([...children, { or: [newCondition()] }])}
            >
              {t("添加条件组", "Add group")}
            </Button>
          )}
        </div>
      </div>
    );
  }
  const field = fields.find((entry) => entry.key === node.field) ?? fields[0];
  const operators =
    field.kind === "text"
      ? ["contains", "starts_with", "eq", "ne", "is_empty", "not_empty"]
      : field.kind === "number" || field.kind === "date"
        ? ["eq", "ne", "gte", "lte", "gt", "lt", "is_empty", "not_empty"]
        : [
            "in",
            "not_in",
            ...(field.kind === "association" ? ["all"] : []),
            "is_empty",
            "not_empty",
            "eq",
            "ne",
          ];
  const operator = node.op ?? "eq";
  const selectedParentIDs = (
    Array.isArray(node.value) ? node.value : [node.value]
  ).filter((id): id is string => typeof id === "string" && !!id);
  const list = parentField
    ? [
        ...parentItems.map((item) => ({
          value: item.id,
          label: `${appStore.projects.get(item.project_id)?.identifier ?? ""}-${item.sequence_id} · ${item.name}`,
        })),
        ...selectedParentIDs
          .filter((id) => !parentItems.some((item) => item.id === id))
          .map((id) => ({
            value: id,
            label:
              appStore.issues.get(id)?.name ??
              t("已选择的工作项", "Selected work item"),
          })),
      ]
    : (options[node.field] ?? []);
  const multiple = ["in", "not_in", "all"].includes(operator);
  const relative =
    typeof node.value === "object" &&
    node.value !== null &&
    "relative_days" in node.value;
  return (
    <div className="filter-expression-row">
      <Select
        aria-label={t("筛选属性", "Filter property")}
        value={node.field}
        onChange={(event) => {
          const next = fields.find(
            (entry) => entry.key === event.target.value,
          )!;
          const choices = options[next.key] ?? [];
          onChange({
            field: next.key,
            op:
              next.kind === "choice" || next.kind === "association"
                ? "in"
                : next.kind === "text"
                  ? "contains"
                  : "eq",
            value:
              next.kind === "choice" || next.kind === "association"
                ? choices.slice(0, 1).map((entry) => entry.value)
                : next.kind === "number"
                  ? 0
                  : "",
          });
        }}
      >
        {fields.map((entry) => (
          <option key={entry.key} value={entry.key}>
            {t(entry.zh, entry.en)}
          </option>
        ))}
      </Select>
      <Select
        aria-label={t("筛选运算", "Filter operator")}
        value={operator}
        onChange={(event) => {
          const op = event.target.value;
          const many = ["in", "not_in", "all"].includes(op);
          onChange({
            ...node,
            op,
            value: ["is_empty", "not_empty"].includes(op)
              ? undefined
              : many
                ? Array.isArray(node.value)
                  ? node.value
                  : [node.value ?? list[0]?.value ?? ""]
                : Array.isArray(node.value)
                  ? (node.value[0] ?? "")
                  : (node.value ?? ""),
          });
        }}
      >
        {operators.map((op) => (
          <option key={op} value={op}>
            {t(...operatorLabels[op])}
          </option>
        ))}
      </Select>
      {!["is_empty", "not_empty"].includes(operator) && (
        <div className="filter-value">
          {parentField && (
            <>
              <Input
                aria-label={t("搜索父工作项", "Search parent work items")}
                placeholder={t(
                  "按标题或编号搜索",
                  "Search by title or identifier",
                )}
                value={parentSearch}
                onChange={(event) => setParentSearch(event.target.value)}
              />
              <ErrorBox message={parentResults.error} />
            </>
          )}
          {list.length ||
          field.kind === "choice" ||
          field.kind === "association" ? (
            multiple ? (
              <MultiSelect
                value={
                  Array.isArray(node.value)
                    ? node.value.map(String)
                    : [String(node.value ?? "")]
                }
                onChange={(value) => onChange({ ...node, value })}
                options={list}
                placeholder={t("选择值", "Choose values")}
              />
            ) : (
              <Select
                aria-label={t("筛选值", "Filter value")}
                value={String(node.value ?? "")}
                onChange={(event) =>
                  onChange({ ...node, value: event.target.value })
                }
              >
                <option value="">{t("选择", "Choose")}</option>
                {list.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </Select>
            )
          ) : (
            <>
              {field.kind === "date" && (
                <Select
                  aria-label={t("日期类型", "Date mode")}
                  value={relative ? "relative" : "absolute"}
                  onChange={(event) =>
                    onChange({
                      ...node,
                      value:
                        event.target.value === "relative"
                          ? { relative_days: 7 }
                          : "",
                    })
                  }
                >
                  <option value="absolute">
                    {t("指定日期", "Exact date")}
                  </option>
                  <option value="relative">
                    {t("距今天数", "Days from today")}
                  </option>
                </Select>
              )}
              <Input
                aria-label={t("筛选值", "Filter value")}
                type={
                  field.kind === "number" || relative
                    ? "number"
                    : field.kind === "date"
                      ? "date"
                      : "text"
                }
                value={
                  relative
                    ? Number(
                        (node.value as { relative_days: number }).relative_days,
                      )
                    : Array.isArray(node.value)
                      ? node.value.join(",")
                      : String(node.value ?? "")
                }
                placeholder={
                  multiple
                    ? t("多个值用逗号分隔", "Separate values with commas")
                    : undefined
                }
                onChange={(event) =>
                  onChange({
                    ...node,
                    value: relative
                      ? { relative_days: Number(event.target.value) }
                      : field.kind === "number"
                        ? Number(event.target.value)
                        : multiple
                          ? event.target.value
                              .split(",")
                              .map((part) => part.trim())
                          : event.target.value,
                  })
                }
              />
            </>
          )}
          {parentField && parentResults.pagination?.has_more && (
            <Button
              size="sm"
              variant="ghost"
              type="button"
              busy={parentResults.loading}
              onClick={() =>
                setParentCursor(parentResults.pagination?.next_cursor ?? null)
              }
            >
              {t("加载更多父工作项", "Load more parent work items")}
            </Button>
          )}
        </div>
      )}
      <Button
        type="button"
        variant="ghost"
        size="icon"
        aria-label={t("移除筛选条件", "Remove condition")}
        onClick={() => onChange(null)}
      >
        <X size={13} />
      </Button>
    </div>
  );
});

export function filtersToQuery(filters: Record<string, unknown>): string {
  return new URLSearchParams(
    Object.entries(filters)
      .filter(
        ([, value]) => value !== undefined && value !== null && value !== "",
      )
      .map(([key, value]) => [
        key,
        typeof value === "object" && !Array.isArray(value)
          ? JSON.stringify(value)
          : String(value),
      ]),
  ).toString();
}
