import { useEffect, useState, type FormEvent } from "react";
import { observer } from "mobx-react-lite";
import { Plus } from "lucide-react";
import { appStore } from "../../stores/app-store";
import { useMutation, useScope } from "../../lib/hooks";
import { errorMessage } from "../../lib/api";
import { memberName, priorities } from "../../lib/utils";
import {
  Button,
  ErrorBox,
  Field,
  Input,
  Modal,
  MultiSelect,
  priorityLabels,
  Select,
} from "../../components/ui";
import { RichEditor } from "../../components/editor";
import type { Priority } from "../../types";

export const IssueForm = observer(function IssueForm({
  projectId,
  onClose,
  parentId,
  stateId,
}: {
  projectId: string | null;
  onClose: () => void;
  parentId?: string;
  stateId?: string;
}) {
  const { workspace } = useScope();
  const mutation = useMutation();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [descriptionJSON, setDescriptionJSON] = useState<unknown>({});
  const [priority, setPriority] = useState<Priority>("none");
  const [selectedState, setSelectedState] = useState(stateId ?? "");
  const [assignees, setAssignees] = useState<string[]>([]);
  const [labels, setLabels] = useState<string[]>([]);
  const [date, setDate] = useState("");
  const [createMore, setCreateMore] = useState(false);
  const [loadError, setLoadError] = useState("");
  const t = appStore.t;
  const states = appStore.states.get(projectId ?? "") ?? [];
  const projectLabels = appStore.labels.get(projectId ?? "") ?? [];
  const members = appStore.members.get(projectId ?? "") ?? [];
  const project = appStore.projects.get(projectId ?? "");

  useEffect(() => {
    if (!projectId) return;
    setLoadError("");
    appStore
      .loadProjectResources(workspace.id, projectId)
      .then(() => {
        const options = appStore.states.get(projectId) ?? [];
        setSelectedState(
          stateId ??
            options.find((state) => state.is_default)?.id ??
            options[0]?.id ??
            "",
        );
      })
      .catch((cause) => setLoadError(errorMessage(cause)));
  }, [projectId, workspace.id, stateId]);

  const reset = () => {
    setName("");
    setDescription("");
    setDescriptionJSON({});
    setAssignees([]);
    setLabels([]);
    setDate("");
    setPriority("none");
    mutation.setError("");
  };
  const close = () => {
    if (!mutation.busy) {
      reset();
      onClose();
    }
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const draft =
      (event.nativeEvent as SubmitEvent).submitter?.getAttribute("value") ===
      "draft";
    if (!projectId) return;
    const issue = await mutation.execute(() =>
      appStore.createIssue(workspace.id, projectId, {
        name: name.trim(),
        description_html: description,
        description_json: descriptionJSON,
        state_id: selectedState,
        priority,
        assignee_ids: assignees,
        label_ids: labels,
        target_date: date || null,
        parent_id: parentId ?? null,
        is_draft: draft,
      }),
    );
    if (issue) {
      appStore.notify(t("工作项已创建", "Work item created"));
      reset();
      if (!createMore) onClose();
    }
  };

  return (
    <Modal
      open={!!projectId}
      onOpenChange={(open) => {
        if (!open) close();
      }}
      title={
        parentId
          ? t("创建子工作项", "Create a sub-item")
          : t("创建工作项", "Create a work item")
      }
      description={
        project ? `${project.name} · ${project.identifier}` : undefined
      }
      className="issue-create-modal"
    >
      <form onSubmit={submit}>
        <div className="issue-form-content">
          <Input
            name="issue_name"
            className="issue-name-input"
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder={t("要做什么？", "What needs to be done?")}
            required
            maxLength={250}
            autoFocus
            aria-label={t("工作项标题", "Work item title")}
          />
          <RichEditor
            projectId={projectId ?? undefined}
            value={description}
            onChange={(html, json) => {
              setDescription(html);
              setDescriptionJSON(json);
            }}
            placeholder={t(
              "补充背景、验收标准或相关资料…",
              "Add context, acceptance criteria, or useful links…",
            )}
            compact
          />
          <div className="issue-form-fields">
            <Field label={t("状态", "State")}>
              <Select
                name="state_id"
                value={selectedState}
                onChange={(event) => setSelectedState(event.target.value)}
                required
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
                name="priority"
                value={priority}
                onChange={(event) =>
                  setPriority(event.target.value as Priority)
                }
              >
                {priorities.map((value) => (
                  <option key={value} value={value}>
                    {t(...priorityLabels[value])}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label={t("负责人", "Assignees")}>
              <MultiSelect
                value={assignees}
                onChange={setAssignees}
                options={members.map((member) => ({
                  value: member.user_id,
                  label: memberName(member),
                }))}
                placeholder={t("未分配", "Unassigned")}
              />
            </Field>
            <Field label={t("标签", "Labels")}>
              <MultiSelect
                value={labels}
                onChange={setLabels}
                options={projectLabels.map((label) => ({
                  value: label.id,
                  label: label.name,
                  color: label.color,
                }))}
                placeholder={t("添加标签", "Add labels")}
              />
            </Field>
            <Field label={t("目标日期", "Due date")}>
              <Input
                name="target_date"
                type="date"
                value={date}
                onChange={(event) => setDate(event.target.value)}
              />
            </Field>
          </div>
          <ErrorBox message={loadError || mutation.error} />
        </div>
        <div className="modal-footer">
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={createMore}
              onChange={(event) => setCreateMore(event.target.checked)}
            />
            {t("继续创建", "Create another")}
          </label>
          <span className="flex-spacer" />
          <Button type="button" onClick={close}>
            {t("取消", "Cancel")}
          </Button>
          <Button
            type="submit"
            value="draft"
            busy={mutation.busy}
            disabled={!selectedState || !name.trim()}
          >
            {t("存为草稿", "Save draft")}
          </Button>
          <Button
            type="submit"
            variant="primary"
            busy={mutation.busy}
            disabled={!selectedState || !name.trim()}
          >
            <Plus size={15} />
            {t("创建工作项", "Create work item")}
          </Button>
        </div>
      </form>
    </Modal>
  );
});
