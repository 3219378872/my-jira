import { useContext, useState, type FormEvent } from "react";
import { type Editor } from "@tiptap/react";
import { observer } from "mobx-react-lite";
import {
  AtSign,
  ChevronDown,
  ChevronRight,
  Code2,
  Image,
  Info,
  Search,
  Sparkles,
  Upload,
} from "lucide-react";
import { appStore } from "../stores/app-store";
import { api, projectPath, request, workspacePath } from "../lib/api";
import { ScopeContext, useMutation, useRemote } from "../lib/hooks";
import { memberName } from "../lib/utils";
import {
  Button,
  ErrorBox,
  Field,
  Input,
  Loading,
  Menu,
  Modal,
  Select,
  Tooltip,
} from "./ui";
import { normalizeEmbedURL } from "@myjira/editor-schema";
import type { Member } from "../types";
import { EditorCommands } from "./editor-commands";

export const AdvancedEditorTools = observer(function AdvancedEditorTools({
  editor,
  disabled = false,
  projectId,
  pageId,
  workItemId,
}: {
  editor: Editor;
  disabled?: boolean;
  projectId?: string;
  pageId?: string;
  workItemId?: string;
}) {
  const scope = useContext(ScopeContext);
  const [imageOpen, setImageOpen] = useState(false);
  const [imageURL, setImageURL] = useState("");
  const [alt, setAlt] = useState("");
  const [imageQuery, setImageQuery] = useState("");
  const [submittedQuery, setSubmittedQuery] = useState("");
  const [mentionOpen, setMentionOpen] = useState(false);
  const [mentionID, setMentionID] = useState("");
  const [embedOpen, setEmbedOpen] = useState(false);
  const [embedURL, setEmbedURL] = useState("");
  const [aiOpen, setAIOpen] = useState(false);
  const [instruction, setInstruction] = useState("");
  const [generated, setGenerated] = useState("");
  const [model, setModel] = useState("");
  const mutation = useMutation();
  const t = appStore.t;
  const members = useRemote<Member[]>(
    scope && mentionOpen
      ? `${workspacePath(scope.workspace.id)}/members`
      : null,
  );
  const images = useRemote<
    {
      id: string;
      alt: string;
      preview_url: string;
      url: string;
      author: string;
      author_url: string;
      source_url: string;
    }[]
  >(
    scope && imageOpen && submittedQuery
      ? `${workspacePath(scope.workspace.id)}/images?q=${encodeURIComponent(submittedQuery)}`
      : null,
  );
  if (!scope) return null;

  const insertText = (text: string) =>
    editor
      .chain()
      .focus()
      .insertContent(
        text.split(/\n{2,}/).map((paragraph) => ({
          type: "paragraph",
          content: paragraph ? [{ type: "text", text: paragraph }] : [],
        })),
      )
      .run();
  const generate = async (event: FormEvent) => {
    event.preventDefault();
    const selection = editor.state.doc.textBetween(
      editor.state.selection.from,
      editor.state.selection.to,
      "\n",
    );
    const result = await mutation.execute(() =>
      api.post<{ text: string; model: string }>(
        `${workspacePath(scope.workspace.id)}/ai/text`,
        { instruction, content: selection || editor.getText() },
      ),
    );
    if (result) {
      setGenerated(result.data.text);
      setModel(result.data.model);
    }
  };
  const insertImage = () => {
    if (!/^https?:\/\//i.test(imageURL) && !imageURL.startsWith("/api/")) {
      mutation.setError(
        t("请输入有效的图片地址。", "Enter a valid image URL."),
      );
      return;
    }
    editor.chain().focus().setImage({ src: imageURL, alt }).run();
    setImageOpen(false);
    setImageURL("");
    setAlt("");
  };

  return (
    <>
      <span className="toolbar-divider" />
      <EditorCommands
        editor={editor}
        disabled={disabled}
        projectId={projectId}
        onImage={() => setImageOpen(true)}
        onMention={() => setMentionOpen(true)}
        onEmbed={() => setEmbedOpen(true)}
        onAI={() => setAIOpen(true)}
      />
      <Tooltip label={t("插入图片", "Insert image")}>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          disabled={disabled}
          aria-label={t("插入图片", "Insert image")}
          onClick={() => setImageOpen(true)}
        >
          <Image size={14} />
        </Button>
      </Tooltip>
      <Tooltip label={t("提及成员", "Mention a member")}>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          disabled={disabled}
          aria-label={t("提及成员", "Mention a member")}
          onClick={() => setMentionOpen(true)}
        >
          <AtSign size={14} />
        </Button>
      </Tooltip>
      <Menu
        items={[
          {
            label: t("提示框", "Callout"),
            icon: <Info size={14} />,
            onSelect: () =>
              editor
                .chain()
                .focus()
                .insertContent({
                  type: "callout",
                  attrs: { tone: "info" },
                  content: [
                    {
                      type: "paragraph",
                      content: [
                        {
                          type: "text",
                          text: t("值得留意的内容…", "Something worth noting…"),
                        },
                      ],
                    },
                  ],
                })
                .run(),
          },
          {
            label: t("折叠内容", "Collapsible content"),
            icon: <ChevronRight size={14} />,
            onSelect: () =>
              editor
                .chain()
                .focus()
                .insertContent({
                  type: "disclosure",
                  attrs: { title: t("展开查看详情", "Open for details") },
                  content: [{ type: "paragraph" }],
                })
                .run(),
          },
          {
            label: t("嵌入内容", "Embedded content"),
            icon: <Code2 size={14} />,
            onSelect: () => setEmbedOpen(true),
          },
        ]}
      >
        <Button
          variant="ghost"
          size="icon"
          type="button"
          disabled={disabled}
          aria-label={t("更多内容块", "More content blocks")}
        >
          <ChevronDown size={14} />
        </Button>
      </Menu>
      <Tooltip label={t("AI 写作助手", "AI writing assistant")}>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          disabled={disabled}
          className="ai-editor-button"
          aria-label={t("AI 写作助手", "AI writing assistant")}
          onClick={() => setAIOpen(true)}
        >
          <Sparkles size={15} />
        </Button>
      </Tooltip>
      <Modal
        open={aiOpen}
        onOpenChange={setAIOpen}
        title={t("AI 写作助手", "AI writing assistant")}
        description={t(
          "描述你的需求，审阅结果后再插入文档。",
          "Describe what you need, review the result, and insert it when ready.",
        )}
        className="ai-dialog"
      >
        <form className="form-stack modal-body" onSubmit={generate}>
          <div className="ai-prompts">
            {[
              {
                zh: "润色文字，保持原意",
                en: "Improve the writing while preserving its meaning",
              },
              { zh: "总结关键要点", en: "Summarize the key points" },
              {
                zh: "整理为可执行的任务清单",
                en: "Turn this into an actionable task list",
              },
            ].map((prompt) => (
              <button
                type="button"
                key={prompt.en}
                onClick={() => setInstruction(t(prompt.zh, prompt.en))}
              >
                {t(prompt.zh, prompt.en)}
              </button>
            ))}
          </div>
          <Field
            label={t("你希望如何改进？", "What would you like to improve?")}
          >
            <textarea
              className="input textarea"
              value={instruction}
              onChange={(event) => setInstruction(event.target.value)}
              required
              rows={3}
              autoFocus
            />
          </Field>
          <ErrorBox message={mutation.error} />
          <Button variant="primary" type="submit" busy={mutation.busy}>
            <Sparkles size={14} />
            {t("生成建议", "Generate suggestion")}
          </Button>
          {generated && (
            <>
              <Field
                label={t(
                  "结果预览（可以继续编辑）",
                  "Review and edit the result",
                )}
              >
                <textarea
                  className="input textarea"
                  value={generated}
                  onChange={(event) => setGenerated(event.target.value)}
                  rows={9}
                />
              </Field>
              <div className="modal-footer">
                <span
                  className="text-muted"
                  style={{ fontSize: 10, marginRight: "auto" }}
                >
                  {model}
                </span>
                <Button
                  variant="primary"
                  type="button"
                  onClick={() => {
                    insertText(generated);
                    setAIOpen(false);
                    setGenerated("");
                  }}
                >
                  {t("插入编辑器", "Insert into editor")}
                </Button>
              </div>
            </>
          )}
        </form>
      </Modal>
      <Modal
        open={imageOpen}
        onOpenChange={setImageOpen}
        title={t("添加图片", "Add an image")}
      >
        <div className="form-stack modal-body">
          <Field label={t("图片地址", "Image URL")}>
            <Input
              type="url"
              value={imageURL}
              onChange={(event) => setImageURL(event.target.value)}
              placeholder="https://…"
            />
          </Field>
          <Field label={t("替代文本", "Alternative text")}>
            <Input
              value={alt}
              onChange={(event) => setAlt(event.target.value)}
              placeholder={t("描述图片内容", "Describe the image")}
            />
          </Field>
          <div className="inline-actions">
            <Button
              variant="primary"
              onClick={insertImage}
              disabled={!imageURL}
            >
              {t("插入图片", "Insert image")}
            </Button>
            <label className="button button-secondary button-md upload-button">
              <Upload size={14} />
              {t("上传文件", "Upload file")}
              <input
                type="file"
                accept="image/*"
                onChange={(event) => {
                  const file = event.target.files?.[0];
                  if (!file) return;
                  const form = new FormData();
                  form.append("file", file);
                  if (pageId) form.append("page_id", pageId);
                  if (workItemId) form.append("work_item_id", workItemId);
                  mutation.execute(async () => {
                    const result = await request<{ download_url: string }>(
                      `${projectId || scope.project ? projectPath(scope.workspace.id, projectId ?? scope.project!.id) : workspacePath(scope.workspace.id)}/assets`,
                      { method: "POST", body: form },
                    );
                    editor
                      .chain()
                      .focus()
                      .setImage({
                        src: `${result.data.download_url}${result.data.download_url.includes("?") ? "&" : "?"}inline=true`,
                        alt: alt || file.name,
                      })
                      .run();
                    setImageOpen(false);
                  });
                }}
              />
            </label>
          </div>
          <ErrorBox message={mutation.error} />
          <form
            className="image-search-form"
            onSubmit={(event) => {
              event.preventDefault();
              setSubmittedQuery(imageQuery);
            }}
          >
            <Input
              value={imageQuery}
              onChange={(event) => setImageQuery(event.target.value)}
              placeholder={t("搜索 Unsplash 图片…", "Search Unsplash images…")}
            />
            <Button type="submit">
              <Search size={14} />
            </Button>
          </form>
          <ErrorBox message={images.error} />
          {images.loading ? (
            <Loading />
          ) : (
            <div className="image-search-grid">
              {images.data?.map((image) => (
                <button
                  key={image.id}
                  type="button"
                  onClick={() => {
                    setImageURL(image.url);
                    setAlt(image.alt ?? "");
                  }}
                >
                  <img
                    src={image.preview_url}
                    alt={image.alt ?? ""}
                    loading="lazy"
                  />
                  <span>{image.author} · Unsplash</span>
                </button>
              ))}
            </div>
          )}
        </div>
      </Modal>
      <Modal
        open={mentionOpen}
        onOpenChange={setMentionOpen}
        title={t("提及团队成员", "Mention a teammate")}
      >
        <div className="form-stack modal-body">
          <ErrorBox message={members.error} />
          <Field label={t("选择成员", "Choose member")}>
            <Select
              value={mentionID}
              onChange={(event) => setMentionID(event.target.value)}
            >
              <option value="">{t("选择一个成员", "Choose a member")}</option>
              {members.data?.map((member) => (
                <option key={member.user_id} value={member.user_id}>
                  {memberName(member)}
                </option>
              ))}
            </Select>
          </Field>
          <div className="modal-footer">
            <Button
              variant="primary"
              disabled={!mentionID}
              onClick={() => {
                const member = members.data?.find(
                  (value) => value.user_id === mentionID,
                );
                if (member) {
                  editor
                    .chain()
                    .focus()
                    .insertContent([
                      {
                        type: "mention",
                        attrs: { id: mentionID, label: memberName(member) },
                      },
                      { type: "text", text: " " },
                    ])
                    .run();
                  setMentionOpen(false);
                }
              }}
            >
              {t("插入提及", "Insert mention")}
            </Button>
          </div>
        </div>
      </Modal>
      <Modal
        open={embedOpen}
        onOpenChange={setEmbedOpen}
        title={t("嵌入外部内容", "Embed external content")}
        description={t(
          "支持 YouTube、Vimeo 和 Figma 链接。",
          "Supports YouTube, Vimeo, and Figma links.",
        )}
      >
        <form
          className="form-stack modal-body"
          onSubmit={(event) => {
            event.preventDefault();
            const src = normalizeEmbedURL(embedURL);
            if (!src) {
              mutation.setError(
                t(
                  "请输入受支持服务的 HTTPS 链接。",
                  "Enter an HTTPS link from a supported service.",
                ),
              );
              return;
            }
            editor
              .chain()
              .focus()
              .insertContent({
                type: "embed",
                attrs: { src, title: t("嵌入内容", "Embedded content") },
              })
              .run();
            setEmbedOpen(false);
            setEmbedURL("");
          }}
        >
          <Input
            type="url"
            value={embedURL}
            onChange={(event) => setEmbedURL(event.target.value)}
            required
            autoFocus
          />
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button variant="primary" type="submit">
              {t("插入", "Insert")}
            </Button>
          </div>
        </form>
      </Modal>
    </>
  );
});
