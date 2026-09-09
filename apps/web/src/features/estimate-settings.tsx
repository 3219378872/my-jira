import { useEffect, useState, type FormEvent } from "react";
import { observer } from "mobx-react-lite";
import { Plus, Settings2, Trash2 } from "lucide-react";
import { appStore } from "../stores/app-store";
import { api, projectPath } from "../lib/api";
import { useMutation, useRemote, useScope } from "../lib/hooks";
import {
  Badge,
  Button,
  Confirm,
  ErrorBox,
  Field,
  Input,
  Loading,
  Menu,
  Modal,
  Select,
} from "../components/ui";
import type { EstimatePoint, EstimateScheme } from "../types";

type EstimateTemplate = Omit<EstimateScheme, "description" | "points"> & {
  points: Omit<EstimatePoint, "id">[];
};

export const EstimateSettings = observer(function EstimateSettings() {
  const { workspace, project } = useScope();
  const base = projectPath(workspace.id, project!.id);
  const schemes = useRemote<EstimateScheme[]>(`${base}/estimates`);
  const active = useRemote<{
    estimate_id: string | null;
    estimate: EstimateScheme | null;
  }>(`${base}/estimate-settings`);
  const templates = useRemote<EstimateTemplate[]>(`${base}/estimate-templates`);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<EstimateScheme | null>(null);
  const [form, setForm] = useState({
    name: "",
    description: "",
    template: "fibonacci",
    kind: "points",
  });
  const [points, setPoints] = useState<Omit<EstimatePoint, "id">[]>([]);
  const [pointEditor, setPointEditor] = useState<{
    scheme: EstimateScheme;
    point: EstimatePoint | null;
  } | null>(null);
  const [pointForm, setPointForm] = useState({
    label: "",
    value: "",
    position: 1024,
  });
  const [deleting, setDeleting] = useState<EstimateScheme | null>(null);
  const [deletingPoint, setDeletingPoint] = useState<{
    scheme: EstimateScheme;
    point: EstimatePoint;
  } | null>(null);
  const [replacement, setReplacement] = useState("");
  const mutation = useMutation();
  const t = appStore.t;
  const refresh = () => {
    schemes.refresh();
    active.refresh();
  };
  const start = (scheme?: EstimateScheme) => {
    setEditing(scheme ?? null);
    const template = templates.data?.[0];
    setForm({
      name: scheme?.name ?? template?.name ?? "",
      description: scheme?.description ?? "",
      template: template?.id ?? "fibonacci",
      kind: scheme?.kind ?? template?.kind ?? "points",
    });
    setPoints(template?.points ?? []);
    setOpen(true);
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    mutation.execute(
      async () => {
        if (editing)
          await api.patch(`${base}/estimates/${editing.id}`, {
            name: form.name,
            description: form.description,
          });
        else
          await api.post(`${base}/estimates`, {
            name: form.name,
            description: form.description,
            kind: form.kind,
            points,
          });
        setOpen(false);
        refresh();
      },
      t("估算方案已保存", "Estimate scheme saved"),
    );
  };
  return (
    <section className="settings-section">
      <h1>{t("估算方案", "Estimates")}</h1>
      <p className="page-description">
        {t(
          "用团队共同理解的尺度评估工作量。",
          "Estimate work using a scale your team understands.",
        )}
      </p>
      <ErrorBox
        message={
          schemes.error || active.error || templates.error || mutation.error
        }
      />
      <Field label={t("当前使用的方案", "Active estimate scheme")}>
        <Select
          value={active.data?.estimate_id ?? ""}
          disabled={mutation.busy}
          onChange={(event) =>
            mutation.execute(async () => {
              await api.patch(`${base}/estimate-settings`, {
                estimate_id: event.target.value || null,
              });
              refresh();
            })
          }
        >
          <option value="">
            {t("不使用方案（自由数值）", "No scheme (free numeric values)")}
          </option>
          {schemes.data?.map((scheme) => (
            <option key={scheme.id} value={scheme.id}>
              {scheme.name}
            </option>
          ))}
        </Select>
      </Field>
      <div className="settings-toolbar">
        <span className="flex-spacer" />
        <Button variant="primary" onClick={() => start()}>
          <Plus size={14} />
          {t("添加估算方案", "Add estimate scheme")}
        </Button>
      </div>
      {schemes.loading ? (
        <Loading />
      ) : (
        schemes.data?.map((scheme) => (
          <section className="settings-panel" key={scheme.id}>
            <div className="settings-panel-heading">
              <h2>{scheme.name}</h2>
              <Badge>
                {scheme.kind === "points"
                  ? t("点数", "Points")
                  : t("分类", "Categories")}
              </Badge>
              <span className="flex-spacer" />
              <Menu
                items={[
                  {
                    label: t("编辑方案", "Edit scheme"),
                    onSelect: () => start(scheme),
                  },
                  {
                    label: t("删除方案", "Delete scheme"),
                    danger: true,
                    onSelect: () => setDeleting(scheme),
                  },
                ]}
              />
            </div>
            <div className="settings-panel-body">
              <p className="settings-note">{scheme.description}</p>
              {scheme.points.map((point) => (
                <div className="catalog-row" key={point.id}>
                  <strong>{point.label}</strong>
                  <span>{point.numeric_value ?? "—"}</span>
                  <span className="flex-spacer" />
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={t(`编辑 ${point.label}`, `Edit ${point.label}`)}
                    onClick={() => {
                      setPointEditor({ scheme, point });
                      setPointForm({
                        label: point.label,
                        value: String(point.numeric_value ?? ""),
                        position: point.position,
                      });
                    }}
                  >
                    <Settings2 size={13} />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={t(
                      `删除 ${point.label}`,
                      `Delete ${point.label}`,
                    )}
                    onClick={() => {
                      setDeletingPoint({ scheme, point });
                      setReplacement("");
                    }}
                  >
                    <Trash2 size={13} />
                  </Button>
                </div>
              ))}
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setPointEditor({ scheme, point: null });
                  setPointForm({
                    label: "",
                    value: "",
                    position: (scheme.points.at(-1)?.position ?? 0) + 1024,
                  });
                }}
              >
                <Plus size={13} />
                {t("添加估算值", "Add estimate value")}
              </Button>
            </div>
          </section>
        ))
      )}
      <Modal
        open={open}
        onOpenChange={setOpen}
        title={
          editing
            ? t("编辑估算方案", "Edit estimate scheme")
            : t("创建估算方案", "Create estimate scheme")
        }
      >
        <form className="form-stack modal-body" onSubmit={submit}>
          <Field label={t("名称", "Name")}>
            <Input
              value={form.name}
              onChange={(event) =>
                setForm({ ...form, name: event.target.value })
              }
              required
              autoFocus
            />
          </Field>
          <Field label={t("说明", "Description")}>
            <Input
              value={form.description}
              onChange={(event) =>
                setForm({ ...form, description: event.target.value })
              }
            />
          </Field>
          {!editing && (
            <>
              <Field label={t("起始模板", "Starting template")}>
                <Select
                  value={form.template}
                  onChange={(event) => {
                    const template = templates.data?.find(
                      (item) => item.id === event.target.value,
                    );
                    if (template) {
                      setForm({
                        ...form,
                        template: template.id,
                        kind: template.kind,
                        name: template.name,
                      });
                      setPoints(template.points);
                    }
                  }}
                >
                  {templates.data?.map((template) => (
                    <option key={template.id} value={template.id}>
                      {template.name}
                    </option>
                  ))}
                </Select>
              </Field>
              <div className="estimate-draft-points">
                {points.map((point, index) => (
                  <div className="inline-actions" key={index}>
                    <Input
                      aria-label={t("估算标签", "Estimate label")}
                      value={point.label}
                      maxLength={20}
                      onChange={(event) =>
                        setPoints(
                          points.map((entry, position) =>
                            position === index
                              ? { ...entry, label: event.target.value }
                              : entry,
                          ),
                        )
                      }
                      required
                    />
                    {form.kind === "points" && (
                      <Input
                        type="number"
                        min={0}
                        step="any"
                        aria-label={t("数值", "Numeric value")}
                        value={point.numeric_value ?? ""}
                        onChange={(event) =>
                          setPoints(
                            points.map((entry, position) =>
                              position === index
                                ? {
                                    ...entry,
                                    numeric_value: Number(event.target.value),
                                  }
                                : entry,
                            ),
                          )
                        }
                        required
                      />
                    )}
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      aria-label={t("移除估算值", "Remove estimate value")}
                      disabled={points.length === 1}
                      onClick={() =>
                        setPoints(
                          points.filter((_, position) => position !== index),
                        )
                      }
                    >
                      <Trash2 size={13} />
                    </Button>
                  </div>
                ))}
                <Button
                  type="button"
                  size="sm"
                  onClick={() =>
                    setPoints([
                      ...points,
                      {
                        label: "",
                        numeric_value: form.kind === "points" ? 0 : null,
                        position: (points.length + 1) * 1024,
                      },
                    ])
                  }
                >
                  <Plus size={13} />
                  {t("添加值", "Add value")}
                </Button>
              </div>
            </>
          )}
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button
              type="submit"
              variant="primary"
              busy={mutation.busy}
              disabled={!editing && points.length === 0}
            >
              {t("保存", "Save")}
            </Button>
          </div>
        </form>
      </Modal>
      <Modal
        open={!!pointEditor}
        onOpenChange={(value) => {
          if (!value) setPointEditor(null);
        }}
        title={t("估算值", "Estimate value")}
      >
        <form
          className="form-stack modal-body"
          onSubmit={(event) => {
            event.preventDefault();
            mutation.execute(async () => {
              const collection = `${base}/estimates/${pointEditor!.scheme.id}/points`;
              const body = {
                label: pointForm.label,
                numeric_value:
                  pointEditor!.scheme.kind === "points"
                    ? Number(pointForm.value)
                    : null,
                position: Number(pointForm.position),
              };
              if (pointEditor?.point)
                await api.patch(`${collection}/${pointEditor.point.id}`, body);
              else await api.post(collection, body);
              setPointEditor(null);
              refresh();
            });
          }}
        >
          <Field label={t("标签", "Label")}>
            <Input
              value={pointForm.label}
              onChange={(event) =>
                setPointForm({ ...pointForm, label: event.target.value })
              }
              required
              maxLength={20}
            />
          </Field>
          {pointEditor?.scheme.kind === "points" && (
            <Field label={t("数值", "Numeric value")}>
              <Input
                type="number"
                min={0}
                step="any"
                value={pointForm.value}
                onChange={(event) =>
                  setPointForm({ ...pointForm, value: event.target.value })
                }
                required
              />
            </Field>
          )}
          <Field label={t("排序位置", "Sort position")}>
            <Input
              type="number"
              value={pointForm.position}
              onChange={(event) =>
                setPointForm({
                  ...pointForm,
                  position: Number(event.target.value),
                })
              }
              required
            />
          </Field>
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button type="submit" variant="primary" busy={mutation.busy}>
              {t("保存", "Save")}
            </Button>
          </div>
        </form>
      </Modal>
      <Confirm
        open={!!deleting}
        onOpenChange={(value) => {
          if (!value) setDeleting(null);
        }}
        title={t("删除估算方案？", "Delete this estimate scheme?")}
        description={t(
          "关联工作项的此方案估算值将被清空。",
          "Estimates using this scheme will be cleared from work items.",
        )}
        busy={mutation.busy}
        onConfirm={() =>
          mutation.execute(async () => {
            await api.delete(`${base}/estimates/${deleting!.id}`);
            setDeleting(null);
            refresh();
          })
        }
      />
      <Modal
        open={!!deletingPoint}
        onOpenChange={(value) => {
          if (!value) setDeletingPoint(null);
        }}
        title={t("移除估算值", "Remove estimate value")}
      >
        <div className="form-stack modal-body">
          <Field
            label={t("现有工作项改用", "Replace on existing work items with")}
          >
            <Select
              value={replacement}
              onChange={(event) => setReplacement(event.target.value)}
            >
              <option value="">{t("清空估算", "Clear estimate")}</option>
              {deletingPoint?.scheme.points
                .filter((point) => point.id !== deletingPoint.point.id)
                .map((point) => (
                  <option key={point.id} value={point.id}>
                    {point.label}
                  </option>
                ))}
            </Select>
          </Field>
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button
              variant="danger"
              busy={mutation.busy}
              onClick={() =>
                mutation.execute(async () => {
                  await api.delete(
                    `${base}/estimates/${deletingPoint!.scheme.id}/points/${deletingPoint!.point.id}${replacement ? `?replacement_id=${replacement}` : ""}`,
                  );
                  setDeletingPoint(null);
                  refresh();
                })
              }
            >
              {t("移除", "Remove")}
            </Button>
          </div>
        </div>
      </Modal>
    </section>
  );
});

export const AutomationSettings = observer(function AutomationSettings() {
  const { workspace, project } = useScope();
  const base = `${projectPath(workspace.id, project!.id)}/automation`;
  const current = useRemote<{
    archive_after_months: number;
    close_after_months: number;
    close_state_id: string | null;
  }>(base);
  const [form, setForm] = useState({
    archive_after_months: 0,
    close_after_months: 0,
    close_state_id: null as string | null,
  });
  const [runOpen, setRunOpen] = useState(false);
  const [result, setResult] = useState<{
    archived: number;
    closed: number;
  } | null>(null);
  const mutation = useMutation();
  const t = appStore.t;
  useEffect(() => {
    if (current.data) setForm(current.data);
  }, [current.data]);
  useEffect(() => {
    appStore.loadProjectResources(workspace.id, project!.id).catch(() => {});
  }, [workspace.id, project!.id]);
  const states = (appStore.states.get(project!.id) ?? []).filter((state) =>
    ["completed", "cancelled"].includes(state.group),
  );
  return (
    <section className="settings-section">
      <h1>{t("自动化", "Automation")}</h1>
      <p className="page-description">
        {t(
          "按工作项最后更新的时间自动整理长期未变化的工作。",
          "Automatically organize work based on its last update.",
        )}
      </p>
      <ErrorBox message={current.error || mutation.error} />
      {current.loading ? (
        <Loading />
      ) : (
        <form
          className="form-stack"
          onSubmit={(event) => {
            event.preventDefault();
            mutation.execute(
              async () => {
                await api.patch(base, form);
                current.refresh();
              },
              t("自动化设置已保存", "Automation settings saved"),
            );
          }}
        >
          {(
            [
              {
                key: "archive_after_months",
                zh: "自动归档已完成工作项",
                en: "Archive completed work items",
              },
              {
                key: "close_after_months",
                zh: "自动关闭不活跃工作项",
                en: "Close inactive work items",
              },
            ] as const
          ).map((setting) => (
            <Field key={setting.key} label={t(setting.zh, setting.en)}>
              <Select
                value={form[setting.key]}
                onChange={(event) =>
                  setForm({
                    ...form,
                    [setting.key]: Number(event.target.value),
                  })
                }
              >
                <option value={0}>{t("不自动处理", "Disabled")}</option>
                {Array.from({ length: 12 }, (_, index) => index + 1).map(
                  (months) => (
                    <option key={months} value={months}>
                      {t(
                        `超过 ${months} 个月未更新`,
                        `Not updated for ${months} months`,
                      )}
                    </option>
                  ),
                )}
              </Select>
            </Field>
          ))}
          <Field label={t("关闭时使用的状态", "State for closed work items")}>
            <Select
              value={form.close_state_id ?? ""}
              onChange={(event) =>
                setForm({ ...form, close_state_id: event.target.value || null })
              }
            >
              <option value="">
                {t("项目默认已取消状态", "Default cancelled state")}
              </option>
              {states.map((state) => (
                <option key={state.id} value={state.id}>
                  {state.name}
                </option>
              ))}
            </Select>
          </Field>
          <div className="settings-actions">
            <Button type="submit" variant="primary" busy={mutation.busy}>
              {t("保存规则", "Save rules")}
            </Button>
            <Button type="button" onClick={() => setRunOpen(true)}>
              {t("立即运行已保存规则", "Run saved rules now")}
            </Button>
          </div>
          {result && (
            <p className="settings-note">
              {t(
                `已归档 ${result.archived} 个工作项，已关闭 ${result.closed} 个工作项。`,
                `Archived ${result.archived} and closed ${result.closed} work items.`,
              )}
            </p>
          )}
        </form>
      )}
      <Confirm
        open={runOpen}
        onOpenChange={setRunOpen}
        title={t("现在运行自动化规则？", "Run automation rules now?")}
        description={t(
          "符合已保存规则的工作项会被归档或关闭。",
          "Matching work items will be archived or closed using the saved rules.",
        )}
        busy={mutation.busy}
        onConfirm={() =>
          mutation.execute(async () => {
            const response = await api.post<{
              archived: number;
              closed: number;
            }>(`${base}/run`);
            setResult(response.data);
            setRunOpen(false);
          })
        }
      />
    </section>
  );
});
