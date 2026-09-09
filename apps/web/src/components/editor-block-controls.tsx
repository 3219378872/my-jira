import { useEffect, useRef, useState, type DragEvent } from "react";
import { createPortal } from "react-dom";
import { type Editor } from "@tiptap/react";
import type { Transaction } from "@tiptap/pm/state";
import { ArrowDown, ArrowUp, Copy, GripVertical, Trash2 } from "lucide-react";
import { appStore } from "../stores/app-store";
import { Button, Menu } from "./ui";
import { moveTopLevelBlock } from "./editor-blocks";

export function editorOverlayHost(editor: Editor) {
  return (
    editor.view.dom.closest<HTMLElement>('[role="dialog"]') ?? document.body
  );
}
export function editorOverlayPoint(
  host: HTMLElement,
  left: number,
  top: number,
) {
  const rect = host.getBoundingClientRect();
  return {
    position:
      host === document.body ? ("fixed" as const) : ("absolute" as const),
    left: host === document.body ? left : left - rect.left + host.scrollLeft,
    top: host === document.body ? top : top - rect.top + host.scrollTop,
  };
}

export function EditorBlockControls({
  editor,
  disabled,
}: {
  editor: Editor;
  disabled: boolean;
}) {
  const [block, setBlock] = useState<{
    from: number;
    left: number;
    top: number;
  } | null>(null);
  const [indicator, setIndicator] = useState<{
    left: number;
    top: number;
    width: number;
  } | null>(null);
  const current = useRef<number | null>(null);
  const dragging = useRef<number | null>(null);
  const dragKey = useRef(crypto.randomUUID());
  const host = editorOverlayHost(editor);
  const t = appStore.t;
  const showBlock = (from: number) => {
    const element = editor.view.nodeDOM(from);
    if (!(element instanceof HTMLElement)) return;
    const rect = element.getBoundingClientRect();
    current.current = from;
    setBlock({
      from,
      left: Math.max(host.getBoundingClientRect().left + 4, rect.left - 52),
      top: rect.top,
    });
  };
  useEffect(() => {
    if (disabled) {
      setBlock(null);
      return;
    }
    const dom = editor.view.dom;
    const locate = (target: EventTarget | null) => {
      if (!(target instanceof Node)) return;
      editor.state.doc.forEach((_node, from) => {
        const element = editor.view.nodeDOM(from);
        if (element instanceof HTMLElement && element.contains(target))
          showBlock(from);
      });
    };
    const pointer = (event: PointerEvent) => locate(event.target);
    const selection = () => {
      if (!editor.isFocused) return;
      const { $from } = editor.state.selection;
      showBlock(
        $from.depth
          ? $from.before(1)
          : Math.min($from.pos, editor.state.doc.content.size - 1),
      );
    };
    const transaction = ({ transaction }: { transaction: Transaction }) => {
      for (const ref of [current, dragging]) {
        if (ref.current === null) continue;
        const mapped = transaction.mapping.mapResult(ref.current, 1);
        ref.current = mapped.deleted ? null : mapped.pos;
      }
      if (
        current.current !== null &&
        current.current < editor.state.doc.content.size
      )
        showBlock(current.current);
      else setBlock(null);
    };
    const targetPosition = (event: globalThis.DragEvent) => {
      const hit = editor.view.posAtCoords({
        left: event.clientX,
        top: event.clientY,
      });
      if (!hit) return null;
      const resolved = editor.state.doc.resolve(hit.pos);
      const from = resolved.depth ? resolved.before(1) : hit.pos;
      const node = editor.state.doc.nodeAt(from);
      const element = editor.view.nodeDOM(from);
      if (!node || !(element instanceof HTMLElement)) return null;
      const rect = element.getBoundingClientRect();
      const after = event.clientY > rect.top + rect.height / 2;
      return {
        pos: after ? from + node.nodeSize : from,
        rect,
        top: after ? rect.bottom : rect.top,
      };
    };
    const over = (event: globalThis.DragEvent) => {
      if (dragging.current === null || !editor.isEditable) return;
      event.preventDefault();
      event.stopPropagation();
      if (event.dataTransfer) event.dataTransfer.dropEffect = "move";
      const target = targetPosition(event);
      if (target)
        setIndicator({
          left: target.rect.left,
          top: target.top,
          width: target.rect.width,
        });
    };
    const drop = (event: globalThis.DragEvent) => {
      if (
        dragging.current === null ||
        event.dataTransfer?.getData("application/x-myjira-block") !==
          dragKey.current ||
        !editor.isEditable
      )
        return;
      event.preventDefault();
      event.stopPropagation();
      event.stopImmediatePropagation();
      const target = targetPosition(event);
      if (target) {
        const transaction = moveTopLevelBlock(
          editor.state,
          dragging.current,
          target.pos,
        );
        if (transaction) editor.view.dispatch(transaction);
      }
      dragging.current = null;
      setIndicator(null);
      editor.commands.focus();
    };
    const refresh = () => {
      if (current.current !== null) showBlock(current.current);
    };
    dom.addEventListener("pointermove", pointer);
    dom.addEventListener("dragover", over, true);
    dom.addEventListener("drop", drop, true);
    window.addEventListener("scroll", refresh, true);
    editor.on("selectionUpdate", selection);
    editor.on("transaction", transaction);
    return () => {
      dom.removeEventListener("pointermove", pointer);
      dom.removeEventListener("dragover", over, true);
      dom.removeEventListener("drop", drop, true);
      window.removeEventListener("scroll", refresh, true);
      editor.off("selectionUpdate", selection);
      editor.off("transaction", transaction);
    };
  }, [editor, disabled]);
  if (disabled || !block) return null;
  const nodes: { from: number; size: number }[] = [];
  editor.state.doc.forEach((node, from) =>
    nodes.push({ from, size: node.nodeSize }),
  );
  const index = nodes.findIndex((node) => node.from === block.from);
  const node = editor.state.doc.nodeAt(block.from);
  if (index < 0 || !node) return null;
  const move = (target: number) => {
    const transaction = moveTopLevelBlock(editor.state, block.from, target);
    if (transaction) editor.view.dispatch(transaction);
    editor.commands.focus();
  };
  const start = (event: DragEvent<HTMLButtonElement>) => {
    if (!editor.isEditable) {
      event.preventDefault();
      return;
    }
    dragging.current = block.from;
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("application/x-myjira-block", dragKey.current);
    event.dataTransfer.setData(
      "text/plain",
      node.textContent || t("内容块", "Content block"),
    );
    const element = editor.view.nodeDOM(block.from);
    if (element instanceof HTMLElement)
      event.dataTransfer.setDragImage(element, 10, 10);
  };
  return createPortal(
    <>
      <div
        className="editor-block-controls"
        data-editor-controls
        style={editorOverlayPoint(host, block.left, block.top)}
      >
        <Button
          variant="ghost"
          size="icon"
          draggable
          aria-label={t("拖动内容块", "Drag content block")}
          onDragStart={start}
          onDragEnd={() => {
            dragging.current = null;
            setIndicator(null);
          }}
        >
          <GripVertical size={15} />
        </Button>
        <Menu
          items={[
            ...(index
              ? [
                  {
                    label: t("上移内容块", "Move block up"),
                    icon: <ArrowUp size={13} />,
                    onSelect: () => move(nodes[index - 1].from),
                  },
                ]
              : []),
            ...(index < nodes.length - 1
              ? [
                  {
                    label: t("下移内容块", "Move block down"),
                    icon: <ArrowDown size={13} />,
                    onSelect: () =>
                      move(nodes[index + 1].from + nodes[index + 1].size),
                  },
                ]
              : []),
            {
              label: t("复制内容块", "Duplicate block"),
              icon: <Copy size={13} />,
              onSelect: () => {
                if (editor.isEditable)
                  editor
                    .chain()
                    .focus()
                    .insertContentAt(block.from + node.nodeSize, node.toJSON())
                    .run();
              },
            },
            {
              label: t("删除内容块", "Delete block"),
              icon: <Trash2 size={13} />,
              danger: true,
              onSelect: () => {
                if (editor.isEditable)
                  editor
                    .chain()
                    .focus()
                    .deleteRange({
                      from: block.from,
                      to: block.from + node.nodeSize,
                    })
                    .run();
              },
            },
          ]}
        />
      </div>
      {indicator && (
        <div
          className="editor-drop-indicator"
          style={{
            ...editorOverlayPoint(host, indicator.left, indicator.top),
            width: indicator.width,
          }}
        />
      )}
    </>,
    host,
  );
}
