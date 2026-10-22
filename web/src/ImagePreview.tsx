import { useEffect, useRef, useState } from 'react';
import type { PointerEvent } from 'react';
import { useI18n } from './i18n';

const MIN_ZOOM = 0.25;
const MAX_ZOOM = 8;
const ZOOM_STEP = 1.25;

type View = {
  width: number;
  height: number;
  naturalWidth: number;
  naturalHeight: number;
  zoom: number;
  x: number;
  y: number;
};

function baseScale(view: View, fillWindow: boolean) {
  if (!view.width || !view.height || !view.naturalWidth || !view.naturalHeight) return 0;
  const widthScale = view.width / view.naturalWidth;
  const heightScale = view.height / view.naturalHeight;
  const scale = Math.min(widthScale, heightScale);
  return fillWindow ? scale : Math.min(1, scale);
}

function panLimits(view: View, fillWindow: boolean) {
  const scale = baseScale(view, fillWindow) * view.zoom;
  return {
    x: Math.max(0, (view.naturalWidth * scale - view.width) / 2),
    y: Math.max(0, (view.naturalHeight * scale - view.height) / 2),
  };
}

function constrain(view: View, fillWindow: boolean): View {
  const limits = panLimits(view, fillWindow);
  return {
    ...view,
    x: Math.max(-limits.x, Math.min(limits.x, view.x)),
    y: Math.max(-limits.y, Math.min(limits.y, view.y)),
  };
}

export function ImagePreview({ url, name, onError, fillWindow, onFillWindowChange }: { url: string; name: string; onError: () => void; fillWindow: boolean; onFillWindowChange: (fillWindow: boolean) => void }) {
  const { t } = useI18n();
  const viewportRef = useRef<HTMLDivElement>(null);
  const [view, setView] = useState<View>({ width: 0, height: 0, naturalWidth: 0, naturalHeight: 0, zoom: 1, x: 0, y: 0 });
  const viewRef = useRef(view);
  const dragRef = useRef<{ pointerId: number; x: number; y: number } | null>(null);
  const [dragging, setDragging] = useState(false);

  function updateView(next: View) {
    viewRef.current = next;
    setView(next);
  }

  function measure(naturalWidth = viewRef.current.naturalWidth, naturalHeight = viewRef.current.naturalHeight) {
    const viewport = viewportRef.current;
    if (!viewport) return;
    updateView(constrain({ ...viewRef.current, width: viewport.clientWidth, height: viewport.clientHeight, naturalWidth, naturalHeight }, fillWindow));
  }

  function zoomTo(zoom: number, anchorX = 0, anchorY = 0) {
    const current = viewRef.current;
    if (!baseScale(current, fillWindow)) return;
    const nextZoom = Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, zoom));
    const ratio = nextZoom / current.zoom;
    // Keep the image point beneath the anchor stationary until an edge is reached.
    updateView(constrain({ ...current, zoom: nextZoom, x: anchorX - (anchorX - current.x) * ratio, y: anchorY - (anchorY - current.y) * ratio }, fillWindow));
  }

  useEffect(() => {
    const viewport = viewportRef.current!;
    const observer = new ResizeObserver(() => measure());
    observer.observe(viewport);
    measure();
    const wheel = (event: WheelEvent) => {
      if (!event.ctrlKey) return;
      event.preventDefault();
      const current = viewRef.current;
      const bounds = viewport.getBoundingClientRect();
      const delta = event.deltaY * (event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? current.height : 1);
      zoomTo(current.zoom * Math.exp(-delta * 0.002), event.clientX - bounds.left - current.width / 2, event.clientY - bounds.top - current.height / 2);
    };
    viewport.addEventListener('wheel', wheel, { passive: false });
    return () => {
      observer.disconnect();
      viewport.removeEventListener('wheel', wheel);
    };
  }, [fillWindow]);

  function startDrag(event: PointerEvent<HTMLDivElement>) {
    if (!event.isPrimary || event.button !== 0 || dragRef.current) return;
    const limits = panLimits(viewRef.current, fillWindow);
    if (!limits.x && !limits.y) return;
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    dragRef.current = { pointerId: event.pointerId, x: event.clientX, y: event.clientY };
    setDragging(true);
  }

  function moveDrag(event: PointerEvent<HTMLDivElement>) {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    const current = viewRef.current;
    updateView(constrain({ ...current, x: current.x + event.clientX - drag.x, y: current.y + event.clientY - drag.y }, fillWindow));
    drag.x = event.clientX;
    drag.y = event.clientY;
  }

  function endDrag(event: PointerEvent<HTMLDivElement>) {
    if (dragRef.current?.pointerId !== event.pointerId) return;
    dragRef.current = null;
    setDragging(false);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
  }

  const scale = baseScale(view, fillWindow) * view.zoom;
  const limits = panLimits(view, fillWindow);
  const canPan = limits.x > 0 || limits.y > 0;

  return <div className="image-preview">
    <div className="image-preview-toolbar" role="group" aria-label={t('图片缩放')}>
      <button type="button" className="button" aria-label={t('缩小图片')} disabled={!scale || view.zoom <= MIN_ZOOM} onClick={() => zoomTo(viewRef.current.zoom / ZOOM_STEP)}>−</button>
      <button type="button" className="button icon-button" aria-label={t('适应窗口')} title={t('适应窗口')} aria-pressed={!fillWindow} disabled={!scale} onClick={() => { onFillWindowChange(false); updateView({ ...viewRef.current, zoom: 1, x: 0, y: 0 }); }}>
        <svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M8 3H3v5m13-5h5v5M3 16v5h5m13-5v5h-5" /><rect x="7" y="8" width="10" height="8" rx="1" /></svg>
      </button>
      <button type="button" className="button icon-button" aria-label={t('铺满窗口')} title={t('铺满窗口（完整显示，后续图片沿用）')} aria-pressed={fillWindow} disabled={!scale} onClick={() => { onFillWindowChange(!fillWindow); updateView({ ...viewRef.current, zoom: 1, x: 0, y: 0 }); }}>
        <svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="3" width="18" height="18" rx="2" /><path d="M7 10V7h3m4 0h3v3M7 14v3h3m4 0h3v-3M7 7l4 4m6-4-4 4m-6 6 4-4m6 4-4-4" /></svg>
      </button>
      <button type="button" className="button" aria-label={t('放大图片')} disabled={!scale || view.zoom >= MAX_ZOOM} onClick={() => zoomTo(viewRef.current.zoom * ZOOM_STEP)}>+</button>
      <output className="image-preview-scale" aria-label={t('图片缩放比例')}>{scale ? `${Math.round(scale * 100)}%` : '—'}</output>
    </div>
    <div ref={viewportRef} className={`image-preview-viewport${canPan ? ' image-preview-pannable' : ''}${dragging ? ' image-preview-dragging' : ''}`} style={{ touchAction: canPan ? 'none' : 'auto' }} onPointerDown={startDrag} onPointerMove={moveDrag} onPointerUp={endDrag} onPointerCancel={endDrag} onLostPointerCapture={endDrag}>
      <div className="image-preview-layer" style={{ width: view.naturalWidth, height: view.naturalHeight, visibility: scale ? 'visible' : 'hidden', transform: `translate(-50%, -50%) translate(${view.x}px, ${view.y}px) scale(${scale})` }}>
        <img className="preview-image" src={url} alt={name} draggable={false} onLoad={event => measure(event.currentTarget.naturalWidth, event.currentTarget.naturalHeight)} onError={onError} />
      </div>
    </div>
  </div>;
}
