import { useLayoutEffect, useRef } from 'react';
import type { RefObject } from 'react';

export type FileDialogKind = 'preview' | 'editor';

const mirrorProperties = [
  'font-family', 'font-size', 'font-weight', 'font-style', 'font-stretch', 'font-variant',
  'font-feature-settings', 'font-variation-settings', 'font-kerning', 'letter-spacing',
  'word-spacing', 'line-height', 'text-transform', 'text-indent', 'text-align', 'tab-size',
  'white-space', 'word-break', 'overflow-wrap', 'display', 'box-sizing',
  'padding-top', 'padding-right', 'padding-bottom', 'padding-left',
  'margin-top', 'margin-right', 'margin-bottom', 'margin-left',
  'border-top-width', 'border-right-width', 'border-bottom-width', 'border-left-width',
  'border-style', 'border-collapse', 'border-spacing', 'list-style-type', 'list-style-position',
] as const;

function horizontalBox(node: HTMLElement) {
  const style = getComputedStyle(node);
  return ['padding-left', 'padding-right', 'border-left-width', 'border-right-width']
    .reduce((width, property) => width + (parseFloat(style.getPropertyValue(property)) || 0), 0);
}

// A separate max-content mirror releases width/max-width constraints on every child,
// including Markdown's scrolling tables/code blocks and responsive images. Its width
// never depends on the live pane's current width, so deleting a long line can shrink it.
function intrinsicWidth(node: HTMLElement, mirror: HTMLElement) {
  const clone = node instanceof HTMLTextAreaElement ? document.createElement('pre') : node.cloneNode(true) as HTMLElement;
  if (node instanceof HTMLTextAreaElement) clone.textContent = node.value || ' ';
  const originals = [node, ...node.querySelectorAll<HTMLElement>('*')];
  const copies = [clone, ...clone.querySelectorAll<HTMLElement>('*')];
  copies.forEach((copy, index) => {
    const original = originals[index];
    const style = getComputedStyle(original);
    copy.removeAttribute('id');
    for (const property of mirrorProperties) copy.style.setProperty(property, style.getPropertyValue(property));
    Object.assign(copy.style, {
      width: 'max-content', minWidth: '0', maxWidth: 'none', height: 'auto', minHeight: '0',
      maxHeight: 'none', overflow: 'visible', position: 'static', flex: 'none',
    });
    if (original instanceof HTMLImageElement) {
      copy.removeAttribute('src');
      copy.removeAttribute('srcset');
      copy.style.width = `${original.naturalWidth}px`;
    }
    if (original.tagName === 'TABLE') copy.style.display = 'table';
  });
  // Textareas have no intrinsic pre margin; use their actual border/padding/font.
  if (node instanceof HTMLTextAreaElement) { clone.style.margin = '0'; clone.style.whiteSpace = 'pre'; }
  mirror.replaceChildren(clone);
  const width = clone.getBoundingClientRect().width;
  mirror.replaceChildren();
  return width;
}

export function useFileDialogWidth(ref: RefObject<HTMLDialogElement | null>, kind: FileDialogKind | undefined, revision: unknown, mode?: string) {
  const schedule = useRef<((force?: boolean) => void) | null>(null);
  useLayoutEffect(() => {
    const dialog = ref.current;
    if (!dialog || !kind) return;
    const body = dialog.querySelector<HTMLElement>(kind === 'preview' ? '.preview-body' : '.text-editor-body');
    if (!body) return;
    const mirror = document.createElement('div');
    mirror.setAttribute('aria-hidden', 'true');
    mirror.inert = true;
    Object.assign(mirror.style, {
      position: 'fixed', left: '0', top: '0', width: '0', height: '0', overflow: 'hidden',
      visibility: 'hidden', pointerEvents: 'none', contain: 'layout style paint',
    });
    document.body.append(mirror);
    const scrollbarProbe = document.createElement('div');
    scrollbarProbe.style.cssText = 'width:100px;height:100px;overflow:scroll;';
    mirror.append(scrollbarProbe);
    const scrollbar = scrollbarProbe.offsetWidth - scrollbarProbe.clientWidth;
    mirror.replaceChildren();
    let frame = 0;
    let forceNext = false;
    let lastWidth = -1;
    let viewportWidth = window.visualViewport?.width ?? document.documentElement.clientWidth;
    let disposed = false;
    const paneWidth = (node: HTMLElement, boundary: HTMLElement) => {
      let width = intrinsicWidth(node, mirror);
      // Reserve scrollbar space independently of whether this version happens to
      // overflow vertically; scrollbar-gutter:stable uses the same space in the UI.
      for (let ancestor: HTMLElement | null = node; ancestor && ancestor !== boundary; ancestor = ancestor.parentElement) {
        if (ancestor !== node) width += horizontalBox(ancestor);
        const style = getComputedStyle(ancestor);
        if (/(auto|scroll)/.test(style.overflowY)) width += scrollbar;
      }
      return width;
    };
    const measure = () => {
      frame = 0;
      if (disposed) return;
      const small = window.matchMedia('(max-width:680px)').matches;
      const limit = Math.max(0, viewportWidth - (small ? 24 : 32));
      let needed = kind === 'preview' ? 880 : 960;
      if (!small) {
        let contentWidth = 0;
        if (kind === 'preview') {
          const content = body.querySelector<HTMLElement>('.preview-text, .markdown-content, .preview-image');
          if (content) contentWidth = paneWidth(content, body);
        } else {
          const workspace = body.querySelector<HTMLElement>('.editor-workspace');
          if (workspace) {
            const source = workspace.querySelector<HTMLTextAreaElement>('textarea');
            const rendered = workspace.querySelector<HTMLElement>('.markdown-content, .toastui-editor-ww-container .toastui-editor-contents');
            const sourceWidth = source ? paneWidth(source, workspace) : 0;
            const renderedWidth = rendered ? paneWidth(rendered, workspace) : 0;
            // Split panes are equal flex columns: each must fit the wider content.
            contentWidth = workspace.classList.contains('editor-split')
              ? 2 * Math.max(sourceWidth, renderedWidth) + (parseFloat(getComputedStyle(workspace).columnGap) || 0)
              : Math.max(sourceWidth, renderedWidth);
            contentWidth += horizontalBox(workspace);
          }
        }
        needed = Math.max(needed, Math.ceil(contentWidth + horizontalBox(body) + horizontalBox(dialog) + scrollbar + 2));
      }
      const width = Math.min(needed, limit);
      if (forceNext || width !== lastWidth) dialog.style.width = `${width}px`;
      forceNext = false;
      lastWidth = width;
    };
    const enqueue = (force = false) => {
      forceNext ||= force;
      if (!frame) frame = requestAnimationFrame(measure);
    };
    schedule.current = enqueue;
    const observer = new MutationObserver(() => enqueue());
    observer.observe(body, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ['src'] });
    const loaded = () => enqueue();
    body.addEventListener('load', loaded, true);
    body.addEventListener('error', loaded, true);
    const resized = () => {
      const nextWidth = window.visualViewport?.width ?? document.documentElement.clientWidth;
      const shrank = nextWidth < viewportWidth;
      viewportWidth = nextWidth;
      enqueue(shrank);
    };
    window.addEventListener('resize', resized);
    window.visualViewport?.addEventListener('resize', resized);
    document.fonts.addEventListener('loadingdone', loaded);
    void document.fonts.ready.then(() => { if (!disposed) enqueue(); });
    enqueue(true);
    return () => {
      disposed = true;
      schedule.current = null;
      cancelAnimationFrame(frame);
      observer.disconnect();
      body.removeEventListener('load', loaded, true);
      body.removeEventListener('error', loaded, true);
      window.removeEventListener('resize', resized);
      window.visualViewport?.removeEventListener('resize', resized);
      document.fonts.removeEventListener('loadingdone', loaded);
      mirror.remove();
    };
  }, [ref, kind]);
  useLayoutEffect(() => { schedule.current?.(true); }, [kind, revision, mode]);
}
