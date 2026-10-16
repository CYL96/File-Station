import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { ApiError, apiURL, preview } from './api';
import type { Entry } from './api';
import { Markdown, isMarkdown } from './Markdown';
import { ImagePreview } from './ImagePreview';
import { VideoPreview } from './VideoPreview';
import { useFileDialogWidth } from './useFileDialogWidth';
import type { FileDialogKind } from './useFileDialogWidth';
import { getLocale, useI18n } from './i18n';

export type IconName = 'folder' | 'file' | 'image' | 'video' | 'trash' | 'upload' | 'download' | 'plus' | 'search' | 'chevron' | 'refresh' | 'copy' | 'move' | 'edit' | 'close' | 'tasks' | 'check' | 'logout' | 'menu' | 'grid' | 'list' | 'restore' | 'eye' | 'eye-off' | 'lock' | 'help' | 'server' | 'arrow' | 'warning' | 'settings';
const shapes: Record<IconName, ReactNode> = {
  folder: <path d="M3 7a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />,
  file: <><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" /><path d="M14 2v6h6M8 13h8M8 17h5" /></>,
  image: <><rect x="3" y="3" width="18" height="18" rx="2" /><circle cx="8" cy="8" r="1.5" /><path d="m21 15-5-5L5 21" /></>,
  video: <><rect x="3" y="4" width="18" height="16" rx="2" /><path d="m10 8 6 4-6 4z" /></>,
  trash: <><path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7" /></>,
  upload: <><path d="M12 16V3m-5 5 5-5 5 5M4 16v5h16v-5" /></>,
  download: <><path d="M12 3v13m-5-5 5 5 5-5M4 16v5h16v-5" /></>,
  plus: <path d="M12 5v14M5 12h14" />,
  search: <><circle cx="10" cy="10" r="6" /><path d="m15 15 5 5" /></>,
  chevron: <path d="m9 5 7 7-7 7" />,
  refresh: <><path d="M20 7v5h-5M4 17v-5h5" /><path d="M5.5 7a7 7 0 0 1 12-2L20 8M4 16l2.5 3a7 7 0 0 0 12-2" /></>,
  copy: <><rect x="8" y="8" width="13" height="13" rx="2" /><path d="M16 8V3H3v13h5" /></>,
  move: <><path d="M12 3v18M3 12h18m-6-3 3 3-3 3M9 6l3-3 3 3" /></>,
  edit: <><path d="m16 3 5 5-12 12-6 1 1-6zM13 6l5 5" /></>,
  close: <path d="m6 6 12 12M6 18 18 6" />,
  tasks: <><rect x="5" y="4" width="15" height="17" rx="2" /><path d="M9 2v4h7V2M9 11h7M9 15h7" /></>,
  check: <path d="m5 12 4 4L19 6" />,
  logout: <><path d="M10 4H4v16h6M10 12h11m-5-5 5 5-5 5" /></>,
  menu: <path d="M4 6h16M4 12h16M4 18h16" />,
  grid: <><rect x="3" y="3" width="7" height="7" rx="1" /><rect x="14" y="3" width="7" height="7" rx="1" /><rect x="3" y="14" width="7" height="7" rx="1" /><rect x="14" y="14" width="7" height="7" rx="1" /></>,
  list: <path d="M8 5h13M8 12h13M8 19h13M3 5h.01M3 12h.01M3 19h.01" />,
  restore: <><path d="M3 3v6h6M3 9a9 9 0 1 1 1 10M12 7v5l3 2" /></>,
  eye: <><path d="M2 12s4-7 10-7 10 7 10 7-4 7-10 7S2 12 2 12" /><circle cx="12" cy="12" r="3" /></>,
  'eye-off': <><path d="M2 12s4-7 10-7 10 7 10 7-4 7-10 7S2 12 2 12" /><circle cx="12" cy="12" r="3" /><path d="m3 3 18 18" /></>,
  lock: <><rect x="5" y="10" width="14" height="11" rx="2" /><path d="M8 10V7a4 4 0 0 1 8 0v3M12 14v3" /></>,
  help: <><circle cx="12" cy="12" r="9" /><path d="M9 8a3 3 0 0 1 6 1c0 2-3 2-3 4M12 17h.01" /></>,
  server: <><rect x="3" y="3" width="18" height="7" rx="2" /><rect x="3" y="14" width="18" height="7" rx="2" /><path d="M7 6.5h.01M7 17.5h.01M12 6.5h5M12 17.5h5" /></>,
  arrow: <path d="M20 12H4m6-6-6 6 6 6" />,
  warning: <><path d="m12 3 10 18H2zM12 9v5M12 17h.01" /></>,
  settings: <><path d="m9.5 3-.6 2.3-1.3.8-2.3-.6-2.5 4.3 1.7 1.7v1.5l-1.7 1.7 2.5 4.3 2.3-.6 1.3.8.6 2.3h5l.6-2.3 1.3-.8 2.3.6 2.5-4.3-1.7-1.7v-1.5l1.7-1.7-2.5-4.3-2.3.6-1.3-.8-.6-2.3z" /><circle cx="12" cy="12" r="3" /></>,
};
export function Icon({ name, size = 20, className = '' }: { name: IconName; size?: number; className?: string }) {
  return <svg aria-hidden="true" width={size} height={size} className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">{shapes[name]}</svg>;
}
function ThumbnailImage({ src, size }: { src: string; size: number }) {
  const [failed, setFailed] = useState(false);
  return failed ? <Icon name="image" size={size} /> : <img src={src} alt="" loading="lazy" decoding="async" onError={() => setFailed(true)} />;
}
export function ImageThumbnail({ share, entry, onOpen, size = 24 }: { share: string; entry: Entry; onOpen: () => void; size?: number }) {
  const { t } = useI18n();
  const src = apiURL('preview', { share, path: entry.path, modified: entry.modified, size: String(entry.size) });
  return <button type="button" className="file-glyph image-glyph file-thumbnail" aria-label={t('预览 {name}', { name: entry.name })} onClick={event => {
    if (event.shiftKey || event.ctrlKey || event.metaKey || event.altKey) return;
    event.stopPropagation();
    if (event.detail <= 1) onOpen();
  }} onDoubleClick={event => event.stopPropagation()}><ThumbnailImage key={src} src={src} size={size} /></button>;
}
export function Modal({ title, children, onClose, wide = false, headerContent, fileDialog }: { title: string; children: ReactNode; onClose: () => void; wide?: boolean; headerContent?: ReactNode; fileDialog?: { kind: FileDialogKind; content: unknown; mode?: string } }) {
  const { t } = useI18n();
  const ref = useRef<HTMLDialogElement>(null);
  const backdropPress = useRef(false);
  useFileDialogWidth(ref, fileDialog?.kind, fileDialog?.content, fileDialog?.mode);
  useEffect(() => {
    const dialog = ref.current!;
    const focused = document.activeElement as HTMLElement | null;
    dialog.showModal();
    return () => { dialog.close(); focused?.focus(); };
  }, []);
  return <dialog ref={ref} className={`modal ${wide ? 'modal-wide' : ''} ${fileDialog ? `file-dialog file-dialog-${fileDialog.kind}` : ''}`} aria-label={title} onCancel={event => { event.preventDefault(); onClose(); }} onPointerDown={event => { const bounds = event.currentTarget.getBoundingClientRect(); backdropPress.current = event.target === event.currentTarget && (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom); }} onClick={event => { const startedOutside = backdropPress.current; backdropPress.current = false; if (startedOutside && event.target === ref.current) { const bounds = ref.current!.getBoundingClientRect(); if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) onClose(); } }}>
    <header className="modal-heading"><h2>{title}</h2>{headerContent}<button className="button icon-button modal-close" aria-label={t('关闭{title}', { title })} onClick={onClose}><Icon name="close" size={17} />{t('关闭')}</button></header>
    {children}
  </dialog>;
}
export function Preview({ share, entry, onClose, onEdit }: { share: string; entry: Entry; onClose: () => void; onEdit?: () => void }) {
  const { t } = useI18n();
  const [content, setContent] = useState<{ url?: string; videoURL?: string; text?: string; error?: string }>({});
  useEffect(() => {
    setContent({});
    const controller = new AbortController();
    let url: string | undefined;
    preview(share, entry.path, controller.signal).then(async response => {
      if (controller.signal.aborted) return;
      const type = (response.headers.get('Content-Type') ?? '').split(';', 1)[0].trim().toLowerCase();
      if (type === 'video/mp4' || type === 'video/webm') {
        await response.body?.cancel();
        if (!controller.signal.aborted) setContent({ videoURL: apiURL('preview', { share, path: entry.path }) });
      } else if (type.startsWith('image/')) {
        const blob = await response.blob();
        if (controller.signal.aborted) return;
        url = URL.createObjectURL(blob);
        setContent({ url });
      } else {
        const text = await response.text();
        if (!controller.signal.aborted) setContent({ text });
      }
    }).catch(error => { if (!controller.signal.aborted) setContent({ error: error instanceof ApiError ? error.message : '预览加载失败，请检查网络连接，或下载文件查看。' }); });
    return () => { controller.abort(); if (url) URL.revokeObjectURL(url); };
  }, [share, entry.path]);
  return <Modal title={entry.name} wide fileDialog={{ kind: 'preview', content }} onClose={onClose} headerContent={<div className="preview-actions">{content.text !== undefined && onEdit && <button className="button" onClick={onEdit}><Icon name="edit" size={17} />{t('编辑文件')}</button>}<a className="button primary" href={apiURL('download', { share, path: entry.path })} download={entry.name}><Icon name="download" size={17} />{t('下载文件')}</a></div>}>
    <div className={`preview-body${content.url ? ' preview-body-image' : content.videoURL ? ' preview-body-video' : ''}`}>
      {content.error ? <div className="empty-state"><Icon name="file" size={48} /><h3>{t('无法预览此文件')}</h3><p>{t(content.error)}</p><p>{t('支持常见图片、MP4 / WebM 视频以及不超过 1 MiB 的 UTF-8 文本；视频能否播放取决于浏览器支持的编码，其他文件请下载查看。')}</p></div>
        : content.videoURL ? <VideoPreview key={content.videoURL} share={share} path={entry.path} url={content.videoURL} name={entry.name} />
        : content.url ? <ImagePreview key={content.url} url={content.url} name={entry.name} onError={() => setContent({ error: '图片无法解码，文件可能已损坏或不是受支持的图片。' })} />
        : content.text !== undefined ? isMarkdown(entry.name) ? <Markdown text={content.text} share={share} path={entry.path} /> : <pre className="preview-text">{content.text}</pre>
        : <div className="loading-state" role="status"><span className="spinner" />{t('正在加载预览…')}</div>}
    </div>
    <footer className="modal-actions preview-footer"><span>{formatBytes(entry.size)} · {formatDate(entry.modified)}</span><span className="preview-resize-help">{t('拖动右下角可调整宽度')}</span></footer>
  </Modal>;
}
export function formatBytes(value: number) {
  if (value < 1024) return `${value.toLocaleString(getLocale())} B`;
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)) - 1, units.length - 1);
  return `${(value / 1024 ** (index + 1)).toLocaleString(getLocale(), { maximumFractionDigits: 1 })} ${units[index]}`;
}
export function formatDate(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString(getLocale(), { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false });
}
export function fileIcon(entry: { name?: string; path?: string; isDir: boolean }): IconName {
  if (entry.isDir) return 'folder';
  const name = entry.name ?? entry.path ?? '';
  if (/\.(mp4|webm)$/i.test(name)) return 'video';
  return /\.(png|jpe?g|gif|webp)$/i.test(name) ? 'image' : 'file';
}
