import { useEffect, useRef, useState } from 'react';
import type { PointerEvent } from 'react';

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

function fitScale(view: View) {
  if (!view.width || !view.height || !view.naturalWidth || !view.naturalHeight) return 0;
  return Math.min(1, view.width / view.naturalWidth, view.height / view.naturalHeight);
}

function panLimits(view: View) {
  const scale = fitScale(view) * view.zoom;
  return {
    x: Math.max(0, (view.naturalWidth * scale - view.width) / 2),
    y: Math.max(0, (view.naturalHeight * scale - view.height) / 2),
  };
}

function constrain(view: View): View {
  const limits = panLimits(view);
  return {
    ...view,
    x: Math.max(-limits.x, Math.min(limits.x, view.x)),
    y: Math.max(-limits.y, Math.min(limits.y, view.y)),
  };
}

export function ImagePreview({ url, name, onError }: { url: string; name: string; onError: () => void }) {
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
    updateView(constrain({ ...viewRef.current, width: viewport.clientWidth, height: viewport.clientHeight, naturalWidth, naturalHeight }));
  }

  function zoomTo(zoom: number, anchorX = 0, anchorY = 0) {
    const current = viewRef.current;
    if (!fitScale(current)) return;
    const nextZoom = Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, zoom));
    const ratio = nextZoom / current.zoom;
    // Keep the image point beneath the anchor stationary until an edge is reached.
    updateView(constrain({ ...current, zoom: nextZoom, x: anchorX - (anchorX - current.x) * ratio, y: anchorY - (anchorY - current.y) * ratio }));
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
  }, []);

  function startDrag(event: PointerEvent<HTMLDivElement>) {
    if (!event.isPrimary || event.button !== 0 || dragRef.current) return;
    const limits = panLimits(viewRef.current);
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
    updateView(constrain({ ...current, x: current.x + event.clientX - drag.x, y: current.y + event.clientY - drag.y }));
    drag.x = event.clientX;
    drag.y = event.clientY;
  }

  function endDrag(event: PointerEvent<HTMLDivElement>) {
    if (dragRef.current?.pointerId !== event.pointerId) return;
    dragRef.current = null;
    setDragging(false);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
  }

  const scale = fitScale(view) * view.zoom;
  const limits = panLimits(view);
  const canPan = limits.x > 0 || limits.y > 0;

  return <div className="image-preview">
    <div className="image-preview-toolbar" role="group" aria-label="图片缩放">
      <button type="button" className="button" aria-label="缩小图片" disabled={!scale || view.zoom <= MIN_ZOOM} onClick={() => zoomTo(viewRef.current.zoom / ZOOM_STEP)}>−</button>
      <button type="button" className="button" aria-label="适应窗口" disabled={!scale} onClick={() => updateView({ ...viewRef.current, zoom: 1, x: 0, y: 0 })}>适应窗口</button>
      <button type="button" className="button" aria-label="放大图片" disabled={!scale || view.zoom >= MAX_ZOOM} onClick={() => zoomTo(viewRef.current.zoom * ZOOM_STEP)}>+</button>
      <output className="image-preview-scale" aria-label="图片缩放比例">{scale ? `${Math.round(scale * 100)}%` : '—'}</output>
    </div>
    <div ref={viewportRef} className={`image-preview-viewport${canPan ? ' image-preview-pannable' : ''}${dragging ? ' image-preview-dragging' : ''}`} style={{ touchAction: canPan ? 'none' : 'auto' }} onPointerDown={startDrag} onPointerMove={moveDrag} onPointerUp={endDrag} onPointerCancel={endDrag} onLostPointerCapture={endDrag}>
      <div className="image-preview-layer" style={{ width: view.naturalWidth, height: view.naturalHeight, visibility: scale ? 'visible' : 'hidden', transform: `translate(-50%, -50%) translate(${view.x}px, ${view.y}px) scale(${scale})` }}>
        <img className="preview-image" src={url} alt={name} draggable={false} onLoad={event => measure(event.currentTarget.naturalWidth, event.currentTarget.naturalHeight)} onError={onError} />
      </div>
    </div>
  </div>;
}
