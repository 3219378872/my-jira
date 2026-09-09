import { useEffect, useState } from "react";
import { observer } from "mobx-react-lite";
import {
  HocuspocusProvider,
  HocuspocusProviderWebsocket,
} from "@hocuspocus/provider";
import * as Y from "yjs";
import { EditorContent, useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import Collaboration from "@tiptap/extension-collaboration";
import CollaborationCaret from "@tiptap/extension-collaboration-caret";
import Placeholder from "@tiptap/extension-placeholder";
import TaskList from "@tiptap/extension-task-list";
import TaskItem from "@tiptap/extension-task-item";
import { TableKit } from "@tiptap/extension-table";
import Image from "@tiptap/extension-image";
import {
  Bold,
  CheckCheck,
  Code,
  Heading2,
  Italic,
  Link2,
  List,
  ListChecks,
  ListOrdered,
  Quote,
  Redo2,
  Table2,
  Undo2,
  Users,
  WifiOff,
} from "lucide-react";
import { appStore } from "../stores/app-store";
import { obtainCSRF } from "../lib/api";
import { Button, ErrorBox, Loading, Tooltip } from "./ui";
import { cn } from "../lib/utils";
import { webEditorBlocks } from "./editor-schema";
import { AdvancedEditorTools } from "./editor-tools";

export interface PageRealtimeUpdate {
  page_id: string;
  version?: number;
  name?: string;
  is_locked?: boolean;
  archived_at?: string | null;
  can_edit?: boolean;
}

export const CollaborativeEditor = observer(function CollaborativeEditor({
  workspaceId,
  projectId,
  pageId,
  locked,
}: {
  workspaceId: string;
  projectId?: string;
  pageId: string;
  locked: boolean;
}) {
  const [connection, setConnection] = useState<{
    provider: HocuspocusProvider;
    document: Y.Doc;
  } | null>(null);
  const [status, setStatus] = useState("connecting");
  const [synced, setSynced] = useState(false);
  const [saved, setSaved] = useState(true);
  const [canEdit, setCanEdit] = useState(false);
  const [generation, setGeneration] = useState(0);
  const [error, setError] = useState("");
  const t = appStore.t;

  useEffect(() => {
    const document = new Y.Doc();
    let disposed = false;
    let initialSync = false;
    setSynced(false);
    setSaved(true);
    setCanEdit(false);
    setStatus(navigator.onLine ? "connecting" : "disconnected");
    setError("");
    const notify = (data: Omit<PageRealtimeUpdate, "page_id">) =>
      window.dispatchEvent(
        new CustomEvent<PageRealtimeUpdate>("myjira:page-updated", {
          detail: { page_id: pageId, ...data },
        }),
      );
    const websocket = new HocuspocusProviderWebsocket({
      url: `${window.location.protocol === "https:" ? "wss:" : "ws:"}//${window.location.host}/live`,
      autoConnect: navigator.onLine,
    });
    const provider = new HocuspocusProvider({
      websocketProvider: websocket,
      name: `${workspaceId}:${projectId || "workspace"}:${pageId}`,
      document,
      token: async () => {
        const cookie = window.document.cookie
          .split("; ")
          .find((value) => value.startsWith("mj_csrf="));
        return JSON.stringify({
          csrf_token: cookie
            ? decodeURIComponent(cookie.slice(8))
            : await obtainCSRF(),
        });
      },
      onStatus: ({ status: next }) => {
        if (!disposed) setStatus(next);
      },
      onClose: ({ event }) => {
        if (disposed) return;
        // Hocuspocus may close only this document while its shared socket stays
        // open. Its synced=false transition does not emit an onSynced event.
        initialSync = false;
        setCanEdit(false);
        setSynced(false);
        setStatus("disconnected");
        notify({ can_edit: false });
        if (event.reason.includes("Document access"))
          setError(
            t(
              "你已无法访问这篇文档。",
              "You no longer have access to this document.",
            ),
          );
      },
      onSynced: ({ state }) => {
        if (!disposed) {
          initialSync = state;
          setSynced(state);
        }
      },
      onAuthenticationFailed: ({ reason }) => {
        if (disposed) return;
        setCanEdit(false);
        setError(
          reason || t("文档访问验证失败。", "Document authentication failed."),
        );
      },
      onStateless: ({ payload }) => {
        if (disposed) return;
        let message: Record<string, unknown>;
        try {
          message = JSON.parse(payload);
        } catch {
          return;
        }
        if (!message || typeof message !== "object") return;
        if (message.type === "permissions") {
          const can_edit = navigator.onLine && message.can_edit === true;
          setCanEdit(can_edit);
          notify({ can_edit });
        } else if (message.type === "reload_required") {
          // A restored snapshot must start with a new CRDT document. Reusing the
          // old Y.Doc would merge edits from the replaced version back into it.
          disposed = true;
          setConnection(null);
          setCanEdit(false);
          notify({ can_edit: false });
          provider.destroy();
          websocket.destroy();
          document.destroy();
          setGeneration((value) => value + 1);
        } else if (message.type === "saved" || message.type === "metadata") {
          const metadata: Omit<PageRealtimeUpdate, "page_id"> = {};
          if (typeof message.version === "number")
            metadata.version = message.version;
          if (typeof message.name === "string") metadata.name = message.name;
          if (typeof message.is_locked === "boolean")
            metadata.is_locked = message.is_locked;
          if (
            message.archived_at === null ||
            typeof message.archived_at === "string"
          )
            metadata.archived_at = message.archived_at;
          if (message.type === "saved") setSaved(true);
          notify(metadata);
        }
      },
    });
    // Externally owned sockets do not attach document providers automatically.
    // Attach before the first WebSocket event so auth, sync and permissions flow.
    provider.attach();
    const offline = () => {
      if (disposed) return;
      setCanEdit(false);
      setStatus("disconnected");
      setSynced(false);
      initialSync = false;
      notify({ can_edit: false });
      websocket.disconnect();
    };
    const online = () => {
      if (disposed) return;
      setCanEdit(false);
      setStatus("connecting");
      setError("");
      websocket.connect().catch(() => {
        if (!disposed) setStatus("disconnected");
      });
    };
    window.addEventListener("offline", offline);
    window.addEventListener("online", online);
    document.on("update", (_update, _origin, _document, transaction) => {
      if (!disposed && initialSync && transaction.local) setSaved(false);
    });
    setConnection({ provider, document });
    return () => {
      window.removeEventListener("offline", offline);
      window.removeEventListener("online", online);
      if (!disposed) {
        disposed = true;
        provider.destroy();
        websocket.destroy();
        document.destroy();
      }
    };
  }, [workspaceId, projectId, pageId, generation]);

  return (
    <div className="collaborative-document">
      <div className="collaboration-status">
        <span
          className={cn(
            "connection-indicator",
            status === "connected" && "connected",
          )}
        />
        {status === "connected" ? (
          <>
            <Users size={13} />
            {t("协作连接已建立", "Collaboration connected")}
          </>
        ) : status === "connecting" ? (
          t("正在连接协作服务…", "Connecting to collaboration…")
        ) : (
          <>
            <WifiOff size={13} />
            {t("连接已中断，正在重连", "Disconnected, reconnecting")}
          </>
        )}
        {synced && (
          <span className="synced-indicator">
            <CheckCheck size={13} />
            {saved
              ? t("所有更改已保存", "All changes saved")
              : t("正在保存…", "Saving changes…")}
          </span>
        )}
      </div>
      <ErrorBox message={error} />
      {connection ? (
        <CollaborationContent
          key={generation}
          provider={connection.provider}
          document={connection.document}
          projectId={projectId}
          pageId={pageId}
          editable={canEdit && !locked && synced && status === "connected"}
        />
      ) : (
        <Loading />
      )}
    </div>
  );
});

const CollaborationContent = observer(function CollaborationContent({
  provider,
  document,
  editable,
  projectId,
  pageId,
}: {
  provider: HocuspocusProvider;
  document: Y.Doc;
  editable: boolean;
  projectId?: string;
  pageId: string;
}) {
  const t = appStore.t;
  const editor = useEditor(
    {
      extensions: [
        StarterKit.configure({ undoRedo: false, link: { openOnClick: false } }),
        Collaboration.configure({ document, field: "default" }),
        CollaborationCaret.configure({
          provider,
          user: {
            name: appStore.user?.display_name || "Member",
            color: "#6977ca",
          },
        }),
        Placeholder.configure({
          placeholder: t(
            "记录你的想法，或用工具栏添加丰富的内容…",
            "Capture your thoughts, or use the toolbar to add rich content…",
          ),
        }),
        TaskList,
        TaskItem.configure({ nested: true }),
        TableKit,
        Image,
        ...webEditorBlocks,
      ],
      editable,
      immediatelyRender: false,
    },
    [provider, document],
  );
  useEffect(() => {
    editor?.setEditable(editable);
  }, [editor, editable]);
  if (!editor) return <Loading />;
  const tools = [
    {
      icon: Bold,
      label: t("粗体", "Bold"),
      active: editor.isActive("bold"),
      run: () => editor.chain().focus().toggleBold().run(),
    },
    {
      icon: Italic,
      label: t("斜体", "Italic"),
      active: editor.isActive("italic"),
      run: () => editor.chain().focus().toggleItalic().run(),
    },
    {
      icon: Heading2,
      label: t("标题", "Heading"),
      active: editor.isActive("heading"),
      run: () => editor.chain().focus().toggleHeading({ level: 2 }).run(),
    },
    {
      icon: List,
      label: t("项目列表", "Bullet list"),
      active: editor.isActive("bulletList"),
      run: () => editor.chain().focus().toggleBulletList().run(),
    },
    {
      icon: ListOrdered,
      label: t("有序列表", "Ordered list"),
      active: editor.isActive("orderedList"),
      run: () => editor.chain().focus().toggleOrderedList().run(),
    },
    {
      icon: ListChecks,
      label: t("待办清单", "Checklist"),
      active: editor.isActive("taskList"),
      run: () => editor.chain().focus().toggleTaskList().run(),
    },
    {
      icon: Quote,
      label: t("引用", "Quote"),
      active: editor.isActive("blockquote"),
      run: () => editor.chain().focus().toggleBlockquote().run(),
    },
    {
      icon: Code,
      label: t("代码块", "Code block"),
      active: editor.isActive("codeBlock"),
      run: () => editor.chain().focus().toggleCodeBlock().run(),
    },
    {
      icon: Table2,
      label: t("表格", "Table"),
      active: editor.isActive("table"),
      run: () =>
        editor
          .chain()
          .focus()
          .insertTable({ rows: 3, cols: 3, withHeaderRow: true })
          .run(),
    },
    {
      icon: Link2,
      label: t("链接", "Link"),
      active: editor.isActive("link"),
      run: () => {
        const url = window.prompt(t("输入链接地址", "Enter a URL"), "https://");
        if (url && /^https?:\/\//i.test(url))
          editor.chain().focus().setLink({ href: url }).run();
      },
    },
    {
      icon: Undo2,
      label: t("撤销", "Undo"),
      active: false,
      run: () => editor.chain().focus().undo().run(),
    },
    {
      icon: Redo2,
      label: t("重做", "Redo"),
      active: false,
      run: () => editor.chain().focus().redo().run(),
    },
  ];
  return (
    <div className="rich-editor document-editor">
      <div className="editor-toolbar">
        {tools.map((tool) => (
          <Tooltip key={tool.label} label={tool.label}>
            <Button
              variant="ghost"
              size="icon"
              disabled={!editable}
              className={tool.active ? "is-active" : ""}
              onClick={tool.run}
              aria-label={tool.label}
            >
              <tool.icon size={15} />
            </Button>
          </Tooltip>
        ))}
        <AdvancedEditorTools
          editor={editor}
          disabled={!editable}
          projectId={projectId}
          pageId={pageId}
        />
      </div>
      <EditorContent editor={editor} />
    </div>
  );
});
