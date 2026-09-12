import { useState, type FormEvent } from "react";
import { observer } from "mobx-react-lite";
import { Link } from "react-router-dom";
import { ArrowRight, Plus } from "lucide-react";
import { appStore } from "../../stores/app-store";
import { useScope } from "../../lib/hooks";
import {
  Button,
  ErrorBox,
  Field,
  Input,
  Modal,
  MultiSelect,
  Select,
} from "../../components/ui";
import { RichEditor } from "../../components/editor";
import { memberName } from "../../lib/utils";
import type { WorkItem } from "../../types";
import { hours, parseHours, splitMinutes } from "./semantics";
import { requirementsStore as store } from "./store";

export const RequirementEditor = observer(function RequirementEditor({
  item,
  seed = {},
  close,
  openItem,
  createChild,
}: {
  item?: WorkItem;
  seed?: Partial<WorkItem>;
  close: () => void;
  openItem: (id: string) => void;
  createChild: (parent: WorkItem) => void;
}) {
  const { workspace, project } = useScope();
  const canEdit = store.canEdit;
  const t = appStore.t;
  const initial = item ?? seed;
  const states = store.states;
  const members = store.members;
  const [form, setForm] = useState<Partial<WorkItem>>({
    name: "",
    requirement_type: "story",
    story_role: "",
    story_goal: "",
    story_benefit: "",
    acceptance_criteria: [],
    state_id:
      states.find((state) => state.is_default)?.id ?? states[0]?.id ?? "",
    priority: "none",
    parent_id: null,
    activity_id: null,
    cycle_id: null,
    assignee_ids: [],
    allocation_weights: [],
    required_skills: [],
    start_date: null,
    target_date: null,
    map_position:
      Math.max(
        0,
        ...store.snapshot.items.map(
          (entry) => entry.map_position ?? entry.position,
        ),
      ) + 65536,
    description_html: "",
    ...initial,
  });
  const [estimated, setEstimated] = useState(
    initial.estimated_minutes == null ? "" : hours(initial.estimated_minutes),
  );
  const [remaining, setRemaining] = useState(
    initial.remaining_minutes == null ? "" : hours(initial.remaining_minutes),
  );
  const [requiredSkills, setRequiredSkills] = useState(
    (initial.required_skills ?? []).join(", "),
  );
  const [error, setError] = useState("");
  const update = <K extends keyof WorkItem>(key: K, value: WorkItem[K]) =>
    setForm((old) => ({ ...old, [key]: value }));
  const type = form.requirement_type;
  const parents = store.snapshot.items.filter(
    (candidate) =>
      !candidate.archived_at &&
      candidate.id !== item?.id &&
      (type === "story"
        ? candidate.requirement_type === "epic"
        : type === "task"
          ? ["story", "task"].includes(candidate.requirement_type ?? "")
          : type == null),
  );
  const parent = store.items.get(form.parent_id ?? "");
  const activities = store.snapshot.activities.filter(
    (activity) =>
      !activity.archived_at && (activity.epic_id ?? "") === (parent?.id ?? ""),
  );
  const children = store.snapshot.items.filter(
    (child) => child.parent_id === item?.id && !!item,
  );
  const allocations = splitMinutes(
    parseHours(remaining) ?? 0,
    form.assignee_ids ?? [],
    form.allocation_weights,
  );
  const save = async (event: FormEvent) => {
    event.preventDefault();
    setError("");
    if (!form.name?.trim()) {
      setError(t("请填写标题", "A title is required"));
      return;
    }
    if (
      form.start_date &&
      form.target_date &&
      form.start_date > form.target_date
    ) {
      setError(
        t("结束日期不能早于开始日期", "End date cannot precede start date"),
      );
      return;
    }
    const estimatedMinutes = parseHours(estimated),
      remainingMinutes = parseHours(remaining);
    if (
      [estimatedMinutes, remainingMinutes].some(
        (value) => value !== null && (!Number.isFinite(value) || value < 0),
      )
    ) {
      setError(
        t("工时必须是非负数字或留空", "Hours must be nonnegative or empty"),
      );
      return;
    }
    const fields: Partial<WorkItem> = {
      name: form.name.trim(),
      requirement_type: form.requirement_type,
      state_id: form.state_id,
      priority: form.priority,
      parent_id: type === "epic" ? null : form.parent_id,
      activity_id: type === "story" ? form.activity_id : null,
      cycle_id: form.cycle_id,
      map_position: form.map_position,
      story_role: form.story_role,
      story_goal: form.story_goal,
      story_benefit: form.story_benefit,
      acceptance_criteria: (form.acceptance_criteria ?? [])
        .map((criterion) => criterion.trim())
        .filter(Boolean),
      assignee_ids: form.assignee_ids,
      allocation_weights: (form.assignee_ids ?? []).map((member_id) => ({
        member_id,
        weight:
          form.allocation_weights?.find(
            (entry) => entry.member_id === member_id,
          )?.weight ?? 1,
      })),
      estimated_minutes: estimatedMinutes,
      remaining_minutes: remainingMinutes,
      required_skills: [
        ...new Set(
          requiredSkills
            .split(/[,，]/)
            .map((value) => value.trim())
            .filter(Boolean),
        ),
      ],
      start_date: form.start_date,
      target_date: form.target_date,
      planning_locked: form.planning_locked ?? false,
      description_html: form.description_html,
      description_json: form.description_json,
    };
    if (await (item ? store.update(item, fields) : store.create(fields)))
      close();
  };
  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
      title={
        item
          ? `${project?.identifier}-${item.sequence_id} · ${t("需求详情", "Requirement details")}`
          : t("新建需求", "Create requirement")
      }
      className="req-detail-modal"
    >
      <form onSubmit={save} className="req-form">
        <ErrorBox message={error || store.error} />
        {item && store.items.get(item.id)?.version !== item.version && (
          <p role="status" className="req-warning">
            {t(
              "此需求已在其他会话更新。保存时将检查版本冲突。",
              "This requirement changed in another session. Saving will check its version.",
            )}
          </p>
        )}
        <Field label={t("标题", "Title")}>
          <Input
            autoFocus
            required
            value={form.name ?? ""}
            onChange={(event) => update("name", event.target.value)}
            disabled={!canEdit}
          />
        </Field>
        <div className="req-form-grid">
          <Field label={t("需求类型", "Requirement type")}>
            <Select
              value={type ?? ""}
              disabled={!canEdit}
              onChange={(event) =>
                setForm((old) => ({
                  ...old,
                  requirement_type: (event.target.value ||
                    null) as WorkItem["requirement_type"],
                  parent_id: null,
                  activity_id: null,
                }))
              }
            >
              <option value="">
                {t("未分类工作项", "Unclassified work item")}
              </option>
              <option value="epic">Epic</option>
              <option value="story">Story</option>
              <option value="task">Task</option>
            </Select>
          </Field>
          <Field label={t("父级", "Parent")}>
            <Select
              value={form.parent_id ?? ""}
              disabled={!canEdit || type === "epic"}
              onChange={(event) =>
                setForm((old) => ({
                  ...old,
                  parent_id: event.target.value || null,
                  activity_id: null,
                }))
              }
            >
              <option value="">{t("未分配", "Unassigned")}</option>
              {parents.map((parent) => (
                <option key={parent.id} value={parent.id}>
                  {parent.name}
                </option>
              ))}
            </Select>
          </Field>
          <Field label={t("状态", "Status")}>
            <Select
              value={form.state_id}
              disabled={!canEdit}
              onChange={(event) => update("state_id", event.target.value)}
            >
              {states.map((state) => (
                <option key={state.id} value={state.id}>
                  {state.name}
                </option>
              ))}
            </Select>
          </Field>
          <Field label={t("优先级", "Priority")}>
            <Select
              value={form.priority}
              disabled={!canEdit}
              onChange={(event) =>
                update("priority", event.target.value as WorkItem["priority"])
              }
            >
              {[
                ["none", "无", "None"],
                ["low", "低", "Low"],
                ["medium", "中", "Medium"],
                ["high", "高", "High"],
                ["urgent", "紧急", "Urgent"],
              ].map(([value, zh, en]) => (
                <option key={value} value={value}>
                  {t(zh, en)}
                </option>
              ))}
            </Select>
          </Field>
          <Field
            label={
              type === "story"
                ? t("承诺 Sprint", "Commitment sprint")
                : t("执行 Sprint", "Execution sprint")
            }
          >
            <Select
              value={form.cycle_id ?? ""}
              disabled={!canEdit}
              onChange={(event) =>
                update("cycle_id", event.target.value || null)
              }
            >
              <option value="">Backlog</option>
              {store.snapshot.cycles
                .filter((cycle) => !cycle.archived_at)
                .map((cycle) => (
                  <option key={cycle.id} value={cycle.id}>
                    {cycle.name}
                  </option>
                ))}
            </Select>
          </Field>
          {type === "story" && (
            <Field label={t("用户活动", "User activity")}>
              <Select
                value={form.activity_id ?? ""}
                disabled={!canEdit}
                onChange={(event) =>
                  update("activity_id", event.target.value || null)
                }
              >
                <option value="">
                  {t("未分配活动", "Unassigned activity")}
                </option>
                {activities.map((activity) => (
                  <option key={activity.id} value={activity.id}>
                    {activity.name}
                  </option>
                ))}
              </Select>
            </Field>
          )}
          <Field
            label={
              type === "story"
                ? t("承诺开始日期", "Commitment start")
                : t("开始日期", "Start date")
            }
          >
            <Input
              type="date"
              value={form.start_date?.slice(0, 10) ?? ""}
              disabled={!canEdit}
              onChange={(event) =>
                update("start_date", event.target.value || null)
              }
            />
          </Field>
          <Field
            label={
              type === "story"
                ? t("承诺结束日期", "Commitment end")
                : t("结束日期", "End date")
            }
          >
            <Input
              type="date"
              value={form.target_date?.slice(0, 10) ?? ""}
              disabled={!canEdit}
              onChange={(event) =>
                update("target_date", event.target.value || null)
              }
            />
          </Field>
        </div>
        {type === "story" && (
          <fieldset className="req-fieldset">
            <legend>{t("用户故事与验收", "User story and acceptance")}</legend>
            <Field label={t("作为（业务角色）", "As a (business role)")}>
              <Input
                value={form.story_role ?? ""}
                disabled={!canEdit}
                onChange={(event) => update("story_role", event.target.value)}
              />
            </Field>
            <Field label={t("我希望（目标）", "I want to (goal)")}>
              <Input
                value={form.story_goal ?? ""}
                disabled={!canEdit}
                onChange={(event) => update("story_goal", event.target.value)}
              />
            </Field>
            <Field label={t("以便（业务价值）", "So that (business value)")}>
              <Input
                value={form.story_benefit ?? ""}
                disabled={!canEdit}
                onChange={(event) =>
                  update("story_benefit", event.target.value)
                }
              />
            </Field>
            <Field
              label={t(
                "验收条件（每行一条）",
                "Acceptance criteria (one per line)",
              )}
            >
              <textarea
                className="input"
                rows={4}
                value={(form.acceptance_criteria ?? []).join("\n")}
                disabled={!canEdit}
                onChange={(event) =>
                  update("acceptance_criteria", event.target.value.split("\n"))
                }
              />
            </Field>
          </fieldset>
        )}
        <fieldset className="req-fieldset">
          <legend>{t("负责人和工作量", "Assignment and effort")}</legend>
          <MultiSelect
            value={form.assignee_ids ?? []}
            disabled={!canEdit}
            onChange={(ids) => update("assignee_ids", ids)}
            placeholder={t("负责人", "Assignees")}
            options={members.map((member) => ({
              value: member.user_id,
              label: memberName(member),
            }))}
          />
          <div className="req-form-grid">
            <Field
              label={t("预计工时（小时）", "Estimated effort (hours)")}
              hint={t(
                "留空表示未知；保存为整数分钟",
                "Empty means unknown; stored as integer minutes",
              )}
            >
              <Input
                type="number"
                step="any"
                min="0"
                value={estimated}
                disabled={!canEdit}
                onChange={(event) => setEstimated(event.target.value)}
              />
            </Field>
            <Field label={t("剩余工时（小时）", "Remaining effort (hours)")}>
              <Input
                type="number"
                step="any"
                min="0"
                value={remaining}
                disabled={!canEdit}
                onChange={(event) => setRemaining(event.target.value)}
              />
            </Field>
          </div>
          {(form.assignee_ids ?? []).map((id) => (
            <div className="req-allocation" key={id}>
              <span>
                {memberName(
                  members.find((member) => member.user_id === id) ?? {},
                )}
              </span>
              <Field label={t("分摊权重", "Allocation weight")}>
                <Input
                  type="number"
                  min="1"
                  step="1"
                  disabled={!canEdit}
                  value={
                    form.allocation_weights?.find(
                      (entry) => entry.member_id === id,
                    )?.weight ?? 1
                  }
                  onChange={(event) =>
                    update("allocation_weights", [
                      ...(form.allocation_weights ?? []).filter(
                        (entry) => entry.member_id !== id,
                      ),
                      { member_id: id, weight: Number(event.target.value) },
                    ])
                  }
                />
              </Field>
              <span>
                {remaining === ""
                  ? "?"
                  : allocations.find((entry) => entry.member_id === id)
                      ?.minutes}{" "}
                {t("分钟", "min")}
              </span>
            </div>
          ))}
          <Field
            label={t(
              "所需技能（逗号分隔）",
              "Required skills (comma separated)",
            )}
          >
            <Input
              value={requiredSkills}
              disabled={!canEdit}
              onChange={(event) => setRequiredSkills(event.target.value)}
            />
          </Field>
          <label className="req-check">
            <input
              type="checkbox"
              checked={form.planning_locked ?? false}
              disabled={!canEdit}
              onChange={(event) =>
                update("planning_locked", event.target.checked)
              }
            />
            {t(
              "锁定人工安排，自动排程不得修改",
              "Lock manual scheduling against automation",
            )}
          </label>
        </fieldset>
        <Field label={t("描述", "Description")}>
          <RichEditor
            value={form.description_html ?? ""}
            editable={canEdit}
            projectId={project!.id}
            workItemId={item?.id}
            onChange={(html, json) =>
              setForm((old) => ({
                ...old,
                description_html: html,
                description_json: json,
              }))
            }
          />
        </Field>
        {item && (
          <fieldset className="req-fieldset">
            <legend>{t("子任务与关联", "Children and context")}</legend>
            {children.map((child) => (
              <button
                key={child.id}
                type="button"
                className="req-child"
                onClick={() => openItem(child.id)}
              >
                <span>{child.name}</span>
                <ArrowRight size={14} />
              </button>
            ))}
            {canEdit && item.requirement_type !== "epic" && (
              <Button type="button" size="sm" onClick={() => createChild(item)}>
                <Plus size={14} />
                {t("创建子任务", "Add task")}
              </Button>
            )}
            <Link
              className="req-inline-link"
              to={`/w/${workspace.slug}/projects/${project!.id}/issues/${item.id}`}
            >
              {t(
                "打开评论、附件和完整活动记录",
                "Open comments, attachments and activity",
              )}
            </Link>
          </fieldset>
        )}
        <div className="req-form-footer">
          {item && canEdit && (
            <Button
              type="button"
              variant="danger"
              disabled={store.busy}
              onClick={async () => {
                if (
                  window.confirm(
                    t(
                      "归档此需求？子项会保留。",
                      "Archive this requirement? Child items remain.",
                    ),
                  ) &&
                  (await store.update(item, {
                    archived_at: new Date().toISOString(),
                  }))
                )
                  close();
              }}
            >
              {t("归档", "Archive")}
            </Button>
          )}
          <span className="req-spacer" />
          <Button type="button" onClick={close}>
            {t("关闭", "Close")}
          </Button>
          {canEdit && (
            <Button type="submit" variant="primary" busy={store.busy}>
              {t("保存需求", "Save requirement")}
            </Button>
          )}
        </div>
      </form>
    </Modal>
  );
});
