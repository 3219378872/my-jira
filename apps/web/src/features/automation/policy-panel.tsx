import { useState, type FormEvent } from "react";
import { ShieldCheck, ShieldOff } from "lucide-react";
import { observer } from "mobx-react-lite";
import {
  Button,
  ErrorBox,
  Field,
  Input,
  MultiSelect,
} from "../../components/ui";
import { api } from "../../lib/api";
import { useMutation, useRemote } from "../../lib/hooks";
import { memberName } from "../../lib/utils";
import { appStore } from "../../stores/app-store";
import type { Member, PageDocument } from "../../types";
import type { AutomationPolicy } from "./contracts";
import { kindLabels, parseIDs } from "./presentation";

const fieldLabels: [string, string, string][] = [
  ["name", "标题", "Title"],
  ["description_html", "正文", "Description"],
  ["requirement_type", "需求类型", "Requirement type"],
  ["story_role", "业务角色", "Story role"],
  ["story_goal", "故事目标", "Story goal"],
  ["story_benefit", "故事价值", "Story benefit"],
  ["acceptance_criteria", "验收条件", "Acceptance criteria"],
  ["parent_id", "父级", "Parent"],
  ["estimated_minutes", "预计工时", "Estimated effort"],
  ["remaining_minutes", "剩余工时", "Remaining effort"],
  ["required_skills", "所需技能", "Required skills"],
  ["start_date", "开始日期", "Start date"],
  ["target_date", "目标日期", "Target date"],
  ["assignee_ids", "负责人", "Assignees"],
  ["allocation_weights", "工时分摊", "Effort allocation"],
  ["dependency_ids", "依赖", "Dependencies"],
  ["cycle_id", "执行 Sprint", "Execution sprint"],
  ["activity_id", "用户活动", "User activity"],
  ["map_position", "地图顺序", "Map position"],
  ["priority", "优先级", "Priority"],
  ["state_id", "状态", "State"],
];

const entityLabels = [
  ["epic", "Epic", "Epic"],
  ["story", "Story", "Story"],
  ["task", "Task", "Task"],
  ["work_item", "未分类工作项", "Unclassified work items"],
] as const;

export const PolicyPanel = observer(function PolicyPanel({
  base,
  policy,
  canManage,
  onSaved,
}: {
  base: string;
  policy: AutomationPolicy;
  canManage: boolean;
  onSaved: () => void;
}) {
  const t = appStore.t;
  const [form, setForm] = useState<AutomationPolicy>(() => ({
    ...policy,
    allowed_operations: policy.allowed_operations ?? ["create", "update"],
  }));
  const [baseVersion, setBaseVersion] = useState(policy.version);
  const [itemIDs, setItemIDs] = useState(
    (policy.allowed_item_ids ?? []).join("\n"),
  );
  const [pageIDs, setPageIDs] = useState(
    (policy.allowed_page_ids ?? []).join("\n"),
  );
  const [memberIDs, setMemberIDs] = useState(
    (policy.allowed_member_ids ?? []).join("\n"),
  );
  const [dirty, setDirty] = useState(false);
  const members = useRemote<Member[]>(`${base}/members`);
  const pages = useRemote<PageDocument[]>(`${base}/pages`);
  const mutation = useMutation();
  const allowedEntities = form.allowed_entities?.length
    ? form.allowed_entities
    : entityLabels.map(([value]) => value);
  const update = <K extends keyof AutomationPolicy>(
    key: K,
    value: AutomationPolicy[K],
  ) => {
    setForm((previous) => ({ ...previous, [key]: value }));
    setDirty(true);
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    mutation.execute(
      async () => {
        const response = await api.put<AutomationPolicy>(
          `${base}/automation/policy`,
          {
            ...form,
            version: baseVersion,
            allowed_item_ids: parseIDs(itemIDs),
            allowed_member_ids: parseIDs(memberIDs),
            allowed_page_ids: parseIDs(pageIDs),
            hard_deadline: form.hard_deadline || null,
          },
        );
        setForm(response.data);
        setBaseVersion(response.data.version);
        setDirty(false);
        onSaved();
      },
      t("自动化策略已保存", "Automation policy saved"),
    );
  };
  const toggleChoice = (
    key: "allowed_kinds" | "allowed_fields" | "allowed_operations",
    value: string,
    enabled: boolean,
  ) =>
    update(
      key,
      enabled
        ? [...new Set([...form[key], value])]
        : form[key].filter((entry) => entry !== value),
    );

  return (
    <form className="automation-policy" onSubmit={submit}>
      <div className="automation-section-heading">
        <div>
          <h2>{t("项目自动化策略", "Project automation policy")}</h2>
          <p>
            {t(
              "保存后，授权范围内的变更将自动应用。人工锁定、已完成事实与硬约束始终受保护。",
              "Once enabled, permitted changes are applied automatically. Manual locks, completed work, and hard constraints remain protected.",
            )}
          </p>
        </div>
        <span className="text-muted">
          {t("版本", "Version")} {policy.version}
        </span>
      </div>
      <ErrorBox message={mutation.error} />
      {policy.version > baseVersion && (
        <div className="automation-notice">
          <span>
            {t(
              "另一位管理员已修改策略。当前草稿保留原版本，保存时会检查冲突。",
              "Another administrator changed the policy. This draft retains its original version and saving will check for conflicts.",
            )}
          </span>
          <Button
            type="button"
            size="sm"
            onClick={() => {
              setForm({
                ...policy,
                allowed_operations: policy.allowed_operations ?? [
                  "create",
                  "update",
                ],
              });
              setBaseVersion(policy.version);
              setItemIDs((policy.allowed_item_ids ?? []).join("\n"));
              setMemberIDs((policy.allowed_member_ids ?? []).join("\n"));
              setPageIDs((policy.allowed_page_ids ?? []).join("\n"));
              setDirty(false);
              mutation.setError("");
            }}
          >
            {t("载入最新策略", "Load latest policy")}
          </Button>
        </div>
      )}
      {!canManage && (
        <p className="automation-notice">
          {t(
            "仅项目管理员可更改自动化策略。",
            "Only project administrators can change automation policy.",
          )}
        </p>
      )}
      <fieldset disabled={!canManage || mutation.busy}>
        <section
          className={`automation-enable ${form.enabled ? "is-enabled" : ""}`}
        >
          {form.enabled ? <ShieldCheck size={22} /> : <ShieldOff size={22} />}
          <div>
            <strong>{t("自动应用", "Automatic application")}</strong>
            <p>
              {t(
                "停用后停止接受新的自动化执行，并在运行提交前重新校验策略。",
                "Disabling stops new automation runs; the policy is checked again before an in-flight run commits.",
              )}
            </p>
          </div>
          <label className="automation-check">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(event) => update("enabled", event.target.checked)}
            />
            {t("启用", "Enabled")}
          </label>
        </section>
        <section className="automation-card">
          <h3>
            {t("允许的能力与操作", "Allowed capabilities and operations")}
          </h3>
          <div className="automation-check-grid">
            {Object.entries(kindLabels).map(([kind, labels]) => (
              <label className="automation-check" key={kind}>
                <input
                  type="checkbox"
                  checked={form.allowed_kinds.includes(kind)}
                  onChange={(event) =>
                    toggleChoice("allowed_kinds", kind, event.target.checked)
                  }
                />
                {t(...labels)}
              </label>
            ))}
          </div>
          <div className="automation-inline">
            {(
              [
                ["create", "创建实体", "Create entities"],
                ["update", "更新实体", "Update entities"],
              ] as const
            ).map(([operation, zh, en]) => (
              <label className="automation-check" key={operation}>
                <input
                  type="checkbox"
                  checked={form.allowed_operations.includes(operation)}
                  onChange={(event) =>
                    toggleChoice(
                      "allowed_operations",
                      operation,
                      event.target.checked,
                    )
                  }
                />
                {t(zh, en)}
              </label>
            ))}
          </div>
        </section>
        {Array.isArray(policy.allowed_entities) && (
          <section className="automation-card">
            <h3>
              {t("允许管理的实体类型", "Entity types automation may manage")}
            </h3>
            <div className="automation-check-grid">
              {entityLabels.map(([value, zh, en]) => (
                <label className="automation-check" key={value}>
                  <input
                    type="checkbox"
                    checked={allowedEntities.includes(value)}
                    disabled={
                      allowedEntities.length === 1 &&
                      allowedEntities.includes(value)
                    }
                    onChange={(event) =>
                      update(
                        "allowed_entities",
                        event.target.checked
                          ? [...allowedEntities, value]
                          : allowedEntities.filter(
                              (entity) => entity !== value,
                            ),
                      )
                    }
                  />
                  {t(zh, en)}
                </label>
              ))}
            </div>
            <p className="text-muted">
              {t(
                "至少保留一种类型。如需停止全部实体写入，可取消创建和更新操作。",
                "Keep at least one entity type selected. Disable both create and update operations to stop all entity writes.",
              )}
            </p>
          </section>
        )}
        <section className="automation-card">
          <h3>{t("允许修改的字段", "Fields automation may modify")}</h3>
          <div className="automation-check-grid">
            {fieldLabels.map(([field, zh, en]) => (
              <label className="automation-check" key={field}>
                <input
                  type="checkbox"
                  checked={form.allowed_fields.includes(field)}
                  onChange={(event) =>
                    toggleChoice("allowed_fields", field, event.target.checked)
                  }
                />
                {t(zh, en)}
              </label>
            ))}
          </div>
        </section>
        <section className="automation-card">
          <h3>{t("项目内授权范围", "Authorized scope in this project")}</h3>
          <p className="text-muted">
            {t(
              "范围留空表示当前项目内所有有效对象。成员技能、日历、容量、Story 承诺与人工锁定仍由执行校验保护。",
              "An empty scope includes all active objects in this project. Skills, calendars, capacity, story commitments, and manual locks are still enforced.",
            )}
          </p>
          <ErrorBox message={members.error || pages.error} />
          <div className="automation-form-grid">
            <Field
              label={t("可分配成员", "Assignable members")}
              hint={t(
                "留空允许当前项目全部有效成员。",
                "Leave empty to allow all active project members.",
              )}
            >
              <MultiSelect
                value={parseIDs(memberIDs)}
                onChange={(ids) => {
                  setMemberIDs(ids.join("\n"));
                  setDirty(true);
                }}
                options={(members.data ?? []).map((member) => ({
                  value: member.user_id,
                  label: memberName(member),
                }))}
                placeholder={t("全部有效成员", "All active members")}
                disabled={!canManage || mutation.busy}
              />
            </Field>
            <Field
              label={t("PRD 来源文档", "PRD source documents")}
              hint={t(
                "留空允许当前项目全部可访问文档。",
                "Leave empty to allow all accessible project documents.",
              )}
            >
              <MultiSelect
                value={parseIDs(pageIDs)}
                onChange={(ids) => {
                  setPageIDs(ids.join("\n"));
                  setDirty(true);
                }}
                options={(pages.data ?? []).map((page) => ({
                  value: page.id,
                  label: page.name,
                }))}
                placeholder={t("全部可访问文档", "All accessible documents")}
                disabled={!canManage || mutation.busy}
              />
            </Field>
            <Field
              label={t("工作项范围 ID", "Work item scope IDs")}
              hint={t(
                "每行一个 ID，或以逗号分隔。留空表示整个项目。",
                "One ID per line or comma-separated. Leave empty for the entire project.",
              )}
            >
              <textarea
                className="input automation-textarea"
                rows={3}
                value={itemIDs}
                onChange={(event) => {
                  setItemIDs(event.target.value);
                  setDirty(true);
                }}
              />
            </Field>
          </div>
          <details className="automation-evidence">
            <summary>
              {t(
                "查看与编辑精确来源和成员 ID",
                "View and edit exact source and member IDs",
              )}
            </summary>
            <div className="automation-form-grid">
              <Field label={t("成员 ID", "Member IDs")}>
                <textarea
                  className="input automation-textarea"
                  rows={3}
                  value={memberIDs}
                  onChange={(event) => {
                    setMemberIDs(event.target.value);
                    setDirty(true);
                  }}
                />
              </Field>
              <Field label={t("文档 ID", "Document IDs")}>
                <textarea
                  className="input automation-textarea"
                  rows={3}
                  value={pageIDs}
                  onChange={(event) => {
                    setPageIDs(event.target.value);
                    setDirty(true);
                  }}
                />
              </Field>
            </div>
          </details>
        </section>
        <section className="automation-card">
          <h3>{t("约束与预算", "Constraints and budgets")}</h3>
          <div className="automation-form-grid">
            <Field
              label={t("硬截止日", "Hard deadline")}
              hint={t(
                "预测和改排不能通过延后承诺来消除风险。",
                "Forecasts and rescheduling cannot remove risk by postponing commitments.",
              )}
            >
              <Input
                type="date"
                value={form.hard_deadline?.slice(0, 10) ?? ""}
                onChange={(event) =>
                  update("hard_deadline", event.target.value)
                }
              />
            </Field>
            <Field label={t("单批变更上限", "Maximum changes per batch")}>
              <Input
                type="number"
                required
                min={1}
                max={100}
                step={1}
                value={form.max_changes}
                onChange={(event) =>
                  update("max_changes", Number(event.target.value))
                }
              />
            </Field>
            <Field
              label={t("调用预算", "Run budget")}
              hint={t(
                `本月已使用 ${policy.calls_used ?? 0} 次。设为 0 将阻止消耗预算的新运行。`,
                `${policy.calls_used ?? 0} runs used this month. Zero prevents new runs that consume budget.`,
              )}
            >
              <Input
                type="number"
                required
                min={0}
                max={10000}
                step={1}
                value={form.budget_calls}
                onChange={(event) =>
                  update("budget_calls", Number(event.target.value))
                }
              />
            </Field>
            <Field
              label={t("最短运行间隔（秒）", "Minimum run interval (seconds)")}
            >
              <Input
                type="number"
                required
                min={0}
                max={86400}
                step={1}
                value={form.min_interval_seconds}
                onChange={(event) =>
                  update("min_interval_seconds", Number(event.target.value))
                }
              />
            </Field>
            <Field
              label={t(
                "连续自动调整轮次上限",
                "Maximum automatic adjustment rounds",
              )}
            >
              <Input
                type="number"
                required
                min={1}
                max={10}
                step={1}
                value={form.max_rounds}
                onChange={(event) =>
                  update("max_rounds", Number(event.target.value))
                }
              />
            </Field>
          </div>
        </section>
      </fieldset>
      {canManage && (
        <div className="automation-form-actions">
          <span className="text-muted" aria-live="polite">
            {dirty
              ? t("有未保存更改", "Unsaved changes")
              : t("当前策略已保存", "Current policy saved")}
          </span>
          <Button
            type="submit"
            variant="primary"
            busy={mutation.busy}
            disabled={!dirty}
          >
            {t("保存策略", "Save policy")}
          </Button>
        </div>
      )}
    </form>
  );
});
