import { useEffect, useRef, useState, type FormEvent } from "react";
import { observer } from "mobx-react-lite";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  Archive,
  ArrowLeft,
  BookOpen,
  ChevronRight,
  Clock3,
  Copy,
  Download,
  FileText,
  History,
  Lock,
  MessageSquare,
  Plus,
  GitBranch,
  Link2,
  ListTree,
  Upload,
  Search,
  Trash2,
  Unlock,
  X,
} from "lucide-react";
import { appStore } from "../stores/app-store";
import { api, APIError, projectPath, request, workspacePath } from "../lib/api";
import { useMutation, useRemote, useScope } from "../lib/hooks";
import { dateTime, safeHTML } from "../lib/utils";
import {
  Badge,
  Button,
  Confirm,
  EmptyState,
  ErrorBox,
  Field,
  Input,
  Loading,
  Menu,
  Modal,
  PageHeader,
  Select,
} from "../components/ui";
import {
  CollaborativeEditor,
  type PageRealtimeUpdate,
} from "../components/collaborative-editor";
import type { Comment, PageDocument } from "../types";
import { useWorkspacePreference } from "../lib/preferences";
import { FavoriteButton } from "../components/favorite-button";

interface PageVersion {
  id: string;
  name: string;
  version: number;
  created_at: string;
  content_html?: string;
}

export const PagesPage = observer(function PagesPage() {
  const { workspace, project } = useScope();
  const documentPreference = useWorkspacePreference("documents", {
    width: "standard",
  });
  const { pageId } = useParams();
  const navigate = useNavigate();
  const [createOpen, setCreateOpen] = useState(false);
  const [newName, setNewName] = useState("");
  const [privatePage, setPrivatePage] = useState(false);
  const [createParent, setCreateParent] = useState("");
  const [movingPage, setMovingPage] = useState<PageDocument | null>(null);
  const [moveParent, setMoveParent] = useState("");
  const [editingComment, setEditingComment] = useState<Comment | null>(null);
  const [replyTo, setReplyTo] = useState<Comment | null>(null);
  const [previewVersion, setPreviewVersion] = useState<PageVersion | null>(
    null,
  );
  const [search, setSearch] = useState("");
  const [archived, setArchived] = useState(false);
  const [name, setName] = useState("");
  const [historyOpen, setHistoryOpen] = useState(false);
  const [commentsOpen, setCommentsOpen] = useState(false);
  const [resourcesOpen, setResourcesOpen] = useState(false);
  const [comment, setComment] = useState("");
  const [deleting, setDeleting] = useState<PageDocument | null>(null);
  const [editorKey, setEditorKey] = useState(0);
  const [canEdit, setCanEdit] = useState(false);
  const titleEditing = useRef(false);
  const titleDirty = useRef(false);
  const collection = `${project ? projectPath(workspace.id, project.id) : workspacePath(workspace.id)}/pages`;
  const route = `/w/${workspace.slug}${project ? `/projects/${project.id}` : ""}/pages`;
  const pages = useRemote<PageDocument[]>(`${collection}?archived=${archived}`);
  const detail = useRemote<PageDocument>(
    pageId ? `${collection}/${pageId}` : null,
  );
  const versions = useRemote<PageVersion[]>(
    pageId && historyOpen ? `${collection}/${pageId}/versions` : null,
  );
  const comments = useRemote<Comment[]>(
    pageId && commentsOpen ? `${collection}/${pageId}/comments` : null,
  );
  const versionPreview = useRemote<PageVersion>(
    pageId && previewVersion
      ? `${collection}/${pageId}/versions/${previewVersion.id}`
      : null,
  );
  const summary = useRemote<{
    public_pages: number;
    private_pages: number;
    archived_pages: number;
    total: number;
  }>(!pageId ? `${collection}/summary` : null);
  const resources = useRemote<{
    links: { url: string; text: string; kind: string }[];
    headings: { id: string; text: string; level: number }[];
    assets: { id: string; filename: string; download_url: string }[];
  }>(
    pageId && resourcesOpen ? `${collection}/${pageId}/resources` : null,
    detail.data?.version ?? 0,
  );
  const mutation = useMutation();
  const t = appStore.t;
  useEffect(() => {
    titleEditing.current = false;
    titleDirty.current = false;
    setCanEdit(false);
  }, [pageId]);
  useEffect(() => {
    if (detail.data && !titleEditing.current && !titleDirty.current)
      setName(detail.data.name);
  }, [detail.data?.id, detail.data?.name]);
  useEffect(() => {
    const receive = (event: Event) => {
      const update = (event as CustomEvent<PageRealtimeUpdate>).detail;
      if (update.page_id !== pageId) return;
      if (typeof update.can_edit === "boolean") setCanEdit(update.can_edit);
      const { page_id: _pageID, can_edit: _permission, ...metadata } = update;
      detail.setResult((current) =>
        current
          ? { ...current, data: { ...current.data, ...metadata } }
          : current,
      );
      pages.setResult((current) =>
        current
          ? {
              ...current,
              data: current.data.map((page) =>
                page.id === pageId ? { ...page, ...metadata } : page,
              ),
            }
          : current,
      );
    };
    window.addEventListener("myjira:page-updated", receive);
    return () => window.removeEventListener("myjira:page-updated", receive);
  }, [pageId, detail.setResult, pages.setResult]);
  useEffect(() => {
    if (pageId)
      api
        .post(`${workspacePath(workspace.id)}/recent-visits`, {
          entity_type: "page",
          entity_id: pageId,
        })
        .catch(() => {});
  }, [pageId, workspace.id]);

  const create = async (event: FormEvent) => {
    event.preventDefault();
    const result = await mutation.execute(() =>
      api.post<PageDocument>(collection, {
        name: newName,
        is_private: privatePage,
        content_html: "<p></p>",
        content_json: { type: "doc", content: [{ type: "paragraph" }] },
        parent_id: createParent || null,
      }),
    );
    if (result) {
      setCreateOpen(false);
      setNewName("");
      setCreateParent("");
      pages.refresh();
      navigate(`${route}/${result.data.id}`);
    }
  };
  const patchMetadata = async (id: string, data: Record<string, unknown>) => {
    // Collaboration autosaves also advance the version. Fetch it immediately
    // before a metadata write and retry an intervening autosave once.
    for (let attempt = 0; attempt < 2; attempt++) {
      const current = await api.get<PageDocument>(`${collection}/${id}`);
      try {
        return await api.patch<PageDocument>(`${collection}/${id}`, {
          ...data,
          version: current.data.version,
        });
      } catch (cause) {
        if (
          !(cause instanceof APIError) ||
          cause.status !== 409 ||
          attempt === 1
        )
          throw cause;
      }
    }
    throw new Error(
      t(
        "文档版本发生变化，请重试。",
        "The document changed. Please try again.",
      ),
    );
  };
  const update = (data: Record<string, unknown>) =>
    mutation.execute(async () => {
      const result = await patchMetadata(pageId!, data);
      titleDirty.current = false;
      detail.setResult(result);
      pages.setResult((current) =>
        current
          ? {
              ...current,
              data: current.data.map((page) =>
                page.id === result.data.id ? result.data : page,
              ),
            }
          : current,
      );
    });
  const downloadPDF = () =>
    mutation.execute(async () => {
      const documentName = `${workspace.id}:${project?.id || "workspace"}:${pageId}`;
      const response = await fetch(
        `/live/documents/${encodeURIComponent(documentName)}/pdf`,
        { credentials: "include" },
      );
      if (
        !response.ok ||
        !response.headers.get("content-type")?.includes("application/pdf")
      ) {
        throw new Error(
          t(
            "PDF 导出失败，请稍后重试。",
            "PDF export failed. Please try again.",
          ),
        );
      }
      const url = URL.createObjectURL(await response.blob());
      const link = document.createElement("a");
      link.href = url;
      link.download = `${name.replace(/[/\\:*?"<>|]/g, "_") || "document"}.pdf`;
      link.click();
      window.setTimeout(() => URL.revokeObjectURL(url), 30_000);
    });
  const restoreVersion = (version: PageVersion) =>
    mutation.execute(
      async () => {
        const current = await api.get<PageDocument>(`${collection}/${pageId}`);
        await api.post(
          `${collection}/${pageId}/versions/${version.id}/restore`,
          { version: current.data.version },
        );
        detail.refresh();
        versions.refresh();
        setEditorKey((value) => value + 1);
      },
      t("已恢复为新的文档版本", "Restored as a new document version"),
    );

  const menu = (page: PageDocument) => [
    {
      label: t("创建子文档", "Create child document"),
      icon: <Plus size={14} />,
      onSelect: () => {
        setCreateParent(page.id);
        setCreateOpen(true);
      },
    },
    {
      label: t("移动文档", "Move document"),
      icon: <GitBranch size={14} />,
      onSelect: () => {
        setMovingPage(page);
        setMoveParent(page.parent_id ?? "");
      },
    },
    ...(page.owner_id === appStore.user?.id
      ? [
          {
            label: page.is_private
              ? t("设为团队文档", "Share with the team")
              : t("设为私有文档", "Make private"),
            icon: page.is_private ? <Unlock size={14} /> : <Lock size={14} />,
            onSelect: () =>
              mutation.execute(async () => {
                const result = await patchMetadata(page.id, {
                  is_private: !page.is_private,
                });
                if (page.id === pageId) detail.setResult(result);
                pages.refresh();
              }),
          },
        ]
      : []),
    {
      label: t("复制文档", "Duplicate document"),
      icon: <Copy size={14} />,
      onSelect: () => {
        mutation.execute(async () => {
          const result = await api.post<PageDocument>(
            `${collection}/${page.id}/duplicate`,
          );
          pages.refresh();
          navigate(`${route}/${result.data.id}`);
        });
      },
    },
    {
      label: page.is_locked
        ? t("解除锁定", "Unlock document")
        : t("锁定文档", "Lock document"),
      icon: page.is_locked ? <Unlock size={14} /> : <Lock size={14} />,
      onSelect: () => {
        mutation.execute(async () => {
          await patchMetadata(page.id, {
            is_locked: !page.is_locked,
          });
          detail.refresh();
          pages.refresh();
        });
      },
    },
    {
      label: page.archived_at
        ? t("恢复文档", "Restore document")
        : t("归档文档", "Archive document"),
      icon: <Archive size={14} />,
      onSelect: () => {
        mutation.execute(async () => {
          await patchMetadata(page.id, {
            archived: !page.archived_at,
          });
          detail.refresh();
          pages.refresh();
        });
      },
    },
    ...(page.archived_at
      ? [
          {
            label: t("删除文档", "Delete document"),
            icon: <Trash2 size={14} />,
            danger: true,
            onSelect: () => setDeleting(page),
          },
        ]
      : []),
  ];

  const list = (pages.data ?? []).filter((page) =>
    page.name.toLowerCase().includes(search.toLowerCase()),
  );
  const tree = orderPages(list);
  const invalidParents = movingPage
    ? descendantIDs(pages.data ?? [], movingPage.id)
    : new Set<string>();
  return (
    <div className={pageId ? "documents-layout" : "page-scroll"}>
      {pageId ? (
        <>
          <aside className="document-nav">
            <header>
              <Link to={route}>
                <ArrowLeft size={14} />
                {t("全部文档", "All documents")}
              </Link>
              <Button
                variant="ghost"
                size="icon"
                aria-label={t("新建文档", "New document")}
                onClick={() => setCreateOpen(true)}
              >
                <Plus size={15} />
              </Button>
            </header>
            {tree.map(({ page, depth }) => (
              <Link
                key={page.id}
                to={`${route}/${page.id}`}
                className={page.id === pageId ? "active" : ""}
                style={{ paddingLeft: 10 + depth * 14 }}
              >
                <FileText size={14} />
                <span>{page.name}</span>
                {page.is_private && <Lock size={12} />}
              </Link>
            ))}
          </aside>
          <div className="document-main">
            {detail.loading ? (
              <Loading />
            ) : detail.error ? (
              <ErrorBox message={detail.error} retry={detail.refresh} />
            ) : (
              detail.data && (
                <>
                  <div className="document-actions">
                    <div className="inline-property">
                      <FileText size={15} />
                      <Badge>
                        {detail.data.is_private
                          ? t("私有文档", "Private document")
                          : t("团队文档", "Team document")}
                      </Badge>
                      {detail.data.is_locked && (
                        <Badge>
                          <Lock size={12} />
                          {t("已锁定", "Locked")}
                        </Badge>
                      )}
                      {detail.data.archived_at && (
                        <Badge>{t("已归档", "Archived")}</Badge>
                      )}
                    </div>
                    <div className="inline-actions">
                      <Select
                        aria-label={t("文档宽度", "Document width")}
                        value={documentPreference.value.width}
                        disabled={documentPreference.loading}
                        onChange={(event) =>
                          mutation.execute(() =>
                            documentPreference.save({
                              width: event.target.value,
                            }),
                          )
                        }
                      >
                        <option value="standard">
                          {t("标准宽度", "Standard width")}
                        </option>
                        <option value="wide">{t("宽版", "Wide")}</option>
                        <option value="full">{t("全宽", "Full width")}</option>
                      </Select>
                      <Button
                        size="icon"
                        variant="ghost"
                        aria-label={t("大纲与资源", "Outline and resources")}
                        onClick={() => setResourcesOpen(!resourcesOpen)}
                      >
                        <ListTree size={16} />
                      </Button>
                      <Button
                        size="icon"
                        variant="ghost"
                        aria-label={t("导出 PDF", "Export PDF")}
                        busy={mutation.busy}
                        onClick={downloadPDF}
                      >
                        <Download size={15} />
                      </Button>
                      <FavoriteButton entityType="page" entityId={pageId} />
                      <Button
                        size="icon"
                        variant="ghost"
                        aria-label={t("文档历史", "Document history")}
                        onClick={() => setHistoryOpen(!historyOpen)}
                      >
                        <History size={16} />
                      </Button>
                      <Button
                        size="icon"
                        variant="ghost"
                        aria-label={t("文档评论", "Document comments")}
                        onClick={() => setCommentsOpen(!commentsOpen)}
                      >
                        <MessageSquare size={16} />
                      </Button>
                      <Menu items={menu(detail.data)} />
                    </div>
                  </div>
                  <div
                    className="document-paper"
                    style={{
                      maxWidth:
                        documentPreference.value.width === "full"
                          ? "none"
                          : documentPreference.value.width === "wide"
                            ? 1120
                            : undefined,
                    }}
                  >
                    {detail.data.parent_id && (
                      <Link
                        className="inline-property settings-note"
                        to={`${route}/${detail.data.parent_id}`}
                      >
                        <GitBranch size={13} />
                        {pages.data?.find(
                          (page) => page.id === detail.data!.parent_id,
                        )?.name ?? t("父文档", "Parent document")}
                      </Link>
                    )}
                    <button
                      type="button"
                      className="document-icon"
                      disabled={!canEdit}
                      aria-label={t("更改文档图标", "Change document icon")}
                      onClick={() => {
                        const icon = window.prompt(
                          t(
                            "输入一个表情图标（留空使用默认图标）",
                            "Enter an emoji (leave empty for the default icon)",
                          ),
                          detail.data!.icon ?? "",
                        );
                        if (icon !== null)
                          update({ icon: icon.trim().slice(0, 20) });
                      }}
                    >
                      {detail.data.icon || <BookOpen size={32} />}
                    </button>
                    <Input
                      className="document-title"
                      aria-label={t("文档标题", "Document title")}
                      value={name}
                      disabled={
                        !canEdit ||
                        detail.data.is_locked ||
                        !!detail.data.archived_at
                      }
                      onFocus={() => {
                        titleEditing.current = true;
                      }}
                      onChange={(event) => {
                        titleDirty.current = true;
                        setName(event.target.value);
                      }}
                      onBlur={() => {
                        titleEditing.current = false;
                        if (name.trim() && name !== detail.data!.name)
                          update({ name: name.trim() });
                        else {
                          titleDirty.current = false;
                          setName(detail.data!.name);
                        }
                      }}
                    />
                    <ErrorBox message={mutation.error} />
                    <CollaborativeEditor
                      key={`${pageId}-${editorKey}`}
                      workspaceId={workspace.id}
                      projectId={project?.id}
                      pageId={pageId}
                      locked={
                        detail.data.is_locked || !!detail.data.archived_at
                      }
                    />
                  </div>
                </>
              )
            )}
          </div>
          {commentsOpen && (
            <aside className="document-side-panel">
              <header>
                <h3>{t("文档评论", "Comments")}</h3>
                <Button
                  size="icon"
                  variant="ghost"
                  onClick={() => setCommentsOpen(false)}
                  aria-label={t("关闭评论", "Close comments")}
                >
                  <X size={15} />
                </Button>
              </header>
              <ErrorBox message={comments.error || mutation.error} />
              {comments.data?.map((entry) => (
                <article
                  key={entry.id}
                  className={
                    entry.parent_id
                      ? "page-comment comment-reply"
                      : "page-comment"
                  }
                >
                  <div
                    className="prose-content"
                    dangerouslySetInnerHTML={{
                      __html: safeHTML(entry.body_html),
                    }}
                  />
                  <time>{dateTime(entry.created_at, appStore.locale)}</time>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => {
                      setReplyTo(entry);
                      setEditingComment(null);
                    }}
                  >
                    {t("回复", "Reply")}
                  </Button>
                  {entry.author_id === appStore.user?.id && (
                    <>
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => {
                          const element = document.createElement("div");
                          element.innerHTML = safeHTML(entry.body_html);
                          setComment(element.textContent ?? "");
                          setEditingComment(entry);
                          setReplyTo(null);
                        }}
                      >
                        {t("编辑", "Edit")}
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => {
                          mutation.execute(async () => {
                            await api.delete(
                              `${collection}/${pageId}/comments/${entry.id}`,
                            );
                            comments.refresh();
                          });
                        }}
                      >
                        {t("删除", "Delete")}
                      </Button>
                    </>
                  )}
                </article>
              ))}
              <form
                onSubmit={(event) => {
                  event.preventDefault();
                  mutation.execute(async () => {
                    const element = document.createElement("div");
                    element.textContent = comment;
                    const body = {
                      body_html: `<p>${element.innerHTML}</p>`,
                      parent_id: replyTo?.id ?? null,
                    };
                    if (editingComment)
                      await api.patch(
                        `${collection}/${pageId}/comments/${editingComment.id}`,
                        { body_html: body.body_html },
                      );
                    else
                      await api.post(`${collection}/${pageId}/comments`, body);
                    setComment("");
                    setEditingComment(null);
                    setReplyTo(null);
                    comments.refresh();
                  });
                }}
                className="form-stack"
              >
                {(editingComment || replyTo) && (
                  <div className="inline-property settings-note">
                    {editingComment
                      ? t("正在编辑评论", "Editing comment")
                      : t("正在回复评论", "Replying to comment")}
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      aria-label={t("取消", "Cancel")}
                      onClick={() => {
                        setEditingComment(null);
                        setReplyTo(null);
                        setComment("");
                      }}
                    >
                      <X size={12} />
                    </Button>
                  </div>
                )}
                <textarea
                  className="input textarea"
                  value={comment}
                  onChange={(event) => setComment(event.target.value)}
                  placeholder={t("留下你的想法…", "Leave a thought…")}
                  rows={4}
                  required
                />
                <Button variant="primary" type="submit" busy={mutation.busy}>
                  {editingComment
                    ? t("保存评论", "Save comment")
                    : t("发表评论", "Post comment")}
                </Button>
              </form>
            </aside>
          )}
          {historyOpen && (
            <aside className="document-side-panel">
              <header>
                <h3>{t("版本历史", "Version history")}</h3>
                <Button
                  size="icon"
                  variant="ghost"
                  onClick={() => setHistoryOpen(false)}
                  aria-label={t("关闭历史", "Close history")}
                >
                  <X size={15} />
                </Button>
              </header>
              <ErrorBox message={versions.error} />
              {versions.loading ? (
                <Loading />
              ) : (
                versions.data?.map((version) => (
                  <div key={version.id} className="version-item">
                    <Clock3 size={15} />
                    <div>
                      <strong>{version.name}</strong>
                      <span>
                        {dateTime(version.created_at, appStore.locale)}
                      </span>
                      <Badge>v{version.version}</Badge>
                    </div>
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => setPreviewVersion(version)}
                    >
                      {t("预览", "Preview")}
                    </Button>
                    <Button
                      size="sm"
                      disabled={mutation.busy}
                      onClick={() => restoreVersion(version)}
                    >
                      {t("恢复", "Restore")}
                    </Button>
                  </div>
                ))
              )}
            </aside>
          )}
          {resourcesOpen && (
            <aside className="document-side-panel">
              <header>
                <h3>{t("大纲与资源", "Outline and resources")}</h3>
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label={t("关闭资源面板", "Close resources")}
                  onClick={() => setResourcesOpen(false)}
                >
                  <X size={15} />
                </Button>
              </header>
              <ErrorBox message={resources.error || mutation.error} />
              {resources.loading ? (
                <Loading />
              ) : (
                <>
                  <h4 className="settings-note">{t("文档大纲", "Outline")}</h4>
                  {resources.data?.headings.map((heading, index) => (
                    <button
                      className="document-outline-link"
                      key={`${heading.id}-${index}`}
                      style={{ paddingLeft: (heading.level - 1) * 12 }}
                      onClick={() => {
                        const headings = [
                          ...document.querySelectorAll<HTMLElement>(
                            ".document-paper .tiptap h1, .document-paper .tiptap h2, .document-paper .tiptap h3, .document-paper .tiptap h4, .document-paper .tiptap h5, .document-paper .tiptap h6",
                          ),
                        ];
                        (
                          headings[index] ??
                          headings.find(
                            (item) => item.textContent === heading.text,
                          )
                        )?.scrollIntoView({
                          block: "center",
                          behavior: "smooth",
                        });
                      }}
                    >
                      {heading.text}
                    </button>
                  ))}
                  <h4 className="settings-note">
                    {t("链接与嵌入", "Links and embeds")}
                  </h4>
                  {resources.data?.links.map((link, index) => (
                    <a
                      className="document-resource-link"
                      href={/^https?:\/\//i.test(link.url) ? link.url : "#"}
                      target="_blank"
                      rel="noreferrer"
                      key={`${link.url}-${index}`}
                    >
                      <Link2 size={13} />
                      <span>{link.text || link.url}</span>
                    </a>
                  ))}
                  <h4 className="settings-note">
                    {t("文档附件", "Attachments")}
                  </h4>
                  {resources.data?.assets.map((asset) => (
                    <div className="attachment-row" key={asset.id}>
                      <FileText size={14} />
                      <a
                        href={asset.download_url}
                        target="_blank"
                        rel="noreferrer"
                      >
                        {asset.filename}
                      </a>
                      {canEdit && (
                        <Button
                          size="icon"
                          variant="ghost"
                          aria-label={t("删除附件", "Delete attachment")}
                          onClick={() =>
                            mutation.execute(async () => {
                              await api.delete(
                                `${project ? projectPath(workspace.id, project.id) : workspacePath(workspace.id)}/assets/${asset.id}`,
                              );
                              resources.refresh();
                            })
                          }
                        >
                          <X size={12} />
                        </Button>
                      )}
                    </div>
                  ))}
                </>
              )}
              {canEdit && (
                <label className="button button-secondary button-md upload-button">
                  <Upload size={13} />
                  {t("上传文档附件", "Upload attachment")}
                  <input
                    type="file"
                    onChange={(event) => {
                      const file = event.target.files?.[0];
                      if (!file) return;
                      const body = new FormData();
                      body.append("file", file);
                      body.append("page_id", pageId);
                      mutation.execute(async () => {
                        await request(
                          `${project ? projectPath(workspace.id, project.id) : workspacePath(workspace.id)}/assets`,
                          { method: "POST", body },
                        );
                        resources.refresh();
                      });
                    }}
                  />
                </label>
              )}
            </aside>
          )}
        </>
      ) : (
        <>
          <PageHeader
            title={t("文档", "Documents")}
            description={t(
              "让想法、决策与知识，在团队中持续流动。",
              "Keep ideas, decisions, and knowledge moving through your team.",
            )}
            actions={
              <Button variant="primary" onClick={() => setCreateOpen(true)}>
                <Plus size={15} />
                {t("新建文档", "New document")}
              </Button>
            }
          >
            {summary.data && (
              <div className="document-summary">
                <Badge>
                  {t("团队", "Team")} {summary.data.public_pages}
                </Badge>
                <Badge>
                  {t("私有", "Private")} {summary.data.private_pages}
                </Badge>
                <Badge>
                  {t("归档", "Archived")} {summary.data.archived_pages}
                </Badge>
              </div>
            )}
            <div className="page-tabs">
              <button
                className={!archived ? "active" : ""}
                onClick={() => setArchived(false)}
              >
                {t("全部文档", "All documents")}
              </button>
              <button
                className={archived ? "active" : ""}
                onClick={() => setArchived(true)}
              >
                {t("已归档", "Archived")}
              </button>
              <span className="flex-spacer" />
              <div className="search-input compact">
                <Search size={14} />
                <Input
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                  placeholder={t("搜索文档…", "Search documents…")}
                  aria-label={t("搜索文档", "Search documents")}
                />
              </div>
            </div>
          </PageHeader>
          <div className="page-body">
            <ErrorBox message={pages.error || mutation.error} />
            {pages.loading ? (
              <Loading />
            ) : !list.length ? (
              <EmptyState
                icon={<BookOpen size={30} />}
                title={t("给团队的想法一个家", "A home for your team's ideas")}
                description={t(
                  "从一份文档开始，记录需求、共享方案，也留住值得回顾的决定。",
                  "Start a document to capture requirements, share proposals, and preserve decisions.",
                )}
                action={
                  <Button variant="primary" onClick={() => setCreateOpen(true)}>
                    <Plus size={15} />
                    {t("新建文档", "New document")}
                  </Button>
                }
              />
            ) : (
              <div className="document-grid">
                {list.map((page) => (
                  <article className="document-card" key={page.id}>
                    <div>
                      <FileText size={22} />
                      <span className="flex-spacer" />
                      {page.is_private && <Lock size={13} />}
                      <Menu items={menu(page)} />
                    </div>
                    <Link to={`${route}/${page.id}`}>
                      <h3>{page.name}</h3>
                      <span>
                        {t("打开文档", "Open document")}
                        <ChevronRight size={13} />
                      </span>
                    </Link>
                    <footer>
                      <Clock3 size={12} />
                      {dateTime(page.updated_at, appStore.locale)}
                    </footer>
                  </article>
                ))}
              </div>
            )}
          </div>
        </>
      )}
      <Modal
        open={createOpen}
        onOpenChange={setCreateOpen}
        title={t("新建文档", "Create a document")}
      >
        <form className="form-stack modal-body" onSubmit={create}>
          <Field label={t("文档标题", "Document title")}>
            <Input
              value={newName}
              onChange={(event) => setNewName(event.target.value)}
              required
              autoFocus
              placeholder={t("给这个想法起个名字", "Give your idea a name")}
            />
          </Field>
          <Field label={t("父文档", "Parent document")}>
            <Select
              value={createParent}
              onChange={(event) => setCreateParent(event.target.value)}
            >
              <option value="">{t("顶层文档", "Top level document")}</option>
              {tree.map(({ page, depth }) => (
                <option key={page.id} value={page.id}>
                  {"　".repeat(depth)}
                  {page.name}
                </option>
              ))}
            </Select>
          </Field>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={privatePage}
              onChange={(event) => setPrivatePage(event.target.checked)}
            />
            {t("仅自己可见", "Private to me")}
          </label>
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button type="submit" variant="primary" busy={mutation.busy}>
              {t("创建文档", "Create document")}
            </Button>
          </div>
        </form>
      </Modal>
      <Modal
        open={!!movingPage}
        onOpenChange={(open) => {
          if (!open) setMovingPage(null);
        }}
        title={t("移动文档", "Move document")}
      >
        <div className="form-stack modal-body">
          <Field label={t("父文档", "Parent document")}>
            <Select
              value={moveParent}
              onChange={(event) => setMoveParent(event.target.value)}
            >
              <option value="">{t("顶层文档", "Top level document")}</option>
              {tree
                .filter(({ page }) => !invalidParents.has(page.id))
                .map(({ page, depth }) => (
                  <option key={page.id} value={page.id}>
                    {"　".repeat(depth)}
                    {page.name}
                  </option>
                ))}
            </Select>
          </Field>
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button
              variant="primary"
              busy={mutation.busy}
              onClick={() =>
                mutation.execute(async () => {
                  const result = await patchMetadata(movingPage!.id, {
                    parent_id: moveParent || null,
                  });
                  if (movingPage!.id === pageId) detail.setResult(result);
                  setMovingPage(null);
                  pages.refresh();
                })
              }
            >
              {t("移动", "Move")}
            </Button>
          </div>
        </div>
      </Modal>
      <Modal
        open={!!previewVersion}
        onOpenChange={(open) => {
          if (!open) setPreviewVersion(null);
        }}
        title={`${previewVersion?.name ?? ""} · v${previewVersion?.version ?? ""}`}
      >
        <div className="modal-body">
          <ErrorBox message={versionPreview.error} />
          {versionPreview.loading ? (
            <Loading />
          ) : (
            <div
              className="prose-content"
              dangerouslySetInnerHTML={{
                __html: safeHTML(versionPreview.data?.content_html ?? ""),
              }}
            />
          )}
        </div>
      </Modal>
      <Confirm
        open={!!deleting}
        onOpenChange={(open) => {
          if (!open) setDeleting(null);
        }}
        title={t("删除文档？", "Delete document?")}
        description={t(
          "文档会从工作区中移除。请先保留需要的内容。",
          "The document will be removed. Keep a copy of any content you need.",
        )}
        busy={mutation.busy}
        onConfirm={() => {
          mutation.execute(async () => {
            await api.delete(`${collection}/${deleting!.id}`);
            setDeleting(null);
            pages.refresh();
            if (pageId) navigate(route);
          });
        }}
      />
    </div>
  );
});

function descendantIDs(pages: PageDocument[], root: string) {
  const found = new Set([root]);
  for (let changed = true; changed;) {
    changed = false;
    for (const page of pages)
      if (page.parent_id && found.has(page.parent_id) && !found.has(page.id)) {
        found.add(page.id);
        changed = true;
      }
  }
  return found;
}

function orderPages(
  pages: PageDocument[],
): { page: PageDocument; depth: number }[] {
  const ids = new Set(pages.map((page) => page.id));
  const visited = new Set<string>();
  const result: { page: PageDocument; depth: number }[] = [];
  const visit = (page: PageDocument, depth: number) => {
    if (visited.has(page.id)) return;
    visited.add(page.id);
    result.push({ page, depth });
    for (const child of pages.filter((item) => item.parent_id === page.id))
      visit(child, depth + 1);
  };
  pages
    .filter((page) => !page.parent_id || !ids.has(page.parent_id))
    .forEach((page) => visit(page, 0));
  pages.forEach((page) => visit(page, 0));
  return result;
}
