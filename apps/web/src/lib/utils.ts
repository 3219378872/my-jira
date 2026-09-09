import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";
import DOMPurify from "dompurify";
import type { Locale, Priority } from "../types";

export const cn = (...values: ClassValue[]) => twMerge(clsx(values));
export const initials = (value: string) =>
  value
    .trim()
    .split(/\s+/)
    .slice(0, 2)
    .map((part) => part[0])
    .join("")
    .toUpperCase() || "?";
export const safeHTML = (value: string) => {
  const fragment = DOMPurify.sanitize(value, {
    USE_PROFILES: { html: true },
    ADD_TAGS: ["iframe"],
    ADD_ATTR: ["allowfullscreen", "loading", "referrerpolicy"],
    RETURN_DOM_FRAGMENT: true,
  });
  for (const frame of fragment.querySelectorAll("iframe")) {
    const source = frame.getAttribute("src") ?? "";
    const approved =
      /^https:\/\/(?:(?:www\.)?youtube(?:-nocookie)?\.com\/embed\/[A-Za-z0-9_-]+|player\.vimeo\.com\/video\/\d+|(?:www\.)?figma\.com\/embed)(?:\?[^\s<>"']*)?$/.test(
        source,
      );
    if (!approved) {
      frame.remove();
      continue;
    }
    frame.setAttribute(
      "sandbox",
      "allow-scripts allow-same-origin allow-presentation",
    );
    frame.setAttribute("referrerpolicy", "no-referrer");
    frame.setAttribute("loading", "lazy");
  }
  const container = document.createElement("div");
  container.append(fragment);
  return container.innerHTML;
};
export const slugify = (value: string) =>
  value
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "");
export const priorities: Priority[] = [
  "urgent",
  "high",
  "medium",
  "low",
  "none",
];
export const shortDate = (
  value: string | null | undefined,
  locale: Locale = "zh-CN",
) =>
  value
    ? new Intl.DateTimeFormat(locale, {
        month: "short",
        day: "numeric",
      }).format(new Date(value.slice(0, 10) + "T12:00:00"))
    : "—";
export const dateTime = (value: string, locale: Locale = "zh-CN") =>
  new Intl.DateTimeFormat(locale, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
export const memberName = (member: {
  display_name?: string;
  email?: string;
  user?: { display_name: string; email: string };
}) =>
  member.display_name ||
  member.user?.display_name ||
  member.email ||
  member.user?.email ||
  "Member";
