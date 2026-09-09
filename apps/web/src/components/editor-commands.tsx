import { useContext, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { Editor } from "@tiptap/react";
import type { EmojiMartData } from "@emoji-mart/data";
import { observer } from "mobx-react-lite";
import {
  Code,
  Heading1,
  Heading2,
  Heading3,
  Image,
  Info,
  List,
  ListChecks,
  ListOrdered,
  ListTodo,
  Minus,
  Quote,
  Search,
  Smile,
  Sparkles,
  Table2,
  AtSign,
  ChevronRight,
  Link2,
  type LucideIcon,
} from "lucide-react";
import { appStore } from "../stores/app-store";
import { api, projectPath, workspacePath } from "../lib/api";
import {
  ScopeContext,
  useDebouncedValue,
  useMutation,
  useRemote,
} from "../lib/hooks";
import type { WorkItem } from "../types";
import {
  Button,
  ErrorBox,
  Input,
  Loading,
  Modal,
  PriorityIcon,
  Select,
  Tooltip,
} from "./ui";
import {
  EditorBlockControls,
  editorOverlayHost,
  editorOverlayPoint,
} from "./editor-block-controls";

interface Command {
  id: string;
  name: string;
  keywords: string;
  icon: LucideIcon;
  run: () => void;
}
interface SlashMenu {
  from: number;
  to: number;
  query: string;
  left: number;
  top: number;
}

export const EditorCommands = observer(function EditorCommands({
  editor,
  disabled,
  projectId,
  onImage,
  onMention,
  onEmbed,
  onAI,
}: {
  editor: Editor;
  disabled: boolean;
  projectId?: string;
  onImage: () => void;
  onMention: () => void;
  onEmbed: () => void;
  onAI: () => void;
}) {
  const scope = useContext(ScopeContext);
  const [emojiOpen, setEmojiOpen] = useState(false);
  const [workOpen, setWorkOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [projectFilter, setProjectFilter] = useState(
    projectId ?? scope?.project?.id ?? "",
  );
  const search = useDebouncedValue(query);
  const [slash, setSlash] = useState<SlashMenu | null>(null);
  const slashRef = useRef<SlashMenu | null>(null);
  const dismissed = useRef("");
  const [activeIndex, setActiveIndex] = useState(0);
  const selectedRef = useRef(0);
  const menuID = useRef(`editor-commands-${crypto.randomUUID()}`);
  const mutation = useMutation();
  const issues = useRemote<WorkItem[]>(
    scope && workOpen
      ? `${workspacePath(scope.workspace.id)}/issues?limit=30&search=${encodeURIComponent(search)}${projectFilter ? `&project_id=${encodeURIComponent(projectFilter)}` : ""}`
      : null,
  );
  const t = appStore.t;
  const host = editorOverlayHost(editor);
  const insert = (content: Record<string, unknown>) => {
    editor.chain().focus().insertContent(content).run();
  };
  const commands: Command[] = [
    {
      id: "heading1",
      name: t("一级标题", "Heading 1"),
      keywords: "h1 标题 heading",
      icon: Heading1,
      run: () => {
        editor.chain().focus().toggleHeading({ level: 1 }).run();
      },
    },
    {
      id: "heading2",
      name: t("二级标题", "Heading 2"),
      keywords: "h2 标题 heading",
      icon: Heading2,
      run: () => {
        editor.chain().focus().toggleHeading({ level: 2 }).run();
      },
    },
    {
      id: "heading3",
      name: t("三级标题", "Heading 3"),
      keywords: "h3 标题 heading",
      icon: Heading3,
      run: () => {
        editor.chain().focus().toggleHeading({ level: 3 }).run();
      },
    },
    {
      id: "bullet",
      name: t("项目列表", "Bullet list"),
      keywords: "bullet list 列表",
      icon: List,
      run: () => {
        editor.chain().focus().toggleBulletList().run();
      },
    },
    {
      id: "ordered",
      name: t("有序列表", "Numbered list"),
      keywords: "ordered number list 列表",
      icon: ListOrdered,
      run: () => {
        editor.chain().focus().toggleOrderedList().run();
      },
    },
    {
      id: "task",
      name: t("待办清单", "Checklist"),
      keywords: "task todo checklist 待办 清单",
      icon: ListChecks,
      run: () => {
        editor.chain().focus().toggleTaskList().run();
      },
    },
    {
      id: "quote",
      name: t("引用", "Quote"),
      keywords: "quote 引用",
      icon: Quote,
      run: () => {
        editor.chain().focus().toggleBlockquote().run();
      },
    },
    {
      id: "code",
      name: t("代码块", "Code block"),
      keywords: "code 代码",
      icon: Code,
      run: () => {
        editor.chain().focus().toggleCodeBlock().run();
      },
    },
    {
      id: "table",
      name: t("表格", "Table"),
      keywords: "table 表格",
      icon: Table2,
      run: () => {
        editor
          .chain()
          .focus()
          .insertTable({ rows: 3, cols: 3, withHeaderRow: true })
          .run();
      },
    },
    {
      id: "line",
      name: t("分隔线", "Divider"),
      keywords: "rule divider hr 分隔",
      icon: Minus,
      run: () => {
        editor.chain().focus().setHorizontalRule().run();
      },
    },
    {
      id: "callout",
      name: t("提示框", "Callout"),
      keywords: "callout info 提示",
      icon: Info,
      run: () =>
        insert({
          type: "callout",
          attrs: { tone: "info" },
          content: [{ type: "paragraph" }],
        }),
    },
    {
      id: "disclosure",
      name: t("折叠内容", "Collapsible content"),
      keywords: "toggle disclosure collapse 折叠",
      icon: ChevronRight,
      run: () =>
        insert({
          type: "disclosure",
          attrs: { title: t("展开详情", "Open details") },
          content: [{ type: "paragraph" }],
        }),
    },
    {
      id: "emoji",
      name: t("表情", "Emoji"),
      keywords: "emoji smile 表情",
      icon: Smile,
      run: () => setEmojiOpen(true),
    },
    {
      id: "work",
      name: t("嵌入工作项", "Embed work item"),
      keywords: "issue work item task 工作项 任务",
      icon: ListTodo,
      run: () => setWorkOpen(true),
    },
    {
      id: "image",
      name: t("图片", "Image"),
      keywords: "image photo 图片",
      icon: Image,
      run: onImage,
    },
    {
      id: "mention",
      name: t("提及成员", "Mention member"),
      keywords: "mention member 提及",
      icon: AtSign,
      run: onMention,
    },
    {
      id: "embed",
      name: t("嵌入服务", "Embed service"),
      keywords: "embed video figma 嵌入",
      icon: Link2,
      run: onEmbed,
    },
    {
      id: "ai",
      name: t("AI 写作助手", "AI writing assistant"),
      keywords: "ai write generate 写作",
      icon: Sparkles,
      run: onAI,
    },
  ];
  const commandRef = useRef(commands);
  commandRef.current = commands;
  const matching = (value: string) =>
    commandRef.current.filter((item) =>
      `${item.id} ${item.name} ${item.keywords}`
        .toLowerCase()
        .includes(value.toLowerCase()),
    );
  const choose = (command: Command) => {
    if (!editor.isEditable) return;
    const range = slashRef.current;
    slashRef.current = null;
    setSlash(null);
    if (range)
      editor
        .chain()
        .focus()
        .deleteRange({ from: range.from, to: range.to })
        .run();
    command.run();
  };
  const chooseRef = useRef(choose);
  chooseRef.current = choose;
  useEffect(() => {
    if (disabled) {
      slashRef.current = null;
      setSlash(null);
      return;
    }
    const inspect = () => {
      if (!editor.isFocused || !editor.isEditable) {
        slashRef.current = null;
        setSlash(null);
        return;
      }
      const { $from, empty } = editor.state.selection;
      const prefix = $from.parent.textBetween(
        0,
        $from.parentOffset,
        "",
        "\ufffc",
      );
      const match =
        empty &&
        $from.parent.isTextblock &&
        $from.parent.type.name !== "codeBlock"
          ? prefix.match(/^\/([^\s/]{0,32})$/)
          : null;
      if (!match || dismissed.current === `${$from.start()}:${prefix}`) {
        slashRef.current = null;
        setSlash(null);
        return;
      }
      if (slashRef.current?.query !== match[1]) {
        selectedRef.current = 0;
        setActiveIndex(0);
      }
      const rect = editor.view.coordsAtPos($from.pos);
      const next = {
        from: $from.start(),
        to: $from.pos,
        query: match[1],
        left: Math.max(8, Math.min(rect.left, window.innerWidth - 282)),
        top: rect.bottom + 6,
      };
      slashRef.current = next;
      setSlash(next);
    };
    const keydown = (event: KeyboardEvent) => {
      const current = slashRef.current;
      if (!current || event.isComposing) return;
      const options = matching(current.query);
      if (!["ArrowDown", "ArrowUp", "Enter", "Escape"].includes(event.key))
        return;
      if (!options.length && event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      event.stopImmediatePropagation();
      if (event.key === "Escape") {
        dismissed.current = `${current.from}:/${current.query}`;
        slashRef.current = null;
        setSlash(null);
      } else if (event.key === "Enter")
        chooseRef.current(
          options[Math.min(selectedRef.current, options.length - 1)],
        );
      else {
        selectedRef.current =
          (selectedRef.current +
            (event.key === "ArrowDown" ? 1 : -1) +
            options.length) %
          options.length;
        setActiveIndex(selectedRef.current);
      }
    };
    editor.on("transaction", inspect);
    editor.on("focus", inspect);
    editor.on("blur", inspect);
    editor.view.dom.addEventListener("keydown", keydown, true);
    window.addEventListener("scroll", inspect, true);
    return () => {
      editor.off("transaction", inspect);
      editor.off("focus", inspect);
      editor.off("blur", inspect);
      editor.view.dom.removeEventListener("keydown", keydown, true);
      window.removeEventListener("scroll", inspect, true);
    };
  }, [editor, disabled]);
  useEffect(() => {
    const dom = editor.view.dom;
    if (slash) {
      dom.setAttribute("aria-controls", menuID.current);
      dom.setAttribute("aria-expanded", "true");
      dom.setAttribute(
        "aria-activedescendant",
        `${menuID.current}-${activeIndex}`,
      );
      document
        .getElementById(`${menuID.current}-${activeIndex}`)
        ?.scrollIntoView({ block: "nearest" });
    } else {
      dom.removeAttribute("aria-controls");
      dom.removeAttribute("aria-expanded");
      dom.removeAttribute("aria-activedescendant");
    }
  }, [editor, slash?.query, !!slash, activeIndex]);
  if (!scope) return null;
  const options = slash ? matching(slash.query) : [];
  return (
    <>
      <Tooltip label={t("插入表情", "Insert emoji")}>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          disabled={disabled}
          aria-label={t("插入表情", "Insert emoji")}
          onClick={() => setEmojiOpen(true)}
        >
          <Smile size={14} />
        </Button>
      </Tooltip>
      <Tooltip label={t("嵌入工作项", "Embed work item")}>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          disabled={disabled}
          aria-label={t("嵌入工作项", "Embed work item")}
          onClick={() => setWorkOpen(true)}
        >
          <ListTodo size={14} />
        </Button>
      </Tooltip>
      <EditorBlockControls editor={editor} disabled={disabled} />
      {slash &&
        createPortal(
          <div
            id={menuID.current}
            className="editor-slash-menu"
            role="listbox"
            aria-label={t("插入内容", "Insert content")}
            style={editorOverlayPoint(host, slash.left, slash.top)}
            onPointerDown={(event) => event.preventDefault()}
          >
            <small>{t("插入内容", "Insert content")}</small>
            {options.length ? (
              options.map((command, index) => (
                <button
                  type="button"
                  key={command.id}
                  id={`${menuID.current}-${index}`}
                  role="option"
                  aria-selected={index === activeIndex}
                  className={index === activeIndex ? "active" : ""}
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => choose(command)}
                >
                  <command.icon size={15} />
                  <span>{command.name}</span>
                </button>
              ))
            ) : (
              <p className="settings-note">
                {t("没有匹配的命令", "No matching commands")}
              </p>
            )}
          </div>,
          host,
        )}
      <EmojiPicker
        open={emojiOpen}
        onOpenChange={setEmojiOpen}
        onSelect={(native) => {
          if (editor.isEditable)
            editor
              .chain()
              .focus()
              .insertContent({ type: "text", text: native })
              .run();
          setEmojiOpen(false);
        }}
      />
      <Modal
        open={workOpen}
        onOpenChange={setWorkOpen}
        title={t("嵌入工作项", "Embed work item")}
        description={t(
          "每位读者只能查看自己有权访问的工作项。",
          "Each reader sees only work items they can access.",
        )}
      >
        <div className="modal-body form-stack">
          <div className="form-row">
            <div className="search-input">
              <Search size={14} />
              <Input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder={t(
                  "搜索名称或编号…",
                  "Search title or identifier…",
                )}
                autoFocus
              />
            </div>
            <Select
              aria-label={t("工作项项目", "Work item project")}
              value={projectFilter}
              onChange={(event) => setProjectFilter(event.target.value)}
            >
              <option value="">{t("所有项目", "All projects")}</option>
              {appStore.workspaceProjects(scope.workspace.id).map((project) => (
                <option value={project.id} key={project.id}>
                  {project.name}
                </option>
              ))}
            </Select>
          </div>
          <ErrorBox message={issues.error || mutation.error} />
          {issues.loading ? (
            <Loading />
          ) : (
            <div className="editor-work-search">
              {issues.data?.map((issue) => (
                <button
                  type="button"
                  key={issue.id}
                  disabled={mutation.busy}
                  onClick={() =>
                    mutation.execute(async () => {
                      const result = await api.get<WorkItem>(
                        `${projectPath(scope.workspace.id, issue.project_id)}/issues/${issue.id}`,
                      );
                      if (!editor.isEditable)
                        throw new Error(
                          t(
                            "文档当前无法编辑。",
                            "The document is not editable.",
                          ),
                        );
                      editor
                        .chain()
                        .focus()
                        .insertContent([
                          {
                            type: "workItem",
                            attrs: {
                              workspaceId: scope.workspace.id,
                              projectId: result.data.project_id,
                              issueId: result.data.id,
                            },
                          },
                          { type: "paragraph" },
                        ])
                        .run();
                      setWorkOpen(false);
                    })
                  }
                >
                  <PriorityIcon priority={issue.priority} />
                  <span>
                    <small>
                      {appStore.projects.get(issue.project_id)?.identifier}-
                      {issue.sequence_id}
                    </small>
                    <strong>{issue.name}</strong>
                  </span>
                </button>
              ))}
              {!issues.data?.length && (
                <p className="settings-note">
                  {t("没有匹配的工作项。", "No matching work items.")}
                </p>
              )}
            </div>
          )}
        </div>
      </Modal>
    </>
  );
});

const emojiCategories: Record<string, [string, string]> = {
  people: ["表情与人物", "Smileys & people"],
  nature: ["动物与自然", "Animals & nature"],
  foods: ["饮食", "Food & drink"],
  activity: ["活动", "Activities"],
  places: ["旅行与地点", "Travel & places"],
  objects: ["物品", "Objects"],
  symbols: ["符号", "Symbols"],
  flags: ["旗帜", "Flags"],
};
const EmojiPicker = observer(function EmojiPicker({
  open,
  onOpenChange,
  onSelect,
}: {
  open: boolean;
  onOpenChange: (value: boolean) => void;
  onSelect: (value: string) => void;
}) {
  const [data, setData] = useState<EmojiMartData | null>(null);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [category, setCategory] = useState("people");
  const [skin, setSkin] = useState(0);
  const t = appStore.t;
  useEffect(() => {
    if (!open || data) return;
    let active = true;
    import("@emoji-mart/data")
      .then((module) => {
        if (active)
          setData((module as unknown as { default: EmojiMartData }).default);
      })
      .catch(() => {
        if (active)
          setError(
            t(
              "表情数据加载失败，请重试。",
              "Emoji data failed to load. Please try again.",
            ),
          );
      });
    return () => {
      active = false;
    };
  }, [open, !!data]);
  const aliases: Record<string, string> = {
    笑: "smile",
    爱: "heart",
    心: "heart",
    赞: "thumb",
    猫: "cat",
    狗: "dog",
    花: "flower",
    火: "fire",
    星: "star",
    哭: "cry",
    生气: "angry",
    庆祝: "party",
  };
  const search = (aliases[query.trim()] ?? query.trim()).toLowerCase();
  const emojis = data
    ? search
      ? Object.values(data.emojis).filter((item) =>
          `${item.name} ${item.keywords.join(" ")} ${item.skins.map((value) => value.native).join(" ")}`
            .toLowerCase()
            .includes(search),
        )
      : (
          data.categories.find((item) => item.id === category)?.emojis ?? []
        ).map((id) => data.emojis[id])
    : [];
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={t("选择表情", "Choose emoji")}
    >
      <div className="modal-body form-stack">
        <Input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder={t("搜索表情…", "Search emoji…")}
          aria-label={t("搜索表情", "Search emoji")}
          autoFocus
        />
        <div className="form-row">
          <Select
            value={category}
            onChange={(event) => {
              setCategory(event.target.value);
              setQuery("");
            }}
            aria-label={t("表情类别", "Emoji category")}
          >
            {Object.entries(emojiCategories).map(([id, names]) => (
              <option key={id} value={id}>
                {t(...names)}
              </option>
            ))}
          </Select>
          <Select
            value={skin}
            onChange={(event) => setSkin(Number(event.target.value))}
            aria-label={t("肤色", "Skin tone")}
          >
            {["🟡", "🏻", "🏼", "🏽", "🏾", "🏿"].map((value, index) => (
              <option key={index} value={index}>
                {value}{" "}
                {index ? t("肤色", "Tone") + ` ${index}` : t("默认", "Default")}
              </option>
            ))}
          </Select>
        </div>
        <ErrorBox message={error} />
        {!data && !error ? (
          <Loading />
        ) : (
          <div className="emoji-grid">
            {emojis.map((emoji) => {
              const native = (emoji.skins[skin] ?? emoji.skins[0]).native;
              return (
                <button
                  type="button"
                  key={emoji.id}
                  title={emoji.name}
                  aria-label={emoji.name}
                  onClick={() => onSelect(native)}
                >
                  {native}
                </button>
              );
            })}
          </div>
        )}
        {data && !emojis.length && (
          <p className="settings-note">
            {t("没有匹配的表情", "No matching emoji")}
          </p>
        )}
      </div>
    </Modal>
  );
});
