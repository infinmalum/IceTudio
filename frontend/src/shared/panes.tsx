import { useRef } from "react";
import type { CSSProperties, PointerEvent as ReactPointerEvent } from "react";

// Column widths for the three-pane shell are user-draggable and persist in
// localStorage as "Npx". An unset value keeps the CSS defaults, including the
// automatic narrower widths on small windows.
export function loadPaneWidth(key: string): string | null {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return null;
    const value = Number.parseFloat(raw);
    if (!Number.isFinite(value) || value < 160 || value > 800) return null;
    return `${Math.round(value)}px`;
  } catch {
    return null;
  }
}

export function paneStyle(width: string | null): CSSProperties | undefined {
  return width ? ({ "--pane-w": width } as CSSProperties) : undefined;
}

// Divider between two shell columns. Dragging moves the divider, so the
// target column grows or shrinks by `grow`. Double-click restores the
// default width; the shared floor keeps the middle editor usable.
export function PaneResizer(props: {
  label: string;
  targetSelector: string;
  grow: 1 | -1;
  min: number;
  max: number;
  className?: string;
  onWidth: (width: number | null) => void;
}) {
  const drag = useRef<{ id: number; startX: number; startWidth: number } | null>(
    null,
  );
  function limit() {
    // Leave at least ~520px for the other two columns so neither pane can
    // drag the editor down to nothing.
    return Math.max(props.min, Math.min(props.max, window.innerWidth - 520));
  }
  function targetWidth() {
    return (
      document.querySelector(props.targetSelector)?.getBoundingClientRect()
        .width ?? 0
    );
  }
  function clamp(width: number) {
    return Math.min(limit(), Math.max(props.min, Math.round(width)));
  }
  function pointerDown(event: ReactPointerEvent<HTMLDivElement>) {
    event.preventDefault();
    drag.current = {
      id: event.pointerId,
      startX: event.clientX,
      startWidth: targetWidth(),
    };
    try {
      event.currentTarget.setPointerCapture(event.pointerId);
    } catch {
      // Without capture the drag only works while the pointer stays on the
      // divider; nothing else to do.
    }
  }
  function pointerMove(event: ReactPointerEvent<HTMLDivElement>) {
    const start = drag.current;
    if (!start || start.id !== event.pointerId) return;
    props.onWidth(
      clamp(start.startWidth + props.grow * (event.clientX - start.startX)),
    );
  }
  function pointerUp(event: ReactPointerEvent<HTMLDivElement>) {
    if (drag.current?.id !== event.pointerId) return;
    drag.current = null;
    try {
      event.currentTarget.releasePointerCapture(event.pointerId);
    } catch {
      // No active pointer (synthetic events, already-released pointer).
    }
  }
  function keyDown(event: React.KeyboardEvent<HTMLDivElement>) {
    const step =
      event.key === "ArrowLeft" ? -16 : event.key === "ArrowRight" ? 16 : 0;
    if (!step) return;
    event.preventDefault();
    props.onWidth(clamp(targetWidth() + props.grow * step));
  }
  return (
    <div
      className={props.className ? `pane-resizer ${props.className}` : "pane-resizer"}
      role="separator"
      aria-orientation="vertical"
      aria-label={props.label}
      tabIndex={0}
      onPointerDown={pointerDown}
      onPointerMove={pointerMove}
      onPointerUp={pointerUp}
      onPointerCancel={() => {
        drag.current = null;
      }}
      onDoubleClick={() => props.onWidth(null)}
      onKeyDown={keyDown}
    />
  );
}
