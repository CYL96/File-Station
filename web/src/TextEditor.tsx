import { useEffect, useRef, useState } from 'react';
import { ApiError, request } from './api';
import { Icon, Modal, formatBytes } from './ui';
import { Markdown, isMarkdown } from './Markdown';
import { MarkdownVisualEditor } from './MarkdownVisualEditor';

type TextDocument = { content: string; version: string };
export function TextEditor({ share, path, name, onClose, onSaved }: { share: string; path: string; name: string; onClose: () => void; onSaved: () => void }) {
  const [document, setDocument] = useState<TextDocument | null>(null);
  const [text, setText] = useState('');
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [conflict, setConflict] = useState(false);
  const [saved, setSaved] = useState(false);
  const [confirm, setConfirm] = useState<'close' | 'reload' | null>(null);
  const [revision, setRevision] = useState(0);
  const [mode, setMode] = useState<'source' | 'split' | 'visual'>('source');
  const markdown = isMarkdown(name);
  const writing = useRef(false);
  const area = useRef<HTMLTextAreaElement>(null);
  const preview = useRef<HTMLDivElement>(null);
  const scrollProgress = useRef(0);
  const syncedScroll = useRef(new WeakMap<HTMLElement, number>());
  const baseline = document?.content.replace(/\r\n?/g, '\n') ?? '';
  const dirty = document !== null && text !== baseline;
  const crlf = !!document?.content.includes('\r\n') && !document.content.replace(/\r\n/g, '').includes('\n');
  const content = crlf ? text.replace(/\n/g, '\r\n') : text;
  const bytes = new TextEncoder().encode(content).byteLength;
  const tooLarge = bytes > (1 << 20);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true); setDocument(null); setError(''); setConflict(false); setSaved(false);
    request<TextDocument>('files/text', undefined, { share, path }, controller.signal).then(data => {
      if (controller.signal.aborted) return;
      setDocument(data); setText(data.content.replace(/\r\n?/g, '\n'));
    }).catch(cause => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : '读取文件失败'); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [share, path, revision]);
  useEffect(() => {
    if (!dirty) return;
    const guard = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ''; };
    window.addEventListener('beforeunload', guard);
    return () => window.removeEventListener('beforeunload', guard);
  }, [dirty]);
  function setScroll(node: HTMLElement, progress: number) {
    const top = progress * Math.max(0, node.scrollHeight - node.clientHeight);
    if (Math.abs(node.scrollTop - top) < 1) return;
    node.scrollTop = top;
    syncedScroll.current.set(node, node.scrollTop);
  }
  function syncScroll(source: HTMLElement) {
    if (mode !== 'split' || !area.current || !preview.current) return;
    const expected = syncedScroll.current.get(source);
    syncedScroll.current.delete(source);
    if (expected !== undefined && Math.abs(source.scrollTop - expected) < 1) return;
    const range = source.scrollHeight - source.clientHeight;
    if (range <= 0) return;
    scrollProgress.current = Math.max(0, Math.min(1, source.scrollTop / range));
    setScroll(source === area.current ? preview.current : area.current, scrollProgress.current);
  }
  useEffect(() => {
    if (mode !== 'split' || !area.current || !preview.current) return;
    syncedScroll.current = new WeakMap();
    const source = area.current;
    const target = preview.current;
    const range = source.scrollHeight - source.clientHeight;
    scrollProgress.current = range > 0 ? source.scrollTop / range : 0;
    const align = () => {
      setScroll(source, scrollProgress.current);
      setScroll(target, scrollProgress.current);
    };
    align();
    const observer = new ResizeObserver(align);
    observer.observe(source);
    observer.observe(target);
    if (target.firstElementChild) observer.observe(target.firstElementChild);
    return () => observer.disconnect();
  }, [mode, loading]);
  function close() {
    if (writing.current) return;
    if (dirty) setConfirm('close'); else onClose();
  }
  function reload() {
    if (dirty) setConfirm('reload'); else setRevision(value => value + 1);
  }
  async function save() {
    if (!document || loading || writing.current || !dirty || tooLarge || conflict || confirm) return;
    writing.current = true; setBusy(true); setError(''); setSaved(false);
    try {
      const result = await request<{ version: string }>('files/text/save', { share, path, content, version: document.version });
      setDocument({ content, version: result.version }); setSaved(true); onSaved();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '保存文件失败');
      setConflict(cause instanceof ApiError && cause.status === 409);
    } finally { writing.current = false; setBusy(false); }
  }
  return <Modal title={`编辑文件 · ${name}`} wide fileDialog={{ kind: 'editor', content: text, mode }} onClose={close} headerContent={<>
    {markdown && !loading && document && <div className="markdown-modes" role="group" aria-label="Markdown 编辑模式">{([['source', '源代码编辑'], ['split', '对比编辑'], ['visual', '预览编辑']] as const).map(([value, label]) => <button key={value} className="button" aria-pressed={mode === value} disabled={busy} onClick={() => setMode(value)}>{label}</button>)}</div>}
    <div className="editor-actions"><button className="button" disabled={busy || loading || !!confirm} onClick={reload}><Icon name="refresh" size={16} />重新读取</button><button className="button primary" disabled={!document || loading || busy || !dirty || tooLarge || conflict || !!confirm} onClick={() => { void save(); }}>{busy ? <span className="spinner" /> : <Icon name="check" size={16} />}保存修改</button></div>
  </>}><div className="text-editor-body" onKeyDown={event => { if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') { event.preventDefault(); void save(); } }}>
    <p className="field-help editor-path" title={path}>{path}</p>
    <p className="editor-resize-help">拖动右下角可调整宽度{markdown && mode === 'split' ? ' · 源码与预览同步滚动' : ''}</p>
    {loading ? <div className="loading-state" role="status"><span className="spinner" />正在读取文本…</div> : document && <>
      {markdown && mode === 'visual' && <p className="field-help markdown-mode-help">直接编辑渲染内容；修改后会按 Markdown 重新序列化，可能规范化空格、列表及表格格式。需要逐字保留格式时使用源代码编辑。</p>}
      <div className={`editor-workspace ${markdown && mode === 'split' ? 'editor-split' : ''}`}>
      {markdown && mode === 'visual' ? <MarkdownVisualEditor value={text} share={share} path={path} disabled={busy} onChange={value => { setText(value); setSaved(false); }} /> : <><label className="sr-only" htmlFor="file-editor-content">文件内容</label><textarea id="file-editor-content" ref={area} className="file-editor-content" value={text} disabled={busy} spellCheck={false} autoCapitalize="off" autoCorrect="off" wrap="off" onScroll={event => syncScroll(event.currentTarget)} onChange={event => { setText(event.target.value); setSaved(false); }} onKeyDown={event => {
        if (event.key === 'Tab' && !event.shiftKey && !event.ctrlKey && !event.metaKey && !event.altKey) {
          event.preventDefault(); const node = event.currentTarget; const start = node.selectionStart; const end = node.selectionEnd;
          setText(current => current.slice(0, start) + '\t' + current.slice(end)); setSaved(false);
          requestAnimationFrame(() => { area.current?.setSelectionRange(start + 1, start + 1); });
        }
      }} />{markdown && mode === 'split' && <div ref={preview} className="editor-markdown-preview" aria-label="Markdown 实时预览" onScroll={event => syncScroll(event.currentTarget)}><Markdown text={text} share={share} path={path} /></div>}</>}
      </div>
      <div className="editor-status"><span>UTF-8 · {crlf ? 'CRLF' : 'LF'} · {formatBytes(bytes)} / 1 MiB</span><span role="status">{busy ? '正在保存…' : saved ? '已保存' : dirty ? '未保存' : '未修改'}</span></div>
      {tooLarge && <p className="form-error" role="alert">内容超过 1 MiB，缩短后才能保存。</p>}
    </>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {conflict && <p className="field-help">编辑内容仍保留。请复制需要保留的内容，再重新读取文件；不会强制覆盖外部修改。</p>}
    {confirm && <div className="editor-discard" role="alert"><p>有未保存的修改，{confirm === 'close' ? '关闭' : '重新读取'}会丢弃这些修改。</p><div><button className="button" onClick={() => setConfirm(null)}>继续编辑</button><button className="button danger" onClick={() => { const action = confirm; setConfirm(null); if (action === 'close') onClose(); else { setDocument(null); setText(''); setRevision(value => value + 1); } }}>丢弃修改并{confirm === 'close' ? '关闭' : '重新读取'}</button></div></div>}
  </div></Modal>;
}
