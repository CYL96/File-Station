import { useCallback, useLayoutEffect, useRef, useState } from 'react';
import type { CSSProperties, HTMLAttributes } from 'react';

const storageKey = 'file-station.sidebar-width';
const minimumWidth = 180;
const mobileBreakpoint = 680;

type Gesture = {
  pointer: number;
  x: number;
  width: number;
  preferred: number | null;
  pending: number;
  moved: boolean;
  handle: HTMLDivElement;
};

function savedWidth(): number | null {
  try {
    const saved = localStorage.getItem(storageKey);
    if (saved !== null && saved.trim() !== '' && Number.isFinite(Number(saved))) return Number(saved);
  } catch { /* Resizing still works when browser storage is unavailable. */ }
  return null;
}

function persistWidth(width: number | null) {
  try {
    if (width === null) localStorage.removeItem(storageKey);
    else localStorage.setItem(storageKey, String(width));
  } catch { /* Keep the current width even when browser storage is unavailable. */ }
}

function maximumWidth(viewport: number) {
  return Math.min(560, Math.floor(viewport * 0.45), viewport - 480);
}

function boundedWidth(width: number, viewport: number) {
  return Math.max(minimumWidth, Math.min(maximumWidth(viewport), Math.round(width)));
}

export function useSidebarResize(active: boolean) {
  const sidebarRef = useRef<HTMLElement>(null);
  const gesture = useRef<Gesture | null>(null);
  const [preferred, setPreferred] = useState(savedWidth);
  const [viewport, setViewport] = useState(() => window.innerWidth);
  const [measuredWidth, setMeasuredWidth] = useState(minimumWidth);
  const [resizing, setResizing] = useState(false);
  const desktop = viewport > mobileBreakpoint;
  // Viewport caps affect only the rendered width, never the stored preference.
  const width = desktop && preferred !== null ? boundedWidth(preferred, viewport) : undefined;

  const finish = useCallback((commit: boolean) => {
    const current = gesture.current;
    if (!current) return;
    gesture.current = null;
    document.body.classList.remove('sidebar-resizing');
    if (current.handle.hasPointerCapture(current.pointer)) current.handle.releasePointerCapture(current.pointer);
    if (commit && current.moved) persistWidth(current.pending);
    else if (!commit) setPreferred(current.preferred);
    setResizing(false);
  }, []);

  useLayoutEffect(() => {
    if (!active) return;
    const onResize = () => {
      finish(false);
      setViewport(window.innerWidth);
    };
    onResize();
    window.addEventListener('resize', onResize);
    const sidebar = sidebarRef.current;
    const measure = () => {
      if (sidebar) setMeasuredWidth(Math.round(sidebar.getBoundingClientRect().width));
    };
    measure();
    const observer = new ResizeObserver(measure);
    if (sidebar) observer.observe(sidebar);
    return () => {
      window.removeEventListener('resize', onResize);
      observer.disconnect();
      finish(false);
    };
  }, [active, finish]);

  function reset() {
    finish(false);
    setPreferred(null);
    persistWidth(null);
  }

  const separatorProps: HTMLAttributes<HTMLDivElement> = {
    role: 'separator',
    tabIndex: desktop ? 0 : -1,
    'aria-orientation': 'vertical',
    'aria-controls': 'sidebar-navigation',
    'aria-valuemin': minimumWidth,
    'aria-valuemax': desktop ? maximumWidth(viewport) : minimumWidth,
    'aria-valuenow': desktop ? width ?? measuredWidth : minimumWidth,
    onPointerDown: event => {
      if (!active || window.innerWidth <= mobileBreakpoint || event.button !== 0 || !event.isPrimary || gesture.current) return;
      const sidebar = sidebarRef.current;
      if (!sidebar) return;
      event.preventDefault();
      event.currentTarget.focus();
      event.currentTarget.setPointerCapture(event.pointerId);
      const startWidth = sidebar.getBoundingClientRect().width;
      gesture.current = { pointer: event.pointerId, x: event.clientX, width: startWidth, preferred, pending: startWidth, moved: false, handle: event.currentTarget };
      document.body.classList.add('sidebar-resizing');
      setResizing(true);
    },
    onPointerMove: event => {
      const current = gesture.current;
      if (!current || current.pointer !== event.pointerId) return;
      event.preventDefault();
      if (event.clientX === current.x && !current.moved) return;
      current.moved = true;
      current.pending = boundedWidth(current.width + event.clientX - current.x, window.innerWidth);
      setPreferred(current.pending);
    },
    onPointerUp: event => {
      if (gesture.current?.pointer === event.pointerId) finish(true);
    },
    onPointerCancel: event => {
      if (gesture.current?.pointer === event.pointerId) finish(false);
    },
    onLostPointerCapture: event => {
      if (gesture.current?.pointer === event.pointerId) finish(false);
    },
    onDoubleClick: reset,
    onKeyDown: event => {
      if (!active || window.innerWidth <= mobileBreakpoint || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
      if (event.key === 'Home') {
        event.preventDefault();
        reset();
      } else if ((event.key === 'ArrowLeft' || event.key === 'ArrowRight') && !gesture.current) {
        event.preventDefault();
        const current = sidebarRef.current?.getBoundingClientRect().width;
        if (current === undefined) return;
        const next = boundedWidth(current + (event.key === 'ArrowRight' ? 10 : -10), window.innerWidth);
        if (next === Math.round(current)) return;
        setPreferred(next);
        persistWidth(next);
      }
    },
  };

  return {
    sidebarRef,
    sidebarStyle: width === undefined ? undefined : { '--sidebar-user-width': `${width}px` } as CSSProperties,
    resizing,
    separatorProps,
  };
}
