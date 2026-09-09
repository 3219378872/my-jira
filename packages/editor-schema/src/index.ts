import { Node, mergeAttributes } from "@tiptap/core";

export function normalizeEmbedURL(value: string): string | null {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return null;
  }
  if (url.protocol !== "https:") return null;
  const host = url.hostname.toLowerCase();
  if (host === "youtu.be")
    return `https://www.youtube.com/embed/${encodeURIComponent(url.pathname.slice(1))}`;
  if (["youtube.com", "www.youtube.com"].includes(host)) {
    const id = url.searchParams.get("v") || url.pathname.split("/embed/")[1];
    return id && /^[a-zA-Z0-9_-]+$/.test(id)
      ? `https://www.youtube.com/embed/${id}`
      : null;
  }
  if (["vimeo.com", "www.vimeo.com", "player.vimeo.com"].includes(host)) {
    const id = url.pathname.split("/").filter(Boolean).at(-1);
    return id && /^\d+$/.test(id)
      ? `https://player.vimeo.com/video/${id}`
      : null;
  }
  if (["figma.com", "www.figma.com"].includes(host)) {
    if (url.pathname === "/embed") return url.toString();
    return `https://www.figma.com/embed?embed_host=share&url=${encodeURIComponent(url.toString())}`;
  }
  return null;
}

export const Callout = Node.create({
  name: "callout",
  group: "block",
  content: "block+",
  defining: true,
  addAttributes() {
    return {
      tone: {
        default: "info",
        parseHTML: (element) => element.getAttribute("data-callout") || "info",
      },
    };
  },
  parseHTML() {
    return [{ tag: "aside[data-callout]" }];
  },
  renderHTML({ node, HTMLAttributes }) {
    const tone = ["info", "warning", "success"].includes(node.attrs.tone)
      ? node.attrs.tone
      : "info";
    return [
      "aside",
      mergeAttributes(HTMLAttributes, {
        "data-callout": tone,
        class: `editor-callout callout-${tone}`,
      }),
      0,
    ];
  },
});

export const Mention = Node.create({
  name: "mention",
  group: "inline",
  inline: true,
  atom: true,
  selectable: false,
  addAttributes() {
    return {
      id: {
        default: "",
        parseHTML: (element) => element.getAttribute("data-mention-id") || "",
      },
      label: {
        default: "",
        parseHTML: (element) =>
          element.getAttribute("data-mention-label") ||
          element.textContent?.replace(/^@/, "") ||
          "",
      },
    };
  },
  parseHTML() {
    return [{ tag: "span[data-mention-id]" }];
  },
  renderHTML({ node }) {
    return [
      "span",
      {
        "data-mention-id": node.attrs.id,
        "data-mention-label": node.attrs.label,
        class: "editor-mention",
      },
      `@${node.attrs.label}`,
    ];
  },
  renderText({ node }) {
    return `@${node.attrs.label}`;
  },
});

export const Embed = Node.create({
  name: "embed",
  group: "block",
  atom: true,
  draggable: true,
  addAttributes() {
    return {
      src: {
        default: "",
        parseHTML: (element) => element.getAttribute("data-embed-src") || "",
      },
      title: {
        default: "Embedded content",
        parseHTML: (element) =>
          element.getAttribute("data-embed-title") || "Embedded content",
      },
    };
  },
  parseHTML() {
    return [{ tag: "figure[data-embed-src]" }];
  },
  renderHTML({ node }) {
    const src = normalizeEmbedURL(node.attrs.src);
    if (!src) return ["p", {}, node.attrs.title];
    return [
      "figure",
      {
        "data-embed-src": src,
        "data-embed-title": node.attrs.title,
        class: "editor-embed",
      },
      [
        "iframe",
        {
          src,
          title: node.attrs.title,
          loading: "lazy",
          referrerpolicy: "no-referrer",
          sandbox: "allow-scripts allow-same-origin allow-presentation",
          allowfullscreen: "true",
        },
      ],
    ];
  },
});

export const Disclosure = Node.create({
  name: "disclosure",
  group: "block",
  content: "block+",
  defining: true,
  addAttributes() {
    return {
      title: {
        default: "Details",
        parseHTML: (element) =>
          element.getAttribute("data-disclosure-title") || "Details",
      },
    };
  },
  parseHTML() {
    return [{ tag: "details[data-disclosure-title]", contentElement: "div" }];
  },
  renderHTML({ node }) {
    return [
      "details",
      {
        "data-disclosure-title": node.attrs.title,
        open: "",
        class: "editor-disclosure",
      },
      ["summary", {}, node.attrs.title],
      ["div", {}, 0],
    ];
  },
});

export const WorkItemReference = Node.create({
  name: "workItem",
  group: "block",
  atom: true,
  draggable: true,
  addAttributes() {
    return {
      workspaceId: {
        default: "",
        parseHTML: (element) => element.getAttribute("data-workspace-id") || "",
      },
      projectId: {
        default: "",
        parseHTML: (element) => element.getAttribute("data-project-id") || "",
      },
      issueId: {
        default: "",
        parseHTML: (element) => element.getAttribute("data-work-item-id") || "",
      },
    };
  },
  parseHTML() {
    return [{ tag: "div[data-work-item-id]" }];
  },
  renderHTML({ node }) {
    const { workspaceId, projectId, issueId } = node.attrs;
    const valid = [workspaceId, projectId, issueId].every(
      (value) =>
        typeof value === "string" &&
        /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
          value,
        ),
    );
    if (!valid) return ["p", {}, "Unavailable work item"];
    // Display names are resolved per reader. The stored document and its PDF do
    // not retain a title that could outlive work-item permissions.
    return [
      "div",
      {
        "data-work-item-id": issueId,
        "data-project-id": projectId,
        "data-workspace-id": workspaceId,
        class: "editor-work-item",
      },
      [
        "a",
        { href: `/go/work-item/${workspaceId}/${projectId}/${issueId}` },
        "Linked work item",
      ],
    ];
  },
  renderText() {
    return "Linked work item";
  },
});

export const originalEditorBlocks = [
  Callout,
  Mention,
  Embed,
  Disclosure,
  WorkItemReference,
];
