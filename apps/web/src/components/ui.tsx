import {
  forwardRef,
  type ButtonHTMLAttributes,
  type HTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
} from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import * as DropdownPrimitive from "@radix-ui/react-dropdown-menu";
import * as TooltipPrimitive from "@radix-ui/react-tooltip";
import * as PopoverPrimitive from "@radix-ui/react-popover";
import {
  AlertCircle,
  Check,
  CheckCircle2,
  ChevronDown,
  Circle,
  Ellipsis,
  Info,
  Loader2,
  X,
} from "lucide-react";
import { observer } from "mobx-react-lite";
import { cn, initials } from "../lib/utils";
import { appStore } from "../stores/app-store";
import type { Priority, State } from "../types";

export const Button = forwardRef<
  HTMLButtonElement,
  ButtonHTMLAttributes<HTMLButtonElement> & {
    variant?: "primary" | "secondary" | "ghost" | "danger";
    size?: "sm" | "md" | "icon";
    busy?: boolean;
  }
>(function Button(
  {
    children,
    variant = "secondary",
    size = "md",
    busy,
    className,
    disabled,
    ...props
  },
  ref,
) {
  return (
    <button
      ref={ref}
      className={cn("button", `button-${variant}`, `button-${size}`, className)}
      disabled={disabled || busy}
      {...props}
    >
      {busy && <Loader2 size={14} className="spin" />}
      {children}
    </button>
  );
});

export const Input = forwardRef<
  HTMLInputElement,
  InputHTMLAttributes<HTMLInputElement>
>(function Input({ className, ...props }, ref) {
  return <input ref={ref} className={cn("input", className)} {...props} />;
});

export const Select = forwardRef<
  HTMLSelectElement,
  SelectHTMLAttributes<HTMLSelectElement>
>(function Select({ className, children, ...props }, ref) {
  return (
    <div className={cn("select-wrapper", className)}>
      <select ref={ref} className="input select" {...props}>
        {children}
      </select>
      <ChevronDown size={13} />
    </div>
  );
});

export function MultiSelect({
  value,
  onChange,
  options,
  placeholder,
  disabled,
}: {
  value: string[];
  onChange: (value: string[]) => void;
  options: { value: string; label: string; color?: string }[];
  placeholder: string;
  disabled?: boolean;
}) {
  return (
    <PopoverPrimitive.Root>
      <PopoverPrimitive.Trigger asChild>
        <button
          type="button"
          className="input multiselect-trigger"
          disabled={disabled}
          aria-label={placeholder}
        >
          {value.length ? (
            <span className="multiselect-values">
              {value.slice(0, 3).map((id) => (
                <span key={id} className="multiselect-value">
                  {options.find((option) => option.value === id)?.label ??
                    id.slice(0, 6)}
                </span>
              ))}
              {value.length > 3 && <span>+{value.length - 3}</span>}
            </span>
          ) : (
            <span className="text-muted">{placeholder}</span>
          )}
          <ChevronDown size={13} />
        </button>
      </PopoverPrimitive.Trigger>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Content
          className="menu-content multiselect-menu"
          sideOffset={5}
          align="start"
        >
          {options.length === 0 && <span className="menu-empty">—</span>}
          {options.map((option) => (
            <label key={option.value} className="menu-item">
              <input
                type="checkbox"
                checked={value.includes(option.value)}
                onChange={(event) =>
                  onChange(
                    event.target.checked
                      ? [...value, option.value]
                      : value.filter((id) => id !== option.value),
                  )
                }
              />
              {option.color && (
                <span
                  className="label-dot"
                  style={{ backgroundColor: option.color }}
                />
              )}
              {option.label}
            </label>
          ))}
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  );
}

export function Field({
  label,
  hint,
  children,
  className,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <label className={cn("field", className)}>
      <span className="field-label">{label}</span>
      {children}
      {hint && <span className="field-hint">{hint}</span>}
    </label>
  );
}

export const Modal = observer(function Modal({
  open,
  onOpenChange,
  title,
  description,
  children,
  className,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="modal-overlay" />
        <DialogPrimitive.Content
          className={cn("modal-content", className)}
          aria-describedby={description ? undefined : undefined}
        >
          <div className="modal-heading">
            <div>
              <DialogPrimitive.Title className="modal-title">
                {title}
              </DialogPrimitive.Title>
              {description ? (
                <DialogPrimitive.Description className="modal-description">
                  {description}
                </DialogPrimitive.Description>
              ) : (
                <DialogPrimitive.Description className="sr-only">
                  {title}
                </DialogPrimitive.Description>
              )}
            </div>
            <DialogPrimitive.Close asChild>
              <Button
                variant="ghost"
                size="icon"
                aria-label={appStore.t("关闭", "Close")}
              >
                <X size={18} />
              </Button>
            </DialogPrimitive.Close>
          </div>
          {children}
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
});

export const Confirm = observer(function Confirm({
  open,
  onOpenChange,
  title,
  description,
  onConfirm,
  busy,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  onConfirm: () => void;
  busy?: boolean;
}) {
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      description={description}
    >
      <div className="modal-footer">
        <Button onClick={() => onOpenChange(false)}>
          {appStore.t("取消", "Cancel")}
        </Button>
        <Button variant="danger" busy={busy} onClick={onConfirm}>
          {appStore.t("确认删除", "Delete")}
        </Button>
      </div>
    </Modal>
  );
});

export interface MenuItem {
  label: string;
  icon?: ReactNode;
  onSelect: () => void;
  danger?: boolean;
  disabled?: boolean;
  separator?: boolean;
}
export const Menu = observer(function Menu({
  children,
  items,
  label,
}: {
  children?: ReactNode;
  items: MenuItem[];
  label?: string;
}) {
  return (
    <DropdownPrimitive.Root>
      <DropdownPrimitive.Trigger asChild>
        {children ?? (
          <Button
            variant="ghost"
            size="icon"
            aria-label={label ?? appStore.t("更多操作", "More actions")}
          >
            <Ellipsis size={17} />
          </Button>
        )}
      </DropdownPrimitive.Trigger>
      <DropdownPrimitive.Portal>
        <DropdownPrimitive.Content
          className="menu-content"
          sideOffset={6}
          align="end"
        >
          {items.map((item, index) => (
            <div key={index}>
              {item.separator && (
                <DropdownPrimitive.Separator className="menu-separator" />
              )}
              <DropdownPrimitive.Item
                disabled={item.disabled}
                className={cn("menu-item", item.danger && "text-danger")}
                onSelect={item.onSelect}
              >
                {item.icon}
                {item.label}
              </DropdownPrimitive.Item>
            </div>
          ))}
        </DropdownPrimitive.Content>
      </DropdownPrimitive.Portal>
    </DropdownPrimitive.Root>
  );
});

export function Tooltip({
  children,
  label,
}: {
  children: ReactNode;
  label: string;
}) {
  return (
    <TooltipPrimitive.Root>
      <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
      <TooltipPrimitive.Portal>
        <TooltipPrimitive.Content className="tooltip-content" sideOffset={6}>
          {label}
        </TooltipPrimitive.Content>
      </TooltipPrimitive.Portal>
    </TooltipPrimitive.Root>
  );
}

export function Avatar({
  name,
  src,
  size = "sm",
}: {
  name: string;
  src?: string;
  size?: "xs" | "sm" | "md" | "lg";
}) {
  const colors = ["#7471a7", "#46838c", "#947243", "#627caa", "#a16379"];
  const color =
    colors[
      [...name].reduce((value, char) => value + char.charCodeAt(0), 0) %
        colors.length
    ];
  return (
    <span
      className={cn("avatar", `avatar-${size}`)}
      title={name}
      style={{ backgroundColor: color }}
    >
      {src ? <img src={src} alt={name} /> : initials(name)}
    </span>
  );
}

export function Badge({
  children,
  className,
  ...props
}: HTMLAttributes<HTMLSpanElement>) {
  return (
    <span className={cn("badge", className)} {...props}>
      {children}
    </span>
  );
}

export function StateIcon({
  state,
  size = 14,
}: {
  state?: State;
  size?: number;
}) {
  if (state?.group === "completed")
    return <CheckCircle2 size={size} style={{ color: state.color }} />;
  if (state?.group === "cancelled")
    return <X size={size} style={{ color: state.color }} />;
  return (
    <Circle
      size={size}
      style={{
        color: state?.color ?? "var(--text-muted)",
        fill: state?.group === "started" ? state.color : "none",
        strokeDasharray: state?.group === "backlog" ? "3 2" : undefined,
      }}
    />
  );
}

export const PriorityIcon = observer(function PriorityIcon({
  priority,
}: {
  priority: Priority;
}) {
  const label = priorityLabels[priority] ?? priorityLabels.none;
  return (
    <span
      className={cn("priority-icon", `priority-${priority}`)}
      title={appStore.t(label[0], label[1])}
    >
      {priority === "urgent" ? (
        <AlertCircle size={14} />
      ) : priority === "none" ? (
        <Ellipsis size={14} />
      ) : (
        <span className="priority-bars">
          <i />
          <i className={priority === "low" ? "faded" : ""} />
          <i className={priority !== "high" ? "faded" : ""} />
        </span>
      )}
    </span>
  );
});

export const priorityLabels: Record<Priority, [string, string]> = {
  urgent: ["紧急", "Urgent"],
  high: ["高", "High"],
  medium: ["中", "Medium"],
  low: ["低", "Low"],
  none: ["无优先级", "No priority"],
};

export const ErrorBox = observer(function ErrorBox({
  message,
  retry,
}: {
  message: string;
  retry?: () => void;
}) {
  if (!message) return null;
  return (
    <div className="error-box" role="alert">
      <AlertCircle size={17} />
      <span>{message}</span>
      {retry && (
        <Button size="sm" onClick={retry}>
          {appStore.t("重试", "Try again")}
        </Button>
      )}
    </div>
  );
});

export function Loading({ full = false }: { full?: boolean }) {
  return (
    <div
      className={cn("loading", full && "loading-full")}
      role="status"
      aria-label="Loading"
    >
      <Loader2 size={22} className="spin" />
    </div>
  );
}

export function EmptyState({
  icon,
  title,
  description,
  action,
}: {
  icon: ReactNode;
  title: string;
  description: string;
  action?: ReactNode;
}) {
  return (
    <div className="empty-state">
      <div className="empty-icon">{icon}</div>
      <h3>{title}</h3>
      <p>{description}</p>
      {action}
    </div>
  );
}

export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
  children,
}: {
  eyebrow?: string;
  title: string;
  description?: string;
  actions?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <header className="page-header">
      <div className="page-header-main">
        <div>
          {eyebrow && <p className="eyebrow">{eyebrow}</p>}
          <h1>{title}</h1>
          {description && <p className="page-description">{description}</p>}
        </div>
        <div className="page-actions">{actions}</div>
      </div>
      {children}
    </header>
  );
}

export const Toasts = observer(function Toasts() {
  return (
    <div className="toast-stack" aria-live="polite">
      {appStore.toasts.map((toast) => (
        <div key={toast.id} className={cn("toast", `toast-${toast.kind}`)}>
          {toast.kind === "error" ? (
            <AlertCircle size={17} />
          ) : toast.kind === "success" ? (
            <Check size={17} />
          ) : (
            <Info size={17} />
          )}
          <span>{toast.title}</span>
          <button
            onClick={() => appStore.dismissToast(toast.id)}
            aria-label={appStore.t("关闭通知", "Dismiss notification")}
          >
            <X size={14} />
          </button>
        </div>
      ))}
    </div>
  );
});
