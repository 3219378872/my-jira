import { Badge } from "../../components/ui";
import { appStore } from "../../stores/app-store";
import { displayValue, statusLabels, statusTone } from "./presentation";

export function StatusBadge({ status }: { status: string }) {
  const labels = statusLabels[status];
  return (
    <Badge
      className={`automation-status automation-status-${statusTone(status)}`}
    >
      {labels ? appStore.t(...labels) : status || appStore.t("未知", "Unknown")}
    </Badge>
  );
}

export function Evidence({
  title,
  value,
  open = false,
}: {
  title: string;
  value: unknown;
  open?: boolean;
}) {
  if (value == null || value === "" || (Array.isArray(value) && !value.length))
    return null;
  return (
    <details className="automation-evidence" open={open}>
      <summary>{title}</summary>
      <pre>{displayValue(value)}</pre>
    </details>
  );
}

export function Facts({ entries }: { entries: [string, unknown][] }) {
  return (
    <dl className="automation-facts">
      {entries.map(([label, value]) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd>{displayValue(value)}</dd>
        </div>
      ))}
    </dl>
  );
}

export function formatTime(value: string | null | undefined) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : new Intl.DateTimeFormat(appStore.locale, {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(date);
}

export function formatDay(value: string | null | undefined) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : new Intl.DateTimeFormat(appStore.locale, {
        dateStyle: "medium",
        timeZone: "UTC",
      }).format(date);
}
