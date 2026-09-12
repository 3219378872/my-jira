import { useEffect, useRef, useState } from "react";
import { observer } from "mobx-react-lite";
import DOMPurify from "dompurify";
import {
  ArrowDown,
  ArrowUp,
  Copy,
  Download,
  Maximize,
  Minus,
  Plus,
  Save,
  X,
} from "lucide-react";
import { appStore } from "../../stores/app-store";
import { api, errorMessage } from "../../lib/api";
import { useRemote, useScope } from "../../lib/hooks";
import {
  Button,
  ErrorBox,
  Field,
  Input,
  Loading,
  Modal,
  MultiSelect,
  Select,
} from "../../components/ui";
import type { PageDocument } from "../../types";
import { requirementsStore as store } from "./store";
import { storyOf } from "./semantics";
import type {
  Scenario,
  ScenarioParticipant,
  ScenarioRelationship,
  ScenarioStep,
} from "./types";

export const ScenariosView = observer(function ScenariosView({
  openItem,
}: {
  openItem: (id: string) => void;
}) {
  const t = appStore.t;
  const canEdit = store.canEdit;
  const [selected, setSelected] = useState("");
  const [mode, setMode] = useState<"use-case" | "sequence">("use-case");
  const [edit, setEdit] = useState<{
    item?: Scenario;
    storyID?: string;
    stepID?: string;
  } | null>(null);
  const [version, setVersion] = useState("");
  const visibleIDs = new Set(
    store.visibleItems
      .map((item) => storyOf(item, store.items))
      .filter((id): id is string => !!id),
  );
  const selectedStory = store.selected
    ? storyOf(store.selected, store.items)
    : undefined;
  const scenarios = store.scenarios.filter((scenario) =>
    visibleIDs.has(scenario.story_id),
  );
  const current =
    scenarios.find((scenario) => scenario.id === selected) ??
    scenarios.find((scenario) => scenario.story_id === selectedStory) ??
    scenarios[0];
  const versions = useRemote<
    { id: string; version: number; created_at: string }[]
  >(
    current ? `${store.base}/scenarios/${current.id}/versions` : null,
    store.snapshot.revision,
  );
  useEffect(() => {
    setVersion("");
  }, [current?.id]);
  const path =
    mode === "sequence"
      ? current
        ? `${store.base}/scenarios/${current.id}/sequence.svg${version ? `?version=${version}` : ""}`
        : null
      : current && version
        ? `${store.base}/scenarios/${current.id}/use-case.svg?version=${version}`
        : `${store.base}/scenarios/use-case.svg?story_ids=${[...visibleIDs].sort().join(",")}`;
  return (
    <section
      className="req-scenarios"
      aria-label={t("业务场景与 UML", "Business scenarios and UML")}
    >
      <div className="req-view-heading">
        <div>
          <h2>
            {t(
              "从结构化场景理解业务交互",
              "Understand interactions through structured scenarios",
            )}
          </h2>
          <p>
            {t(
              "业务角色与登录角色分别维护。图中的关系和消息来自明确录入的场景。",
              "Business actors are separate from login roles. Diagram relationships and messages come from explicit scenario data.",
            )}
          </p>
        </div>
        {canEdit && (
          <Button
            size="sm"
            variant="primary"
            onClick={() => setEdit({ storyID: selectedStory })}
          >
            <Plus size={14} />
            {t("创建业务场景", "Create business scenario")}
          </Button>
        )}
      </div>
      <div className="req-scenario-layout">
        <aside className="req-scenario-list">
          <h3>
            {t("当前可见场景", "Visible scenarios")} ({scenarios.length})
          </h3>
          {scenarios.map((scenario) => (
            <button
              key={scenario.id}
              className={`req-scenario-list-item ${current?.id === scenario.id ? "is-selected" : ""}`}
              onClick={() => {
                setSelected(scenario.id);
                store.select(scenario.story_id);
              }}
            >
              <strong>{scenario.name}</strong>
              <span>
                {scenario.story.name} · {scenario.story.state_name}
              </span>
              {scenario.review_needed && (
                <small className="req-warning">
                  {t("故事已变化，待核对", "Story changed; review needed")}
                </small>
              )}
            </button>
          ))}
          {!scenarios.length && (
            <p className="req-empty-inline">
              {t(
                "选择 Story 创建第一个业务场景。",
                "Select a story to create its first business scenario.",
              )}
            </p>
          )}
        </aside>
        <div className="req-scenario-main">
          <div className="req-actions req-diagram-tabs">
            <Button
              variant={mode === "use-case" ? "primary" : "secondary"}
              onClick={() => setMode("use-case")}
            >
              {t("项目用例图", "Project use cases")}
            </Button>
            <Button
              variant={mode === "sequence" ? "primary" : "secondary"}
              disabled={!current}
              onClick={() => setMode("sequence")}
            >
              {t("场景时序图", "Scenario sequence")}
            </Button>
            {current && (
              <Select
                aria-label={t("场景版本", "Scenario version")}
                value={version}
                onChange={(event) => setVersion(event.target.value)}
              >
                <option value="">
                  {t("当前版本", "Current version")} · v{current.version}
                </option>
                {versions.data?.map((entry) => (
                  <option value={entry.version} key={entry.id}>
                    v{entry.version} · {entry.created_at.slice(0, 10)}
                  </option>
                ))}
              </Select>
            )}
          </div>
          {current && (
            <div className="req-scenario-summary">
              <div>
                <strong>{current.name}</strong>
                <p>{current.goal}</p>
                <small>
                  {t("主 Story", "Main story")}:{" "}
                  <button
                    className="req-inline-link"
                    onClick={() => openItem(current.story_id)}
                  >
                    {current.story.name}
                  </button>{" "}
                  · {current.story.state_name} · v{current.version}
                </small>
              </div>
              {canEdit && (
                <div className="req-actions">
                  <Button size="sm" onClick={() => setEdit({ item: current })}>
                    {t("编辑结构化场景", "Edit structured scenario")}
                  </Button>
                  <Button
                    size="icon"
                    aria-label={t("复制场景", "Copy scenario")}
                    disabled={store.busy}
                    onClick={() =>
                      void store.mutate(() =>
                        api.post(`${store.base}/scenarios/${current.id}/copy`, {
                          version: current.version,
                          name: `${current.name} ${t("副本", "copy")}`,
                        }),
                      )
                    }
                  >
                    <Copy size={14} />
                  </Button>
                </div>
              )}
              {current.review_needed && (
                <div className="req-scenario-review">
                  <p className="req-warning">
                    {t(
                      "故事正文或验收条件已经变化，请核对场景后确认。",
                      "The story narrative or acceptance criteria changed. Review the scenario before confirming.",
                    )}
                  </p>
                  {canEdit && (
                    <Button
                      size="sm"
                      disabled={store.busy}
                      onClick={() =>
                        void store.mutate(() =>
                          api.patch(`${store.base}/scenarios/${current.id}`, {
                            version: current.version,
                            review_needed: false,
                          }),
                        )
                      }
                    >
                      {t("已核对当前故事", "Confirmed against current story")}
                    </Button>
                  )}
                </div>
              )}
            </div>
          )}
          <DiagramCanvas
            path={path}
            revision={store.snapshot.revision}
            onNode={(storyID, scenarioID, stepID) => {
              if (stepID) {
                const scenario = store.scenarios.find(
                  (entry) => entry.id === scenarioID,
                );
                if (scenario) {
                  setSelected(scenario.id);
                  setEdit({ item: scenario, stepID });
                }
              } else if (scenarioID) {
                const scenario = store.scenarios.find(
                  (entry) => entry.id === scenarioID,
                );
                if (scenario) {
                  setSelected(scenario.id);
                  store.select(scenario.story_id);
                  setMode("sequence");
                }
              } else if (storyID) openItem(storyID);
            }}
          />
        </div>
      </div>
      {edit && (
        <ScenarioEditor
          item={edit.item}
          initialStory={edit.storyID}
          stepID={edit.stepID}
          close={() => setEdit(null)}
        />
      )}
    </section>
  );
});

function DiagramCanvas({
  path,
  revision,
  onNode,
}: {
  path: string | null;
  revision: number;
  onNode: (storyID?: string, scenarioID?: string, stepID?: string) => void;
}) {
  const t = appStore.t;
  const [svg, setSVG] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const drag = useRef<{
    x: number;
    y: number;
    panX: number;
    panY: number;
  } | null>(null);
  const viewport = useRef<HTMLDivElement>(null);
  const exportController = useRef<AbortController | null>(null);
  useEffect(() => {
    setSVG("");
    setError("");
    setZoom(1);
    setPan({ x: 0, y: 0 });
    exportController.current?.abort();
    if (!path) {
      setLoading(false);
      return;
    }
    const controller = new AbortController();
    setLoading(true);
    fetch(`/api/v1${path}`, {
      credentials: "include",
      signal: controller.signal,
    })
      .then(async (response) => {
        if (!response.ok)
          throw new Error(
            `${t("图读取失败", "Unable to load diagram")} (${response.status})`,
          );
        const text = await response.text();
        if (controller.signal.aborted) return;
        const safe = DOMPurify.sanitize(text, {
          USE_PROFILES: { svg: true, svgFilters: false },
          ADD_ATTR: [
            "data-story-id",
            "data-scenario-id",
            "data-step-id",
            "data-source-kind",
            "tabindex",
            "role",
            "aria-label",
          ],
          FORBID_TAGS: ["script", "foreignObject", "image", "use", "style"],
          FORBID_ATTR: ["href", "xlink:href", "onload", "onclick", "style"],
        });
        setSVG(safe);
      })
      .catch((cause) => {
        if (!controller.signal.aborted) setError(errorMessage(cause));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => {
      controller.abort();
      exportController.current?.abort();
    };
  }, [path, revision, t]);
  useEffect(() => {
    if (!svg) return;
    for (const node of viewport.current?.querySelectorAll<SVGElement>(
      "[data-story-id],[data-scenario-id],[data-step-id]",
    ) ?? []) {
      node.setAttribute("tabindex", "0");
      node.setAttribute("role", "button");
    }
    fit();
  }, [svg]);
  const activate = (target: EventTarget | null) => {
    const node = (target as Element | null)?.closest?.(
      "[data-step-id],[data-scenario-id],[data-story-id]",
    );
    if (node)
      onNode(
        node.getAttribute("data-story-id") ??
          node.closest("[data-story-id]")?.getAttribute("data-story-id") ??
          undefined,
        node.getAttribute("data-source-kind") === "story"
          ? undefined
          : (node.getAttribute("data-scenario-id") ??
              node
                .closest("[data-scenario-id]")
                ?.getAttribute("data-scenario-id") ??
              undefined),
        node.getAttribute("data-step-id") ?? undefined,
      );
  };
  const fit = () => {
    const element = viewport.current?.querySelector("svg");
    if (!element || !viewport.current) return;
    const dimensions = element.viewBox.baseVal;
    setZoom(
      Math.min(
        1,
        (viewport.current.clientWidth - 40) / (dimensions.width || 800),
        (viewport.current.clientHeight - 40) / (dimensions.height || 600),
      ),
    );
    setPan({ x: 0, y: 0 });
  };
  return (
    <div className="req-diagram">
      <div className="req-diagram-toolbar">
        <Button
          size="icon"
          aria-label={t("缩小", "Zoom out")}
          onClick={() => setZoom((value) => Math.max(0.2, value / 1.2))}
        >
          <Minus size={14} />
        </Button>
        <span>{Math.round(zoom * 100)}%</span>
        <Button
          size="icon"
          aria-label={t("放大", "Zoom in")}
          onClick={() => setZoom((value) => Math.min(4, value * 1.2))}
        >
          <Plus size={14} />
        </Button>
        <Button size="sm" onClick={fit}>
          <Maximize size={14} />
          {t("适应画布", "Fit canvas")}
        </Button>
        <span className="req-spacer" />
        <Button
          size="sm"
          disabled={!svg}
          onClick={async () => {
            if (!path) return;
            const generation = store.authorizationGeneration;
            const base = store.base;
            exportController.current?.abort();
            const controller = new AbortController();
            exportController.current = controller;
            try {
              const response = await fetch(
                `/api/v1${path}${path.includes("?") ? "&" : "?"}download=1`,
                {
                  credentials: "include",
                  cache: "no-store",
                  signal: controller.signal,
                },
              );
              if (!response.ok)
                throw new Error(
                  `${t("导出权限检查失败", "Export authorization failed")} (${response.status})`,
                );
              const text = await response.text();
              if (
                controller.signal.aborted ||
                store.authorizationGeneration !== generation ||
                store.base !== base ||
                store.revoked
              )
                return;
              const blob = new Blob([text], {
                type: "image/svg+xml;charset=utf-8",
              });
              const url = URL.createObjectURL(blob);
              const link = document.createElement("a");
              link.href = url;
              link.download = `myjira-${path.includes("sequence") ? "sequence" : "use-cases"}.svg`;
              link.click();
              URL.revokeObjectURL(url);
            } catch (cause) {
              if (!controller.signal.aborted) setError(errorMessage(cause));
            }
          }}
        >
          <Download size={14} />
          {t("导出 SVG", "Export SVG")}
        </Button>
      </div>
      <ErrorBox message={error} />
      <div
        className="req-diagram-viewport"
        ref={viewport}
        tabIndex={0}
        aria-label={t(
          "可缩放和平移的 UML 画布；方向键平移",
          "Zoomable and pannable UML canvas; arrow keys pan",
        )}
        onPointerDown={(event) => {
          if (
            (event.target as Element).closest(
              "[data-step-id],[data-scenario-id],[data-story-id]",
            )
          )
            return;
          drag.current = {
            x: event.clientX,
            y: event.clientY,
            panX: pan.x,
            panY: pan.y,
          };
          event.currentTarget.setPointerCapture(event.pointerId);
        }}
        onPointerMove={(event) => {
          if (drag.current)
            setPan({
              x: drag.current.panX + event.clientX - drag.current.x,
              y: drag.current.panY + event.clientY - drag.current.y,
            });
        }}
        onPointerUp={() => {
          drag.current = null;
        }}
        onPointerCancel={() => {
          drag.current = null;
        }}
        onClick={(event) => activate(event.target)}
        onKeyDown={(event) => {
          if (
            ["Enter", " "].includes(event.key) &&
            event.target !== event.currentTarget
          ) {
            event.preventDefault();
            activate(event.target);
          } else if (
            ["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(
              event.key,
            ) &&
            event.target === event.currentTarget
          ) {
            event.preventDefault();
            setPan((old) => ({
              x:
                old.x +
                (event.key === "ArrowLeft"
                  ? 30
                  : event.key === "ArrowRight"
                    ? -30
                    : 0),
              y:
                old.y +
                (event.key === "ArrowUp"
                  ? 30
                  : event.key === "ArrowDown"
                    ? -30
                    : 0),
            }));
          }
        }}
      >
        {loading ? (
          <Loading />
        ) : svg ? (
          <div
            className="req-diagram-content"
            style={{
              transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})`,
            }}
            dangerouslySetInnerHTML={{ __html: svg }}
          />
        ) : (
          !error && (
            <p className="req-empty-inline">
              {t(
                "创建场景后即可查看交互图。",
                "Create a scenario to see its interaction diagram.",
              )}
            </p>
          )
        )}
      </div>
      <p className="field-hint">
        {t(
          "拖动画布平移；点击节点定位故事、场景或具体步骤。",
          "Drag the canvas to pan. Activate nodes to locate their story, scenario or step.",
        )}
      </p>
    </div>
  );
}

const ScenarioEditor = observer(function ScenarioEditor({
  item,
  initialStory,
  stepID,
  close,
}: {
  item?: Scenario;
  initialStory?: string;
  stepID?: string;
  close: () => void;
}) {
  const t = appStore.t;
  const canEdit = store.canEdit;
  const { workspace, project } = useScope();
  const [form, setForm] = useState({
    story_id: item?.story_id ?? initialStory ?? "",
    name: item?.name ?? "",
    goal: item?.goal ?? "",
    trigger: item?.trigger ?? "",
    preconditions: item?.preconditions ?? "",
    outcome: item?.outcome ?? "",
    system_boundary: item?.system_boundary ?? project?.name ?? "",
    bind_story_name: item?.bind_story_name ?? true,
    source_page_ids: item?.source_page_ids ?? [],
    participants: item?.participants ?? ([] as ScenarioParticipant[]),
    steps: item?.steps ?? ([] as ScenarioStep[]),
    relationships: item?.relationships ?? ([] as ScenarioRelationship[]),
  });
  const [error, setError] = useState("");
  const pages = useRemote<PageDocument[]>(`${store.base}/pages`);
  const update = <K extends keyof typeof form>(
    key: K,
    value: (typeof form)[K],
  ) => setForm((old) => ({ ...old, [key]: value }));
  const setStep = (index: number, fields: Partial<ScenarioStep>) =>
    update(
      "steps",
      form.steps.map((step, position) =>
        index === position ? { ...step, ...fields } : step,
      ),
    );
  const reorder = <T,>(entries: T[], index: number, direction: number): T[] => {
    const result = [...entries];
    [result[index], result[index + direction]] = [
      result[index + direction],
      result[index],
    ];
    return result;
  };
  useEffect(() => {
    if (stepID)
      document
        .getElementById(`scenario-step-${stepID}`)
        ?.scrollIntoView({ block: "center" });
  }, [stepID]);
  const endpoints = [
    ...form.participants.map((participant) => ({
      id: participant.id,
      name: `${t("参与者", "Participant")}: ${participant.name}`,
    })),
    ...store.scenarios.map((scenario) => ({
      id: scenario.id,
      name: `${t("用例", "Use case")}: ${scenario.id === item?.id ? form.name : scenario.name}`,
    })),
  ];
  const validate = () => {
    let branch = false,
      hasElse = false;
    const calls = new Set<string>();
    for (const step of form.steps) {
      if (step.kind === "alt") {
        if (branch)
          return t("仅支持一层条件分支", "Only one branch level is supported");
        branch = true;
        hasElse = false;
      } else if (step.kind === "else") {
        if (!branch || hasElse)
          return t(
            "else 必须位于 alt 与 end 之间，且只能出现一次",
            "else must appear once between alt and end",
          );
        hasElse = true;
      } else if (step.kind === "end") {
        if (!branch)
          return t("end 缺少对应 alt", "end requires a matching alt");
        branch = false;
      } else {
        if (
          !form.participants.some(
            (participant) => participant.id === step.from_id,
          ) ||
          !form.participants.some(
            (participant) => participant.id === step.to_id,
          )
        )
          return t(
            "每个消息需要有效的发送方和接收方",
            "Every message needs valid participants",
          );
        if (
          step.kind === "return" &&
          (!step.return_of || !calls.has(step.return_of))
        )
          return t(
            "返回消息必须引用前面的调用",
            "Returns must reference an earlier call",
          );
        if (step.kind === "call") calls.add(step.id);
      }
    }
    return branch ? t("条件分支需要 end 结束", "A branch requires an end") : "";
  };
  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
      title={
        item
          ? t("编辑业务场景", "Edit business scenario")
          : t("新建业务场景", "New business scenario")
      }
      className="req-scenario-modal"
    >
      <form
        className="req-form"
        onSubmit={async (event) => {
          event.preventDefault();
          const invalid = validate();
          if (invalid) {
            setError(invalid);
            return;
          }
          const payload = {
            ...form,
            steps: form.steps.map((step) =>
              ["alt", "else", "end"].includes(step.kind)
                ? { id: step.id, kind: step.kind, message: step.message }
                : {
                    id: step.id,
                    kind: step.kind,
                    message: step.message,
                    from_id: step.from_id,
                    to_id: step.to_id,
                    ...(step.kind === "return"
                      ? { return_of: step.return_of }
                      : {}),
                  },
            ),
            ...(item ? { version: item.version } : {}),
          };
          if (
            await store.mutate(() =>
              item
                ? api.patch(`${store.base}/scenarios/${item.id}`, payload)
                : api.post(`${store.base}/scenarios`, payload),
            )
          )
            close();
        }}
      >
        <div className="req-form-grid">
          <Field label={t("主 Story", "Main story")}>
            <Select
              required
              disabled={!canEdit}
              value={form.story_id}
              onChange={(event) => update("story_id", event.target.value)}
            >
              <option value="">{t("选择 Story", "Select story")}</option>
              {store.snapshot.items
                .filter((story) => story.requirement_type === "story")
                .map((story) => (
                  <option key={story.id} value={story.id}>
                    {story.name}
                  </option>
                ))}
            </Select>
          </Field>
          <Field label={t("场景名称", "Scenario name")}>
            <Input
              required
              value={form.name}
              disabled={!canEdit}
              onChange={(event) => update("name", event.target.value)}
            />
          </Field>
        </div>
        <label className="req-check">
          <input
            type="checkbox"
            disabled={!canEdit}
            checked={form.bind_story_name}
            onChange={(event) =>
              update("bind_story_name", event.target.checked)
            }
          />
          {t(
            "图标签绑定 Story 名称和当前进度",
            "Bind diagram labels to story name and current progress",
          )}
        </label>
        {(
          [
            "goal",
            "trigger",
            "preconditions",
            "outcome",
            "system_boundary",
          ] as const
        ).map((key, index) => (
          <Field
            key={key}
            label={t(
              ["业务目标", "触发条件", "前置条件", "结果", "系统边界"][index],
              [
                "Business goal",
                "Trigger",
                "Preconditions",
                "Outcome",
                "System boundary",
              ][index],
            )}
          >
            <Input
              value={form[key]}
              disabled={!canEdit}
              onChange={(event) => update(key, event.target.value)}
            />
          </Field>
        ))}
        <Field
          label={t("显式来源文档", "Explicit source documents")}
          hint={t(
            "场景与导出仅向同时能读取主 Story 及来源的成员开放。",
            "Scenario and export access requires access to both its story and source documents.",
          )}
        >
          <MultiSelect
            value={form.source_page_ids}
            disabled={!canEdit}
            onChange={(ids) => update("source_page_ids", ids)}
            placeholder={t("选择来源文档", "Select source documents")}
            options={(pages.data ?? []).map((page) => ({
              value: page.id,
              label: page.name,
            }))}
          />
        </Field>
        {form.source_page_ids.map((id) => (
          <a
            className="req-inline-link"
            key={id}
            href={`/w/${workspace.slug}/projects/${project!.id}/pages/${id}`}
            target="_blank"
            rel="noreferrer"
          >
            {pages.data?.find((page) => page.id === id)?.name ??
              t("来源文档", "Source document")}
          </a>
        ))}
        <fieldset className="req-fieldset">
          <legend>
            {t("业务参与者和系统", "Business actors and systems")}
          </legend>
          {form.participants.map((participant, index) => (
            <div className="req-participant-row" key={participant.id}>
              <Field label={t("参与者名称", "Participant name")}>
                <Input
                  required
                  value={participant.name}
                  disabled={!canEdit}
                  onChange={(event) =>
                    update(
                      "participants",
                      form.participants.map((entry) =>
                        entry.id === participant.id
                          ? { ...entry, name: event.target.value }
                          : entry,
                      ),
                    )
                  }
                />
              </Field>
              <Field label={t("种类", "Kind")}>
                <Select
                  value={participant.kind}
                  disabled={!canEdit}
                  onChange={(event) =>
                    update(
                      "participants",
                      form.participants.map((entry) =>
                        entry.id === participant.id
                          ? {
                              ...entry,
                              kind: event.target
                                .value as ScenarioParticipant["kind"],
                            }
                          : entry,
                      ),
                    )
                  }
                >
                  <option value="actor">
                    {t("业务角色", "Business actor")}
                  </option>
                  <option value="system">{t("系统", "System")}</option>
                </Select>
              </Field>
              {canEdit && (
                <>
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    disabled={index === 0}
                    aria-label={t("参与者前移", "Move participant earlier")}
                    onClick={() =>
                      update(
                        "participants",
                        reorder(form.participants, index, -1),
                      )
                    }
                  >
                    <ArrowUp size={14} />
                  </Button>
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    aria-label={t("删除参与者", "Remove participant")}
                    onClick={() =>
                      update(
                        "participants",
                        form.participants.filter(
                          (entry) => entry.id !== participant.id,
                        ),
                      )
                    }
                  >
                    <X size={14} />
                  </Button>
                </>
              )}
            </div>
          ))}
          {canEdit && (
            <Button
              type="button"
              size="sm"
              onClick={() =>
                update("participants", [
                  ...form.participants,
                  { id: crypto.randomUUID(), name: "", kind: "actor" },
                ])
              }
            >
              <Plus size={14} />
              {t("添加参与者", "Add participant")}
            </Button>
          )}
        </fieldset>
        <fieldset className="req-fieldset">
          <legend>
            {t("有序消息与条件分支", "Ordered messages and branches")}
          </legend>
          {form.steps.map((step, index) => (
            <div
              key={step.id}
              id={`scenario-step-${step.id}`}
              className={`req-step ${stepID === step.id ? "is-selected" : ""}`}
            >
              <div className="req-step-heading">
                <strong>#{index + 1}</strong>
                <Select
                  aria-label={t("步骤类型", "Step type")}
                  disabled={!canEdit}
                  value={step.kind}
                  onChange={(event) =>
                    setStep(index, {
                      kind: event.target.value as ScenarioStep["kind"],
                    })
                  }
                >
                  {[
                    ["call", "调用", "Call"],
                    ["return", "返回", "Return"],
                    ["alt", "条件分支", "alt branch"],
                    ["else", "否则", "else branch"],
                    ["end", "结束分支", "end branch"],
                  ].map(([value, zh, en]) => (
                    <option key={value} value={value}>
                      {t(zh, en)}
                    </option>
                  ))}
                </Select>
                {canEdit && (
                  <div className="req-actions">
                    <Button
                      type="button"
                      size="icon"
                      variant="ghost"
                      disabled={index === 0}
                      aria-label={t("步骤前移", "Move step earlier")}
                      onClick={() =>
                        update("steps", reorder(form.steps, index, -1))
                      }
                    >
                      <ArrowUp size={14} />
                    </Button>
                    <Button
                      type="button"
                      size="icon"
                      variant="ghost"
                      disabled={index === form.steps.length - 1}
                      aria-label={t("步骤后移", "Move step later")}
                      onClick={() =>
                        update("steps", reorder(form.steps, index, 1))
                      }
                    >
                      <ArrowDown size={14} />
                    </Button>
                    <Button
                      type="button"
                      size="icon"
                      variant="ghost"
                      aria-label={t("删除步骤", "Remove step")}
                      onClick={() =>
                        update(
                          "steps",
                          form.steps.filter((entry) => entry.id !== step.id),
                        )
                      }
                    >
                      <X size={14} />
                    </Button>
                  </div>
                )}
              </div>
              {["call", "return"].includes(step.kind) && (
                <div className="req-form-grid">
                  {(["from_id", "to_id"] as const).map((key) => (
                    <Field
                      key={key}
                      label={
                        key === "from_id"
                          ? t("发送方", "Sender")
                          : t("接收方", "Receiver")
                      }
                    >
                      <Select
                        required
                        disabled={!canEdit}
                        value={step[key] ?? ""}
                        onChange={(event) =>
                          setStep(index, { [key]: event.target.value })
                        }
                      >
                        <option value="">
                          {t("选择参与者", "Select participant")}
                        </option>
                        {form.participants.map((participant) => (
                          <option key={participant.id} value={participant.id}>
                            {participant.name}
                          </option>
                        ))}
                      </Select>
                    </Field>
                  ))}
                </div>
              )}
              {step.kind === "return" && (
                <Field label={t("对应调用", "Return for call")}>
                  <Select
                    required
                    disabled={!canEdit}
                    value={step.return_of ?? ""}
                    onChange={(event) =>
                      setStep(index, { return_of: event.target.value })
                    }
                  >
                    <option value="">
                      {t("选择前面的调用", "Select an earlier call")}
                    </option>
                    {form.steps
                      .slice(0, index)
                      .filter((entry) => entry.kind === "call")
                      .map((entry) => (
                        <option key={entry.id} value={entry.id}>
                          #{form.steps.indexOf(entry) + 1} {entry.message}
                        </option>
                      ))}
                  </Select>
                </Field>
              )}
              {step.kind !== "end" && (
                <Field
                  label={
                    ["alt", "else"].includes(step.kind)
                      ? t("条件", "Condition")
                      : t("消息", "Message")
                  }
                >
                  <Input
                    required
                    disabled={!canEdit}
                    value={step.message}
                    onChange={(event) =>
                      setStep(index, { message: event.target.value })
                    }
                  />
                </Field>
              )}
            </div>
          ))}
          {canEdit && (
            <Button
              type="button"
              size="sm"
              onClick={() =>
                update("steps", [
                  ...form.steps,
                  {
                    id: crypto.randomUUID(),
                    kind: "call",
                    message: "",
                    from_id: form.participants[0]?.id,
                    to_id: form.participants[1]?.id,
                  },
                ])
              }
            >
              <Plus size={14} />
              {t("添加步骤", "Add step")}
            </Button>
          )}
        </fieldset>
        <fieldset className="req-fieldset">
          <legend>
            {t("显式用例关系", "Explicit use case relationships")}
          </legend>
          {!item && (
            <p className="field-hint">
              {t(
                "先保存场景，再建立与用例的关系。",
                "Save this scenario before connecting use case relationships.",
              )}
            </p>
          )}
          {form.relationships.map((relation, index) => (
            <div className="req-relation-form" key={relation.id}>
              <Select
                aria-label={t("关系类型", "Relationship type")}
                disabled={!canEdit}
                value={relation.kind}
                onChange={(event) =>
                  update(
                    "relationships",
                    form.relationships.map((entry, position) =>
                      position === index
                        ? {
                            ...entry,
                            kind: event.target
                              .value as ScenarioRelationship["kind"],
                          }
                        : entry,
                    ),
                  )
                }
              >
                {["association", "include", "extend", "generalization"].map(
                  (kind) => (
                    <option value={kind} key={kind}>
                      {kind}
                    </option>
                  ),
                )}
              </Select>
              {(["from_id", "to_id"] as const).map((key) => (
                <Select
                  key={key}
                  aria-label={
                    key === "from_id"
                      ? t("关系来源", "Relationship source")
                      : t("关系目标", "Relationship target")
                  }
                  required
                  disabled={!canEdit}
                  value={relation[key]}
                  onChange={(event) =>
                    update(
                      "relationships",
                      form.relationships.map((entry, position) =>
                        position === index
                          ? { ...entry, [key]: event.target.value }
                          : entry,
                      ),
                    )
                  }
                >
                  <option value="">{t("选择端点", "Select endpoint")}</option>
                  {endpoints.map((endpoint) => (
                    <option key={endpoint.id} value={endpoint.id}>
                      {endpoint.name}
                    </option>
                  ))}
                </Select>
              ))}
              {canEdit && (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={t("删除关系", "Remove relationship")}
                  onClick={() =>
                    update(
                      "relationships",
                      form.relationships.filter(
                        (entry) => entry.id !== relation.id,
                      ),
                    )
                  }
                >
                  <X size={14} />
                </Button>
              )}
            </div>
          ))}
          {canEdit && item && (
            <Button
              type="button"
              size="sm"
              onClick={() =>
                update("relationships", [
                  ...form.relationships,
                  {
                    id: crypto.randomUUID(),
                    kind: "association",
                    from_id: form.participants[0]?.id ?? "",
                    to_id: item.id,
                  },
                ])
              }
            >
              <Plus size={14} />
              {t("添加关系", "Add relationship")}
            </Button>
          )}
        </fieldset>
        <ErrorBox message={error || store.error} />
        <div className="req-form-footer">
          {canEdit && item && (
            <Button
              type="button"
              variant="danger"
              disabled={store.busy}
              onClick={async () => {
                if (
                  window.confirm(
                    t("删除这个业务场景？", "Delete this business scenario?"),
                  ) &&
                  (await store.mutate(() =>
                    api.delete(
                      `${store.base}/scenarios/${item.id}?version=${item.version}`,
                    ),
                  ))
                )
                  close();
              }}
            >
              {t("删除场景", "Delete scenario")}
            </Button>
          )}
          <span className="req-spacer" />
          <Button type="button" onClick={close}>
            {t("关闭", "Close")}
          </Button>
          {canEdit && (
            <Button type="submit" variant="primary" busy={store.busy}>
              <Save size={14} />
              {t("保存场景并更新图", "Save scenario and update diagrams")}
            </Button>
          )}
        </div>
      </form>
    </Modal>
  );
});
