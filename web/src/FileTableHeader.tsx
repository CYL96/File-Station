import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import type { PointerEvent, ReactNode } from 'react';
import type { FileColumnKey, FileSettings, FileSort } from './api';
import { columnLabels } from './FileSettings';

type DropPosition = { key: FileColumnKey; after: boolean };
type ColumnGeometry = { key: FileColumnKey; left: number; width: number; cells: HTMLElement[] };
type DragGhost = { label: string; left: number; top: number; width: number; height: number; settling: boolean };
export function FileTableHeader({ columns, sort, trash, busy, selection, onSort, onReorder }: {
  columns: FileSettings['columns']; sort: FileSort; trash: boolean; busy: boolean; selection: ReactNode;
  onSort: (key: FileColumnKey) => void;
  onReorder: (source: FileColumnKey, target: FileColumnKey, after: boolean) => Promise<void>;
}) {
  const gesture = useRef<{ key: FileColumnKey; x: number; pointer: number; moved: boolean; offset: number; scroll: number; geometry: ColumnGeometry[] } | null>(null);
  const suppressClick = useRef(0);
  const [source, setSource] = useState<FileColumnKey | null>(null);
  const [drop, setDrop] = useState<DropPosition | null>(null);
  const [ghost, setGhost] = useState<DragGhost | null>(null);
  const dropRef = useRef<DropPosition | null>(null);
  const previewCells = useRef<HTMLElement[]>([]);
  const header = useRef<HTMLDivElement>(null);
  const settling = useRef(false);
  function clearPreview() {
    for (const cell of previewCells.current) {
      cell.style.removeProperty('--column-shift');
      cell.classList.remove('column-slide', 'column-preview-source');
    }
    previewCells.current = [];
  }
  useEffect(() => () => clearPreview(), []);
  // The saved DOM order already matches the preview. Remove transforms before paint.
  useLayoutEffect(() => { clearPreview(); }, [columns.map(column => column.key).join(',')]);
  function preview(geometry: ColumnGeometry[], key: FileColumnKey, next: DropPosition | null) {
    const order = geometry.filter(column => column.key !== key);
    const moved = geometry.find(column => column.key === key)!;
    if (next) order.splice(order.findIndex(column => column.key === next.key) + (next.after ? 1 : 0), 0, moved);
    else order.splice(geometry.findIndex(column => column.key === key), 0, moved);
    let left = geometry[0].left;
    for (const column of order) {
      for (const cell of column.cells) {
        cell.classList.add('column-slide');
        cell.classList.toggle('column-preview-source', column.key === key);
        cell.style.setProperty('--column-shift', `${left - column.left}px`);
      }
      left += column.width;
    }
  }
  async function finish(event: PointerEvent<HTMLButtonElement>, cancelled: boolean) {
    const active = gesture.current;
    if (!active || active.pointer !== event.pointerId) return;
    gesture.current = null;
    if (!active.moved) return;
    suppressClick.current = performance.now() + 500;
    event.preventDefault();
    const next = cancelled ? null : dropRef.current;
    if (!next) for (const cell of previewCells.current) cell.style.setProperty('--column-shift', '0px');
    const origin = header.current?.querySelector<HTMLElement>(`[data-column="${active.key}"]`);
    if (origin) {
      const bounds = origin.getBoundingClientRect();
      setGhost(current => current && { ...current, left: bounds.left, top: bounds.top, settling: true });
    }
    settling.current = true;
    setDrop(null); dropRef.current = null;
    // Finish the landing even when persistence completes immediately.
    await Promise.all([
      next ? onReorder(active.key, next.key, next.after) : Promise.resolve(),
      new Promise<void>(resolve => window.setTimeout(resolve, 180)),
    ]);
    if (previewCells.current.length) {
      for (const cell of previewCells.current) cell.style.setProperty('--column-shift', '0px');
      await new Promise<void>(resolve => window.setTimeout(resolve, 180));
    }
    clearPreview(); settling.current = false; setSource(null); setGhost(null);
  }
  return <><div ref={header} className={`table-header ${source ? 'column-drag-active' : ''}`} role="row" aria-busy={busy}>
    <div role="columnheader" className="check-cell">{selection}</div>
    {columns.map(column => {
      const label = trash && column.key === 'modified' ? '删除时间' : trash && column.key === 'type' ? '原始位置' : columnLabels[column.key];
      return <div key={column.key} role="columnheader" data-column={column.key} aria-sort={sort.key === column.key ? sort.ascending ? 'ascending' : 'descending' : 'none'} className={`column-${column.key} draggable-column ${source === column.key ? 'column-drag-source' : ''} ${drop?.key === column.key ? drop.after ? 'column-drop-after' : 'column-drop-before' : ''}`}>
        <button disabled={busy} title={`${label}：点击排序，左右拖动调整列顺序；Alt + 左右键也可移动`} onClick={event => {
          if (performance.now() < suppressClick.current || settling.current) { event.preventDefault(); return; }
          onSort(column.key);
        }} onKeyDown={event => {
          if (!event.altKey || (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') || settling.current) return;
          event.preventDefault();
          const index = columns.findIndex(item => item.key === column.key);
          const direction = event.key === 'ArrowRight' ? 1 : -1;
          const target = columns[index + direction];
          if (target) void onReorder(column.key, target.key, direction > 0);
        }} onPointerDown={event => {
          if (event.button !== 0 || busy || !event.isPrimary || settling.current) return;
          const table = header.current?.closest('.file-table');
          if (!table || !header.current) return;
          const rows = table.classList.contains('grid-table') ? [] : Array.from(table.querySelectorAll<HTMLElement>('.file-row'));
          const geometry = Array.from(header.current.querySelectorAll<HTMLElement>('[data-column]'), (node, index) => {
            const bounds = node.getBoundingClientRect();
            return { key: node.dataset.column as FileColumnKey, left: bounds.left, width: bounds.width, cells: [node, ...rows.map(row => row.children[index + 1] as HTMLElement)] };
          });
          const bounds = event.currentTarget.parentElement!.getBoundingClientRect();
          suppressClick.current = 0;
          gesture.current = { key: column.key, x: event.clientX, pointer: event.pointerId, moved: false, offset: event.clientX - bounds.left, scroll: table.closest('.file-area')?.scrollLeft ?? 0, geometry };
          event.currentTarget.setPointerCapture(event.pointerId);
        }} onPointerMove={event => {
          const active = gesture.current;
          if (!active || active.pointer !== event.pointerId) return;
          if (!active.moved && Math.abs(event.clientX - active.x) < 6) return;
          if (!active.moved) previewCells.current = active.geometry.flatMap(column => column.cells);
          active.moved = true; setSource(active.key); event.preventDefault();
          const area = header.current?.closest('.file-area');
          if (area) {
            const bounds = area.getBoundingClientRect();
            if (event.clientX > bounds.right - 28) area.scrollLeft += 24;
            else if (event.clientX < bounds.left + 28) area.scrollLeft -= 24;
          }
          const bounds = header.current!.getBoundingClientRect();
          const scroll = (area?.scrollLeft ?? 0) - active.scroll;
          const x = event.clientX + scroll;
          const target = active.geometry.find(column => x >= column.left && x < column.left + column.width);
          const inside = event.clientY >= bounds.top && event.clientY <= bounds.bottom;
          let next: DropPosition | null = null;
          if (inside && target && target.key !== active.key) next = { key: target.key, after: x >= target.left + target.width / 2 };
          // Hit-test original slots, not animated cells, to avoid oscillation under the pointer.
          if (dropRef.current?.key !== next?.key || dropRef.current?.after !== next?.after || !previewCells.current[0]?.classList.contains('column-slide')) preview(active.geometry, active.key, next);
          dropRef.current = next; setDrop(next);
          const moved = active.geometry.find(column => column.key === active.key)!;
          setGhost({ label, left: event.clientX - active.offset, top: bounds.top - 3, width: moved.width, height: bounds.height, settling: false });
        }} onPointerUp={event => { void finish(event, false); }} onPointerCancel={event => { void finish(event, true); }} onLostPointerCapture={event => { void finish(event, true); }}>{label}<span className={sort.key === column.key ? 'sort-active' : 'sort-idle'}>{sort.key === column.key && !sort.ascending ? '↓' : '↑'}</span></button>
      </div>;
    })}
  </div>{ghost && createPortal(<div className={`column-drag-ghost ${ghost.settling ? 'column-ghost-landing' : ''}`} aria-hidden="true" style={{ left: ghost.left, top: ghost.top, width: ghost.width, height: ghost.height }}>{ghost.label}<span>↔</span></div>, document.body)}</>;
}
