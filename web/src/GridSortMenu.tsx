import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import type { KeyboardEvent } from 'react';
import type { FileColumnKey, FileSort } from './api';
import { columnLabels } from './FileSettings';
import { Icon } from './ui';

const mainFields: FileColumnKey[] = ['name', 'modified', 'type'];
const moreFields: FileColumnKey[] = ['size', 'extension', 'permissions', 'owner', 'group', 'created'];

export function GridSortMenu({ sort, trash, onChange }: {
  sort: FileSort; trash: boolean; onChange: (sort: FileSort) => void;
}) {
  const id = useId();
  const button = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const items = useRef<(HTMLButtonElement | null)[]>([]);
  const pendingFocus = useRef(0);
  const [open, setOpen] = useState(false);
  const [more, setMore] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const moreSelected = moreFields.includes(sort.key);

  function close(restoreFocus = false) {
    setOpen(false);
    if (restoreFocus) button.current?.focus({ preventScroll: true });
  }

  function focusItem(index: number) {
    const item = items.current[index];
    const panel = menu.current;
    if (!item || !panel) return;
    item.focus({ preventScroll: true });
    const bounds = item.getBoundingClientRect();
    const panelBounds = panel.getBoundingClientRect();
    if (bounds.top < panelBounds.top + panel.clientTop) panel.scrollTop -= panelBounds.top + panel.clientTop - bounds.top;
    else if (bounds.bottom > panelBounds.top + panel.clientTop + panel.clientHeight) panel.scrollTop += bounds.bottom - panelBounds.top - panel.clientTop - panel.clientHeight;
  }

  function show(last = false) {
    pendingFocus.current = last ? 5 : moreSelected ? 3 : mainFields.indexOf(sort.key);
    setMore(false);
    setOpen(true);
  }

  function changePage(next: boolean) {
    pendingFocus.current = next ? moreSelected ? moreFields.indexOf(sort.key) + 1 : 1 : 3;
    setMore(next);
  }

  function choose(next: FileSort) {
    close(true);
    onChange(next);
  }

  useLayoutEffect(() => {
    const panel = menu.current;
    const trigger = button.current;
    if (!open || !panel || !trigger) return;
    const margin = 8;
    panel.style.width = `${Math.min(200, Math.max(0, window.innerWidth - margin * 2))}px`;
    panel.style.maxHeight = `${Math.max(0, window.innerHeight - margin * 2)}px`;
    const anchor = trigger.getBoundingClientRect();
    const bounds = panel.getBoundingClientRect();
    const below = anchor.bottom + 4;
    const top = below + bounds.height <= window.innerHeight - margin ? below : anchor.top - bounds.height - 4;
    panel.style.left = `${Math.max(margin, Math.min(anchor.left, window.innerWidth - bounds.width - margin))}px`;
    panel.style.top = `${Math.max(margin, Math.min(top, window.innerHeight - bounds.height - margin))}px`;
    panel.scrollTop = 0;
    focusItem(pendingFocus.current);
  }, [open, more]);

  useEffect(() => {
    if (!open) return;
    const outsidePointer = (event: PointerEvent) => {
      if (!(event.target instanceof Node) || menu.current?.contains(event.target) || button.current?.contains(event.target)) return;
      setOpen(false);
    };
    const outsideScroll = (event: Event) => {
      if (event.target instanceof Node && menu.current?.contains(event.target)) return;
      setOpen(false);
    };
    const outsideFocus = (event: FocusEvent) => {
      if (!(event.target instanceof Node) || (!menu.current?.contains(event.target) && !button.current?.contains(event.target))) setOpen(false);
    };
    const resize = () => setOpen(false);
    document.addEventListener('pointerdown', outsidePointer, true);
    document.addEventListener('scroll', outsideScroll, true);
    document.addEventListener('focusin', outsideFocus, true);
    window.addEventListener('resize', resize);
    return () => {
      document.removeEventListener('pointerdown', outsidePointer, true);
      document.removeEventListener('scroll', outsideScroll, true);
      document.removeEventListener('focusin', outsideFocus, true);
      window.removeEventListener('resize', resize);
    };
  }, [open]);

  function menuKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    event.stopPropagation();
    if (event.key === 'Tab') { close(); return; }
    if (event.key === 'Escape') { event.preventDefault(); close(true); return; }
    const count = more ? moreFields.length + 1 : mainFields.length + 3;
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      focusItem((activeIndex + (event.key === 'ArrowDown' ? 1 : -1) + count) % count);
    } else if (event.key === 'Home' || event.key === 'End') {
      event.preventDefault();
      focusItem(event.key === 'Home' ? 0 : count - 1);
    } else if (event.key === 'ArrowRight') {
      event.preventDefault();
      if (!more && activeIndex === 3) changePage(true);
    } else if (event.key === 'ArrowLeft') {
      event.preventDefault();
      if (more) changePage(false);
    } else if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      items.current[activeIndex]?.click();
    }
  }

  function itemProps(index: number) {
    return {
      ref: (element: HTMLButtonElement | null) => { items.current[index] = element; },
      tabIndex: activeIndex === index ? 0 : -1,
      onFocus: () => setActiveIndex(index),
    };
  }

  function field(key: FileColumnKey, index: number) {
    const label = key === 'modified' ? trash ? '删除时间' : '修改日期' : key === 'type' && trash ? '原始位置' : columnLabels[key];
    return <button key={key} type="button" role="menuitemradio" aria-checked={sort.key === key} {...itemProps(index)} onClick={() => choose({ key, ascending: sort.ascending })}>
      <span className="grid-sort-dot" aria-hidden="true">{sort.key === key ? '●' : ''}</span><span className="grid-sort-label">{label}</span>
    </button>;
  }

  return <>
    <button ref={button} id={`${id}-button`} type="button" className="button grid-sort-button" aria-haspopup="menu" aria-expanded={open} aria-controls={open ? `${id}-menu` : undefined} onClick={() => open ? close() : show()} onKeyDown={event => {
      event.stopPropagation();
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp' || event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        if (!open) show(event.key === 'ArrowUp');
        else focusItem(event.key === 'ArrowUp' ? more ? moreFields.length : 5 : 0);
      } else if (event.key === 'Escape' && open) { event.preventDefault(); close(true); }
    }}><svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M8 4v16m-4-4 4 4 4-4M16 20V4m-4 4 4-4 4 4" /></svg>排序<Icon name="chevron" size={12} className="grid-sort-chevron" /></button>
    {open && createPortal(<div ref={menu} id={`${id}-menu`} className="context-menu grid-sort-menu" role="menu" aria-labelledby={`${id}-button`} onKeyDown={menuKeyDown} onBlur={event => {
      if (!event.currentTarget.contains(event.relatedTarget) && !button.current?.contains(event.relatedTarget)) close();
    }}>
      {more ? <>
        <button type="button" role="menuitem" {...itemProps(0)} onClick={() => changePage(false)}><Icon name="chevron" size={14} className="grid-sort-back" /><span>返回</span></button>
        <div className="menu-divider" role="separator" />
        <div role="group" aria-label="更多排序字段">{moreFields.map((key, index) => field(key, index + 1))}</div>
      </> : <>
        <div role="group" aria-label="排序字段">{mainFields.map(field)}</div>
        <button type="button" role="menuitem" {...itemProps(3)} aria-label={moreSelected ? `更多排序字段，当前按${columnLabels[sort.key]}排序` : '更多排序字段'} onClick={() => changePage(true)}><span className="grid-sort-dot" aria-hidden="true">{moreSelected ? '●' : ''}</span><span className="grid-sort-label">更多</span><Icon name="chevron" size={13} /></button>
        <div className="menu-divider" role="separator" />
        <div role="group" aria-label="排序方向">{[true, false].map((ascending, index) => <button key={String(ascending)} type="button" role="menuitemradio" aria-checked={sort.ascending === ascending} {...itemProps(index + 4)} onClick={() => choose({ key: sort.key, ascending })}><span className="grid-sort-dot" aria-hidden="true">{sort.ascending === ascending ? '●' : ''}</span><span className="grid-sort-label">{ascending ? '递增' : '递减'}</span></button>)}</div>
      </>}
    </div>, document.body)}
  </>;
}
