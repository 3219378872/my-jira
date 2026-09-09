import { useEffect, useRef } from "react";
import { EditorContent, useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import Placeholder from "@tiptap/extension-placeholder";
import Image from "@tiptap/extension-image";
import TaskList from "@tiptap/extension-task-list";
import TaskItem from "@tiptap/extension-task-item";
import { TableKit } from "@tiptap/extension-table";
import {
  Bold,
  Code,
  Heading2,
  Italic,
  Link,
  List,
  ListChecks,
  ListOrdered,
  Quote,
  Redo2,
  Strikethrough,
  Table,
  Undo2,
} from "lucide-react";
import { observer } from "mobx-react-lite";
import { appStore } from "../stores/app-store";
import { cn } from "../lib/utils";
import { Button, Tooltip } from "./ui";
import { webEditorBlocks } from "./editor-schema";
import { AdvancedEditorTools } from "./editor-tools";

export const RichEditor = observer(function RichEditor({
  value,
  onChange,
  placeholder,
  editable = true,
  compact = false,
  className,
  projectId,
  workItemId,
}: {
  value: string;
  onChange: (html: string, json: unknown) => void;
  placeholder?: string;
  editable?: boolean;
  compact?: boolean;
  className?: string;
  projectId?: string;
  workItemId?: string;
}) {
  const initialized = useRef(false);
  const applyingValue = useRef(false);
  const lastHTML = useRef("");
  const editor = useEditor({
    extensions: [
      StarterKit.configure({
        heading: { levels: [1, 2, 3] },
        link: { openOnClick: false },
      }),
      Placeholder.configure({
        placeholder:
          placeholder ?? appStore.t("在这里记录想法…", "Write something here…"),
      }),
      Image.configure({ allowBase64: false }),
      TaskList,
      TaskItem.configure({ nested: true }),
      TableKit.configure({ table: { resizable: true } }),
      ...webEditorBlocks,
    ],
    content: value,
    editable,
    immediatelyRender: false,
    onCreate: ({ editor: created }) => {
      initialized.current = true;
      lastHTML.current = created.getHTML();
    },
    onUpdate: ({ editor: updated, transaction }) => {
      const appended = transaction.getMeta("appendedTransaction");
      const html = updated.getHTML();
      if (
        !initialized.current ||
        applyingValue.current ||
        transaction.getMeta("preventUpdate") ||
        appended?.getMeta("preventUpdate") ||
        html === lastHTML.current
      )
        return;
      lastHTML.current = html;
      onChange(html, updated.getJSON());
    },
  });

  useEffect(() => {
    editor?.setEditable(editable);
  }, [editor, editable]);
  useEffect(() => {
    if (editor && !editor.isFocused && editor.getHTML() !== value) {
      applyingValue.current = true;
      editor.commands.setContent(value, { emitUpdate: false });
      lastHTML.current = editor.getHTML();
      applyingValue.current = false;
    }
  }, [editor, value]);

  if (!editor) return <div className="editor-loading" />;

  const addLink = () => {
    const previous = editor.getAttributes("link").href as string | undefined;
    const url = window.prompt(
      appStore.t("链接地址（https://…）", "Link URL (https://…)"),
      previous ?? "https://",
    );
    if (url === null) return;
    if (!url) editor.chain().focus().extendMarkRange("link").unsetLink().run();
    else if (/^https?:\/\//i.test(url))
      editor
        .chain()
        .focus()
        .extendMarkRange("link")
        .setLink({ href: url })
        .run();
  };

  const tools = [
    {
      icon: <Bold size={14} />,
      label: appStore.t("粗体", "Bold"),
      active: editor.isActive("bold"),
      run: () => editor.chain().focus().toggleBold().run(),
    },
    {
      icon: <Italic size={14} />,
      label: appStore.t("斜体", "Italic"),
      active: editor.isActive("italic"),
      run: () => editor.chain().focus().toggleItalic().run(),
    },
    {
      icon: <Strikethrough size={14} />,
      label: appStore.t("删除线", "Strikethrough"),
      active: editor.isActive("strike"),
      run: () => editor.chain().focus().toggleStrike().run(),
    },
    {
      icon: <Heading2 size={15} />,
      label: appStore.t("标题", "Heading"),
      active: editor.isActive("heading"),
      run: () => editor.chain().focus().toggleHeading({ level: 2 }).run(),
    },
    {
      icon: <List size={15} />,
      label: appStore.t("项目列表", "Bullet list"),
      active: editor.isActive("bulletList"),
      run: () => editor.chain().focus().toggleBulletList().run(),
    },
    {
      icon: <ListOrdered size={15} />,
      label: appStore.t("有序列表", "Ordered list"),
      active: editor.isActive("orderedList"),
      run: () => editor.chain().focus().toggleOrderedList().run(),
    },
    {
      icon: <ListChecks size={15} />,
      label: appStore.t("待办清单", "Checklist"),
      active: editor.isActive("taskList"),
      run: () => editor.chain().focus().toggleTaskList().run(),
    },
    {
      icon: <Quote size={14} />,
      label: appStore.t("引用", "Quote"),
      active: editor.isActive("blockquote"),
      run: () => editor.chain().focus().toggleBlockquote().run(),
    },
    {
      icon: <Code size={15} />,
      label: appStore.t("代码块", "Code block"),
      active: editor.isActive("codeBlock"),
      run: () => editor.chain().focus().toggleCodeBlock().run(),
    },
    {
      icon: <Link size={14} />,
      label: appStore.t("链接", "Link"),
      active: editor.isActive("link"),
      run: addLink,
    },
  ];

  return (
    <div
      className={cn(
        "rich-editor",
        compact && "rich-editor-compact",
        !editable && "rich-editor-readonly",
        className,
      )}
    >
      {editable && (
        <div className="editor-toolbar">
          {tools.map((tool) => (
            <Tooltip key={tool.label} label={tool.label}>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className={tool.active ? "is-active" : ""}
                onClick={tool.run}
                aria-label={tool.label}
              >
                {tool.icon}
              </Button>
            </Tooltip>
          ))}
          {!compact && (
            <>
              <span className="toolbar-divider" />
              <Tooltip label={appStore.t("插入表格", "Insert table")}>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={appStore.t("插入表格", "Insert table")}
                  onClick={() =>
                    editor
                      .chain()
                      .focus()
                      .insertTable({ rows: 3, cols: 3, withHeaderRow: true })
                      .run()
                  }
                >
                  <Table size={14} />
                </Button>
              </Tooltip>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={appStore.t("撤销", "Undo")}
                onClick={() => editor.chain().focus().undo().run()}
              >
                <Undo2 size={14} />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={appStore.t("重做", "Redo")}
                onClick={() => editor.chain().focus().redo().run()}
              >
                <Redo2 size={14} />
              </Button>
            </>
          )}
          <AdvancedEditorTools
            editor={editor}
            projectId={projectId}
            workItemId={workItemId}
          />
        </div>
      )}
      <EditorContent editor={editor} />
    </div>
  );
});
