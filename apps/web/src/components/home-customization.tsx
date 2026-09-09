import { Children, isValidElement, useState, type ReactNode } from "react";
import { observer } from "mobx-react-lite";
import {
  ArrowDown,
  ArrowUp,
  ExternalLink,
  Link2,
  Plus,
  Settings2,
  Trash2,
} from "lucide-react";
import { appStore } from "../stores/app-store";
import { useMutation } from "../lib/hooks";
import { Button, ErrorBox, Field, Input, Menu, Modal } from "./ui";

const widgetNames: Record<string, [string, string]> = {
  stats: ["工作概览", "Work overview"],
  upcoming: ["我的近期工作", "Upcoming work"],
  projects: ["团队项目", "Team projects"],
  favorites: ["收藏", "Favorites"],
  recent: ["最近访问", "Recently visited"],
  stickies: ["个人便笺", "Personal notes"],
  links: ["快捷链接", "Quick links"],
};
export interface QuickLink {
  id: string;
  title: string;
  url: string;
}
export interface HomePreferences {
  widgets: string[];
  hidden: string[];
  quick_links: QuickLink[];
}
export const homeDefaults: HomePreferences = {
  widgets: Object.keys(widgetNames),
  hidden: [],
  quick_links: [],
};

export function OrderedHomeWidgets({
  value,
  children,
}: {
  value: HomePreferences;
  children: ReactNode;
}) {
  const order = [
    ...value.widgets,
    ...Object.keys(widgetNames).filter((key) => !value.widgets.includes(key)),
  ];
  const widgets = Children.toArray(children).filter(
    isValidElement<{ "data-widget": string }>,
  );
  return (
    <div className="home-widgets">
      {widgets
        .filter((child) => !value.hidden.includes(child.props["data-widget"]))
        .sort(
          (a, b) =>
            order.indexOf(a.props["data-widget"]) -
            order.indexOf(b.props["data-widget"]),
        )}
    </div>
  );
}

export const HomeCustomization = observer(function HomeCustomization({
  value,
  save,
  disabled,
}: {
  value: HomePreferences;
  save: (value: Partial<HomePreferences>) => Promise<unknown>;
  disabled: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(value);
  const mutation = useMutation();
  const t = appStore.t;
  const move = (index: number, delta: number) => {
    const widgets = [...draft.widgets];
    [widgets[index], widgets[index + delta]] = [
      widgets[index + delta],
      widgets[index],
    ];
    setDraft({ ...draft, widgets });
  };
  return (
    <>
      <Button
        disabled={disabled}
        onClick={() => {
          setDraft({
            ...value,
            widgets: [
              ...value.widgets,
              ...Object.keys(widgetNames).filter(
                (id) => !value.widgets.includes(id),
              ),
            ],
          });
          setOpen(true);
        }}
      >
        <Settings2 size={14} />
        {t("自定义首页", "Customize home")}
      </Button>
      <Modal
        open={open}
        onOpenChange={setOpen}
        title={t("自定义首页", "Customize home")}
      >
        <div className="modal-body form-stack">
          <p className="settings-note">
            {t(
              "选择想看到的组件，并调整显示顺序。设置只对你生效。",
              "Choose visible widgets and their order. These settings apply only to you.",
            )}
          </p>
          {draft.widgets
            .filter((id) => widgetNames[id])
            .map((id, index) => (
              <div className="catalog-row" key={id}>
                <label className="checkbox-label flex-spacer">
                  <input
                    type="checkbox"
                    checked={!draft.hidden.includes(id)}
                    onChange={(event) =>
                      setDraft({
                        ...draft,
                        hidden: event.target.checked
                          ? draft.hidden.filter((key) => key !== id)
                          : [...draft.hidden, id],
                      })
                    }
                  />
                  {t(...widgetNames[id])}
                </label>
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label={`${t("上移", "Move up")} ${t(...widgetNames[id])}`}
                  disabled={!index}
                  onClick={() => move(index, -1)}
                >
                  <ArrowUp size={14} />
                </Button>
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label={`${t("下移", "Move down")} ${t(...widgetNames[id])}`}
                  disabled={index === draft.widgets.length - 1}
                  onClick={() => move(index, 1)}
                >
                  <ArrowDown size={14} />
                </Button>
              </div>
            ))}
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button onClick={() => setDraft(homeDefaults)}>
              {t("重置布局", "Reset layout")}
            </Button>
            <Button
              variant="primary"
              busy={mutation.busy}
              onClick={() =>
                mutation.execute(async () => {
                  await save({ widgets: draft.widgets, hidden: draft.hidden });
                  setOpen(false);
                })
              }
            >
              {t("保存", "Save")}
            </Button>
          </div>
        </div>
      </Modal>
    </>
  );
});

export const QuickLinks = observer(function QuickLinks({
  links,
  save,
}: {
  links: QuickLink[];
  save: (links: QuickLink[]) => Promise<unknown>;
}) {
  const [open, setOpen] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [title, setTitle] = useState("");
  const [url, setURL] = useState("");
  const mutation = useMutation();
  const t = appStore.t;
  const edit = (link?: QuickLink) => {
    setEditingId(link?.id ?? null);
    setTitle(link?.title ?? "");
    setURL(link?.url ?? "");
    setOpen(true);
  };
  const move = (index: number, delta: number) => {
    const next = [...links];
    [next[index], next[index + delta]] = [next[index + delta], next[index]];
    return save(next);
  };
  return (
    <>
      <div className="section-title">
        <h2>
          <Link2 size={16} />
          {t("快捷链接", "Quick links")}
        </h2>
        <Button
          size="icon"
          variant="ghost"
          aria-label={t("添加快捷链接", "Add quick link")}
          onClick={() => edit()}
        >
          <Plus size={14} />
        </Button>
      </div>
      <ErrorBox message={mutation.error} />
      {links.length ? (
        <div className="bookmark-list">
          {links.map((link, index) => (
            <div key={link.id}>
              <ExternalLink size={13} />
              <a
                href={/^https?:\/\//i.test(link.url) ? link.url : undefined}
                target="_blank"
                rel="noreferrer"
              >
                {link.title}
              </a>
              <Menu
                items={[
                  {
                    label: t("编辑链接", "Edit link"),
                    onSelect: () => edit(link),
                  },
                  ...(index
                    ? [
                        {
                          label: t("上移", "Move up"),
                          onSelect: () => {
                            mutation.execute(() => move(index, -1));
                          },
                        },
                      ]
                    : []),
                  ...(index < links.length - 1
                    ? [
                        {
                          label: t("下移", "Move down"),
                          onSelect: () => {
                            mutation.execute(() => move(index, 1));
                          },
                        },
                      ]
                    : []),
                  {
                    label: t("删除", "Delete"),
                    icon: <Trash2 size={13} />,
                    danger: true,
                    onSelect: () => {
                      mutation.execute(() =>
                        save(links.filter((item) => item.id !== link.id)),
                      );
                    },
                  },
                ]}
              />
            </div>
          ))}
        </div>
      ) : (
        <button className="new-sticky" onClick={() => edit()}>
          <Plus size={14} />
          {t("添加团队工具或参考资料", "Add team tools or resources")}
        </button>
      )}
      <Modal
        open={open}
        onOpenChange={setOpen}
        title={
          editingId
            ? t("编辑快捷链接", "Edit quick link")
            : t("添加快捷链接", "Add quick link")
        }
      >
        <form
          className="modal-body form-stack"
          onSubmit={(event) => {
            event.preventDefault();
            mutation.execute(async () => {
              const parsed = new URL(url);
              if (!["http:", "https:"].includes(parsed.protocol))
                throw new Error(
                  t(
                    "请输入 HTTP 或 HTTPS 地址。",
                    "Enter an HTTP or HTTPS URL.",
                  ),
                );
              const link = {
                id: editingId ?? crypto.randomUUID(),
                title: title.trim(),
                url: parsed.href,
              };
              await save(
                editingId
                  ? links.map((item) => (item.id === editingId ? link : item))
                  : [...links, link],
              );
              setOpen(false);
            });
          }}
        >
          <Field label={t("名称", "Name")}>
            <Input
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              required
              maxLength={100}
              autoFocus
            />
          </Field>
          <Field label={t("链接", "URL")}>
            <Input
              value={url}
              onChange={(event) => setURL(event.target.value)}
              type="url"
              required
            />
          </Field>
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button variant="primary" type="submit" busy={mutation.busy}>
              {t("保存", "Save")}
            </Button>
          </div>
        </form>
      </Modal>
    </>
  );
});
