import { useEffect, useMemo, useRef, useState } from 'react';
import type { CSSProperties, MouseEvent } from 'react';
import { ApiError, apiURL, configureSession, request, upload } from './api';
import type { Conflict, Entry, FileColumnKey, FileSettings, FileSort, Operation, Session, Share, ShareSettingsChange, Task, TrashEntry } from './api';
import { DestinationDialog, Login, NameDialog } from './dialogs';
import { Icon, ImageThumbnail, Modal, Preview, fileIcon, formatBytes, formatDate } from './ui';
import type { IconName } from './ui';
import { DirectoryTree } from './DirectoryTree';
import { ShareSettings } from './ShareSettings';
import { AdvancedSettings, defaultFileSettings } from './FileSettings';
import { About } from './About';
import { FileTableHeader } from './FileTableHeader';
import { GridSortMenu } from './GridSortMenu';
import { FileSizeDialog } from './FileSizeDialog';
import type { SizeSelection } from './FileSizeDialog';
import { TextEditor } from './TextEditor';

type Item = Entry & { trashId?: string; deletedAt?: string };
type View = { share: string; path: string; trash: boolean };
type ModalState = { kind: 'mkdir' } | { kind: 'rename'; entry: Item } | { kind: 'destination'; operation: Operation } | { kind: 'delete'; items: Item[]; permanent: boolean } | { kind: 'preview'; entry: Item } | { kind: 'edit'; share: string; entry: Item } | { kind: 'help' } | { kind: 'shares' } | { kind: 'advanced' } | { kind: 'about' };
type UploadTask = { id: string; name: string; size: number; loaded: number; status: 'queued' | 'running' | 'done' | 'error' | 'cancelled' | 'skipped'; error?: string; controller: AbortController };
type UploadSource = { file: File; share: string; path: string; conflict: Conflict };
type ConflictPrompt = { title: string; description: string; resolve: (policy: Conflict | null) => void };
const taskLabels: Record<string, string> = { copy: '复制', move: '移动', trash: '移至回收站' };
const statuses: Record<string, string> = { queued: '等待上传', running: '正在处理', done: '已完成', error: '失败', cancelled: '已取消', skipped: '已跳过' };
const columnWidths: Record<FileColumnKey, number> = { name: 145, size: 80, modified: 150, type: 110, extension: 90, permissions: 90, owner: 110, group: 110, created: 150 };
// Upload IDs are page-local UI keys, not security tokens.
let uploadSequence = 0;

export default function App() {
  const [session, setSession] = useState<Session | null>(null);
  const [checking, setChecking] = useState(true);
  const [loginError, setLoginError] = useState('');
  const [shares, setShares] = useState<Share[]>([]);
  const [shareRevision, setShareRevision] = useState(0);
  const [view, setView] = useState<View>({ share: '', path: '', trash: false });
  const [entries, setEntries] = useState<Item[]>([]);
  const [loading, setLoading] = useState(false);
  const [listError, setListError] = useState('');
  const [refresh, setRefresh] = useState(0);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [filter, setFilter] = useState('');
  const [fileSettings, setFileSettings] = useState<FileSettings>(defaultFileSettings);
  const showHidden = fileSettings.showHidden;
  const [sort, setSort] = useState<FileSort>(defaultFileSettings.sort);
  const [layout, setLayout] = useState<'list' | 'grid'>('list');
  const [modal, setModal] = useState<ModalState | null>(null);
  const [conflict, setConflict] = useState<ConflictPrompt | null>(null);
  const [notice, setNotice] = useState<{ text: string; error: boolean } | null>(null);
  const [drawer, setDrawer] = useState(false);
  const [sidebar, setSidebar] = useState(false);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [uploads, setUploads] = useState<UploadTask[]>([]);
  const [retryingTasks, setRetryingTasks] = useState<Set<string>>(new Set());
  const [clearingTasks, setClearingTasks] = useState(false);
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number } | null>(null);
  const [clipboard, setClipboard] = useState<Operation | null>(null);
  const [dragging, setDragging] = useState(false);
  const [actionBusy, setActionBusy] = useState(false);
  const [sizeSelection, setSizeSelection] = useState<SizeSelection | null>(null);
  const [settingsBusy, setSettingsBusy] = useState(false);
  const settingsWriting = useRef(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const filterInput = useRef<HTMLInputElement>(null);
  const anchor = useRef<string | null>(null);
  const pendingRetries = useRef(new Set<string>());
  const previousTasks = useRef(new Map<string, Task['status']>());
  const uploadControllers = useRef(new Set<AbortController>());
  const uploadSources = useRef(new Map<string, UploadSource>());
  const enqueuedUploads = useRef(new Set<string>());
  const conflictQueue = useRef(Promise.resolve());
  const uploadQueue = useRef(Promise.resolve());
  const conflictRef = useRef<ConflictPrompt | null>(null);
  const authEpoch = useRef(0);
  const viewRef = useRef(view);
  const backHistory = useRef<View[]>([]);
  const forwardHistory = useRef<View[]>([]);

  function setCurrentView(next: View) {
    viewRef.current = next;
    setView(next);
  }
  function clearHistory() {
    backHistory.current = []; forwardHistory.current = [];
  }

  function clearAuth() {
    authEpoch.current++;
    clearHistory();
    configureSession(null, clearAuth);
    setSession(null); setShares([]); setEntries([]); setTasks([]); setUploads([]); setModal(null); setDrawer(false); setSelected(new Set()); setClipboard(null); setContextMenu(null);
    setFileSettings(defaultFileSettings); setSort(defaultFileSettings.sort);
    setSizeSelection(null);
    setClearingTasks(false);
    pendingRetries.current.clear(); setRetryingTasks(new Set());
    conflictRef.current?.resolve(null); conflictRef.current = null; setConflict(null);
    for (const controller of uploadControllers.current) controller.abort();
    uploadControllers.current.clear(); uploadSources.current.clear(); enqueuedUploads.current.clear(); previousTasks.current.clear();
  }
  function acceptSession(value: Session) {
    clearHistory();
    configureSession(value, clearAuth); setSession(value); setLoginError(''); setNotice(null);
  }
  function notify(text: string, error = false) {
    setNotice({ text, error });
  }
  async function saveViewSettings(next: FileSettings) {
    if (settingsWriting.current) return;
    settingsWriting.current = true; setSettingsBusy(true);
    const epoch = authEpoch.current;
    try {
      const saved = await request<FileSettings>('settings/files/save', next);
      if (epoch === authEpoch.current) setFileSettings(saved);
    } catch (error) {
      if (epoch === authEpoch.current) notify(error instanceof Error ? error.message : '保存文件视图设置失败', true);
    } finally { settingsWriting.current = false; setSettingsBusy(false); }
  }
  async function reorderColumn(source: FileColumnKey, target: FileColumnKey, after: boolean) {
    if (settingsWriting.current || source === target) return;
    const moved = fileSettings.columns.find(column => column.key === source);
    if (!moved) return;
    const columns = fileSettings.columns.filter(column => column.key !== source);
    const index = columns.findIndex(column => column.key === target);
    if (index < 0) return;
    columns.splice(index + (after ? 1 : 0), 0, moved);
    if (columns.every((column, position) => column.key === fileSettings.columns[position].key)) return;
    await saveViewSettings({ ...fileSettings, columns });
  }
  useEffect(() => {
    configureSession(null, clearAuth);
    request<Session>('session').then(acceptSession).catch(error => { if (!(error instanceof ApiError && error.status === 401)) setLoginError(error instanceof Error ? error.message : '无法连接服务器'); }).finally(() => setChecking(false));
    // A single initial session probe; later auth changes are driven by API responses.
  }, []);
  useEffect(() => {
    if (!session) return;
    const controller = new AbortController();
    request<Share[]>('shares', undefined, undefined, controller.signal).then(data => {
      if (controller.signal.aborted) return;
      clearHistory();
      setShares(data); setCurrentView({ share: data[0]?.id ?? '', path: '', trash: false });
    }).catch(error => { if (!controller.signal.aborted) setListError(error instanceof Error ? error.message : '读取共享目录失败'); });
    return () => controller.abort();
  }, [session]);
  useEffect(() => {
    if (!session) return;
    const controller = new AbortController();
    request<FileSettings>('settings/files', undefined, undefined, controller.signal).then(data => {
      if (!controller.signal.aborted) { setFileSettings(data); setSort(data.sort); }
    }).catch(error => { if (!controller.signal.aborted) notify(error instanceof Error ? error.message : '读取高级设置失败', true); });
    return () => controller.abort();
  }, [session]);
  useEffect(() => {
    if (!session || !view.share) { setLoading(false); return; }
    const controller = new AbortController();
    setLoading(true); setListError(''); setEntries([]); setSelected(new Set()); anchor.current = null;
    const load = view.trash
      ? request<TrashEntry[]>('trash', undefined, { share: view.share }, controller.signal).then(data => data.map(entry => ({ ...entry, name: entry.path.split('/').pop() ?? entry.path, modified: entry.deletedAt, trashId: entry.id })))
      : request<{ entries: Entry[] }>('files', undefined, { share: view.share, path: view.path }, controller.signal).then(data => data.entries);
    load.then(data => { if (!controller.signal.aborted) setEntries(data); }).catch(error => { if (!controller.signal.aborted) setListError(error instanceof Error ? error.message : '读取目录失败'); }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [session, view.share, view.path, view.trash, refresh]);
  useEffect(() => {
    if (!session) return;
    const controller = new AbortController();
    let source: EventSource | undefined;
    let retry: number | undefined;
    let fallback: number | undefined;
    const receive = (data: Task[]) => {
      if (controller.signal.aborted) return;
      for (const task of data) {
        const old = previousTasks.current.get(task.id);
        if (task.status !== 'running' && old !== task.status) setRefresh(value => value + 1);
        previousTasks.current.set(task.id, task.status);
      }
      setTasks(data);
    };
    const poll = () => { request<Task[]>('tasks', undefined, undefined, controller.signal).then(receive).catch(() => { /* Retry on the next poll. */ }); };
    const connect = () => {
      if (controller.signal.aborted) return;
      source = new EventSource(apiURL('events'));
      source.onopen = () => { if (fallback) { clearInterval(fallback); fallback = undefined; } };
      source.onmessage = event => {
        try { receive(JSON.parse(event.data) as Task[]); } catch { /* Ignore malformed events; polling remains available. */ }
      };
      source.onerror = () => {
        source?.close(); poll();
        if (!fallback) fallback = window.setInterval(poll, 5000);
        retry = window.setTimeout(connect, 5000);
      };
    };
    poll(); connect();
    return () => { controller.abort(); source?.close(); clearTimeout(retry); clearInterval(fallback); };
  }, [session]);
  useEffect(() => {
    if (!notice) return;
    const timer = setTimeout(() => setNotice(null), notice.error ? 10000 : 5000);
    return () => clearTimeout(timer);
  }, [notice]);
  useEffect(() => {
    if (!contextMenu) return;
    const close = () => setContextMenu(null);
    window.addEventListener('click', close); window.addEventListener('resize', close); window.addEventListener('scroll', close, true);
    return () => { window.removeEventListener('click', close); window.removeEventListener('resize', close); window.removeEventListener('scroll', close, true); };
  }, [contextMenu]);

  const share = shares.find(item => item.id === view.share);
  const displayColumns = fileSettings.columns.filter(column => layout === 'grid' ? column.key === 'name' : column.visible);
  const tableStyle = { '--file-columns': '36px ' + displayColumns.map(column => column.key === 'name' ? 'minmax(145px, 1fr)' : `${columnWidths[column.key]}px`).join(' '), '--file-min-width': `${36 + displayColumns.reduce((sum, column) => sum + columnWidths[column.key], 0)}px` } as CSSProperties;
  const typeLabel = (entry: Item) => view.trash ? entry.path : entry.isSymlink ? '符号链接' : entry.isDir ? '文件夹' : fileIcon(entry) === 'image' ? '图片' : '文件';
  function cellValue(entry: Item, key: FileColumnKey) {
    switch (key) {
      case 'name': return entry.name;
      case 'size': return entry.isDir ? '—' : formatBytes(entry.size);
      case 'modified': return formatDate(entry.modified);
      case 'created': return entry.created ? formatDate(entry.created) : '—';
      case 'type': return typeLabel(entry);
      default: return entry[key] || '—';
    }
  }
  const readOnly = !share || share.readOnly;
  const visible = useMemo(() => {
    const items = entries.filter(entry => (showHidden || !entry.name.startsWith('.')) && (entry.name.toLocaleLowerCase().includes(filter.toLocaleLowerCase()) || (view.trash && entry.path.toLocaleLowerCase().includes(filter.toLocaleLowerCase()))));
    return items.sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
      let comparison = 0;
      if (sort.key === 'name') comparison = a.name.localeCompare(b.name, 'zh-CN', { numeric: true });
      else if (sort.key === 'size') comparison = a.size - b.size;
      else if (sort.key === 'modified') comparison = new Date(a.modified).getTime() - new Date(b.modified).getTime();
      else if (sort.key === 'created') {
        if (!a.created || !b.created) return a.created ? -1 : b.created ? 1 : a.name.localeCompare(b.name, 'zh-CN', { numeric: true });
        comparison = new Date(a.created).getTime() - new Date(b.created).getTime();
      } else if (sort.key === 'permissions') comparison = parseInt(a.permissions || '0', 8) - parseInt(b.permissions || '0', 8);
      else if (sort.key === 'type') comparison = typeLabel(a).localeCompare(typeLabel(b), 'zh-CN', { numeric: true });
      else comparison = (a[sort.key] || '').localeCompare(b[sort.key] || '', 'zh-CN', { numeric: true });
      return sort.ascending ? comparison : -comparison;
    });
  }, [entries, filter, sort, view.trash, showHidden]);
  const picked = entries.filter(entry => selected.has(entry.trashId ?? entry.path));
  const single = picked.length === 1 ? picked[0] : null;
  const running = tasks.filter(task => task.status === 'running').length + uploads.filter(task => task.status === 'running' || task.status === 'queued').length;
  const completedTasks = tasks.filter(task => task.status === 'done').length + uploads.filter(task => task.status === 'done').length;
  const failedTasks = tasks.filter(task => task.status === 'error').length + uploads.filter(task => task.status === 'error').length;

  async function clearSuccessfulTasks() {
    if (clearingTasks || completedTasks === 0) return;
    const epoch = authEpoch.current;
    const taskIds = tasks.filter(task => task.status === 'done').map(task => task.id);
    const uploadIds = new Set(uploads.filter(task => task.status === 'done').map(task => task.id));
    setClearingTasks(true);
    try {
      await request('tasks/clear', { ids: taskIds });
      if (epoch !== authEpoch.current) return;
      const clearedIds = new Set(taskIds);
      setTasks(current => current.filter(task => !clearedIds.has(task.id)));
      setUploads(current => current.filter(task => !uploadIds.has(task.id)));
      for (const id of taskIds) previousTasks.current.delete(id);
      notify('已清除成功任务');
    } catch (error) {
      if (epoch === authEpoch.current) notify(error instanceof Error ? error.message : '清除失败', true);
    } finally {
      if (epoch === authEpoch.current) setClearingTasks(false);
    }
  }
  const pathParts = view.path.split('/').filter(Boolean);

  function applyNavigation(next: View) {
    setCurrentView(next); setSelected(new Set()); setFilter(''); setSidebar(false); setContextMenu(null);
  }
  function navigate(shareId: string, path = '', trash = false) {
    const next = { share: shareId, path, trash };
    const current = viewRef.current;
    if (current.share !== next.share || current.path !== next.path || current.trash !== next.trash) {
      if (current.share && shares.some(item => item.id === current.share)) backHistory.current.push(current);
      forwardHistory.current = [];
    }
    applyNavigation(next);
  }
  function travelHistory(direction: 'back' | 'forward') {
    const source = direction === 'back' ? backHistory.current : forwardHistory.current;
    const destination = direction === 'back' ? forwardHistory.current : backHistory.current;
    const current = viewRef.current;
    while (source.length) {
      const next = source.pop()!;
      if (!shares.some(item => item.id === next.share) || (current.share === next.share && current.path === next.path && current.trash === next.trash)) continue;
      if (shares.some(item => item.id === current.share)) destination.push(current);
      applyNavigation(next);
      return;
    }
  }
  async function refreshShares(change: ShareSettingsChange) {
    const epoch = authEpoch.current;
    setClipboard(null); setSelected(new Set()); setEntries([]); setContextMenu(null); setListError(''); anchor.current = null;
    if (change.removed || change.pathChanged) {
      backHistory.current = backHistory.current.filter(item => item.share !== change.id);
      forwardHistory.current = [];
    }
    const current = viewRef.current;
    if (current.share === change.id) {
      if (change.removed) setCurrentView({ share: shares.find(item => item.id !== change.id)?.id ?? '', path: '', trash: false });
      else if (change.pathChanged) setCurrentView({ ...current, path: '', trash: false });
    }
    setShareRevision(value => value + 1); setRefresh(value => value + 1);
    try {
      const data = await request<Share[]>('shares');
      if (epoch !== authEpoch.current) return;
      setShares(data);
      backHistory.current = backHistory.current.filter(item => data.some(share => share.id === item.share));
      if (shares.some(item => !data.some(share => share.id === item.id))) forwardHistory.current = [];
      else forwardHistory.current = forwardHistory.current.filter(item => data.some(share => share.id === item.share));
      if (!data.some(item => item.id === viewRef.current.share)) setCurrentView({ share: data[0]?.id ?? '', path: '', trash: false });
    } catch (error) {
      if (epoch === authEpoch.current) setListError(error instanceof Error ? error.message : '读取共享目录失败');
      throw error;
    }
  }
  function select(entry: Item, event?: Pick<MouseEvent, 'shiftKey' | 'ctrlKey' | 'metaKey'>, toggle = false) {
    const key = entry.trashId ?? entry.path;
    setSelected(current => {
      if (event?.shiftKey && anchor.current) {
        const start = visible.findIndex(item => (item.trashId ?? item.path) === anchor.current);
        const end = visible.findIndex(item => (item.trashId ?? item.path) === key);
        if (start >= 0 && end >= 0) return new Set([...current, ...visible.slice(Math.min(start, end), Math.max(start, end) + 1).map(item => item.trashId ?? item.path)]);
      }
      anchor.current = key;
      if (toggle || event?.ctrlKey || event?.metaKey) { const next = new Set(current); if (next.has(key)) next.delete(key); else next.add(key); return next; }
      return new Set([key]);
    });
  }
  function openEntry(entry: Item) {
    if (view.trash) return;
    if (entry.isSymlink) { notify('符号链接不可进入或预览，以保护共享目录边界。', true); return; }
    if (entry.isDir) navigate(view.share, entry.path);
    else setModal({ kind: 'preview', entry });
  }
  function askConflict(title: string, description: string, signal?: AbortSignal): Promise<Conflict | null> {
    const { promise, resolve } = Promise.withResolvers<Conflict | null>();
    const epoch = authEpoch.current;
    conflictQueue.current = conflictQueue.current.then(async () => {
      if (epoch !== authEpoch.current || signal?.aborted) { resolve(null); return; }
      const { promise: answer, resolve: choose } = Promise.withResolvers<void>();
      const onAbort = () => prompt.resolve(null);
      const prompt: ConflictPrompt = { title, description, resolve: choice => {
        signal?.removeEventListener('abort', onAbort); conflictRef.current = null; setConflict(null); resolve(choice); choose();
      } };
      conflictRef.current = prompt; setConflict(prompt);
      signal?.addEventListener('abort', onAbort, { once: true });
      await answer;
    });
    return promise;
  }
  async function withConflict(action: (policy: Conflict) => Promise<unknown>, title: string): Promise<boolean> {
    const epoch = authEpoch.current;
    let policy: Conflict = 'error';
    while (epoch === authEpoch.current) {
      try { await action(policy); return epoch === authEpoch.current; }
      catch (error) {
        if (epoch !== authEpoch.current) return false;
        if (!(error instanceof ApiError && error.status === 409)) throw error;
        const answer = await askConflict(title, error.message);
        if (!answer) return false;
        policy = answer;
      }
    }
    return false;
  }
  async function runOperation(operation: Operation) {
    const epoch = authEpoch.current;
    const result = await request<{ id: string }>('operation', operation);
    if (epoch !== authEpoch.current) return;
    setTasks(current => current.some(task => task.id === result.id) ? current : [{ id: result.id, kind: operation.kind, status: 'running', completed: 0, total: operation.paths.length, canRetry: false }, ...current]);
    if (!previousTasks.current.has(result.id)) previousTasks.current.set(result.id, 'running');
    setDrawer(true);
  }
  async function startOperation(operation: Operation) {
    const success = await withConflict(policy => runOperation({ ...operation, conflict: policy }), '目标位置存在同名项目');
    if (success) notify('任务已加入任务中心');
  }
  async function retryTask(task: Task) {
    if (task.status !== 'error' || !task.canRetry || pendingRetries.current.has(task.id)) return;
    const epoch = authEpoch.current;
    pendingRetries.current.add(task.id);
    setRetryingTasks(current => new Set(current).add(task.id));
    const submit = async (policy?: Conflict) => {
      const result = await request<{ id: string }>('tasks/retry', { id: task.id, conflict: policy });
      if (epoch !== authEpoch.current) return;
      setTasks(current => current.some(item => item.id === result.id) ? current : [{ id: result.id, kind: task.kind, status: 'running', completed: 0, total: task.total - task.completed, canRetry: false }, ...current]);
      if (!previousTasks.current.has(result.id)) previousTasks.current.set(result.id, 'running');
      setDrawer(true);
    };
    try {
      if (task.kind === 'trash') await submit();
      else {
        const policy = await askConflict('选择重试时的同名文件处理方式', task.error ?? '将仅重试未完成的项目；已完成的项目不会撤销或重复执行。');
        if (!policy || epoch !== authEpoch.current) return;
        await submit(policy);
      }
    } catch (error) {
      if (epoch === authEpoch.current) notify(error instanceof Error ? error.message : '重试失败', true);
    } finally {
      if (epoch === authEpoch.current) {
        pendingRetries.current.delete(task.id);
        setRetryingTasks(current => { const next = new Set(current); next.delete(task.id); return next; });
      }
    }
  }
  function destination(kind: 'copy' | 'move') {
    if (!picked.length || view.trash || (kind === 'move' && readOnly)) return;
    setModal({ kind: 'destination', operation: { kind, share: view.share, paths: picked.map(item => item.path), conflict: 'error' } });
  }
  function deleteSelection(permanent = false) {
    if (picked.length && !readOnly) setModal({ kind: 'delete', items: picked, permanent: view.trash || permanent });
  }
  function downloadSelected() {
    for (const entry of picked.filter(item => !item.isDir && !item.isSymlink)) {
      const link = document.createElement('a'); link.href = apiURL('download', { share: view.share, path: entry.path }); link.download = entry.name;
      document.body.append(link); link.click(); link.remove();
    }
  }
  async function restore(items: Item[]) {
    if (readOnly || !items.length) return;
    setActionBusy(true);
    try {
      let restored = 0;
      for (const item of items) {
        if (await withConflict(policy => request('trash/restore', { share: view.share, id: item.trashId, conflict: policy }), `恢复「${item.name}」时发生冲突`)) restored++;
      }
      setRefresh(value => value + 1); if (restored) notify(`已处理 ${restored} 个恢复项目`);
    } catch (error) { notify(error instanceof Error ? error.message : '恢复失败', true); setRefresh(value => value + 1); }
    finally { setActionBusy(false); }
  }
  function enqueueUploads(jobs: UploadTask[]) {
    const epoch = authEpoch.current;
    for (const task of jobs) {
      enqueuedUploads.current.add(task.id);
      uploadControllers.current.add(task.controller);
    }
    const update = (id: string, patch: Partial<UploadTask>) => {
      if (epoch === authEpoch.current) setUploads(current => current.map(task => task.id === id ? { ...task, ...patch } : task));
    };
    uploadQueue.current = uploadQueue.current.then(async () => {
      for (const task of jobs) {
        if (epoch !== authEpoch.current) break;
        const source = uploadSources.current.get(task.id);
        const signal = task.controller.signal;
        try {
          if (!source || signal.aborted) throw new DOMException('上传已取消', 'AbortError');
          update(task.id, { status: 'running' });
          let result: { skipped?: boolean };
          while (true) {
            try {
              result = await upload(source.file, source.share, source.path, source.conflict, loaded => {
                if (!signal.aborted) update(task.id, { loaded });
              }, signal);
              break;
            }
            catch (error) {
              if (epoch !== authEpoch.current || signal.aborted) throw new DOMException('上传已取消', 'AbortError');
              if (!(error instanceof ApiError && error.status === 409)) throw error;
              const answer = await askConflict(`上传「${task.name}」时发生冲突`, error.message, signal);
              if (!answer) throw new DOMException('上传已取消', 'AbortError');
              source.conflict = answer; update(task.id, { loaded: 0 });
            }
          }
          uploadSources.current.delete(task.id);
          update(task.id, signal.aborted ? { status: 'cancelled' } : { status: result.skipped ? 'skipped' : 'done', loaded: task.size });
        } catch (error) {
          const cancelled = signal.aborted || (error instanceof DOMException && error.name === 'AbortError');
          if (cancelled) uploadSources.current.delete(task.id);
          update(task.id, { status: cancelled ? 'cancelled' : 'error', error: cancelled ? undefined : error instanceof Error ? error.message : '上传失败' });
        } finally {
          uploadControllers.current.delete(task.controller);
          enqueuedUploads.current.delete(task.id);
          if (epoch === authEpoch.current && source && viewRef.current.share === source.share && viewRef.current.path === source.path) setRefresh(value => value + 1);
        }
      }
    });
  }
  function queueUploads(files: File[]) {
    if (readOnly || view.trash || !files.length) return;
    const jobs = files.map(file => {
      const task: UploadTask = { id: `upload-${++uploadSequence}`, name: file.name, size: file.size, loaded: 0, status: 'queued', controller: new AbortController() };
      uploadSources.current.set(task.id, { file, share: view.share, path: view.path, conflict: 'error' });
      return task;
    });
    setUploads(current => [...jobs, ...current]); setDrawer(true);
    enqueueUploads(jobs);
  }
  function retryUpload(task: UploadTask) {
    if (task.status !== 'error' || !uploadSources.current.has(task.id) || enqueuedUploads.current.has(task.id)) return;
    const next: UploadTask = { ...task, status: 'queued', loaded: 0, error: undefined, controller: new AbortController() };
    setUploads(current => current.map(item => item.id === task.id ? next : item));
    enqueueUploads([next]);
  }
  function cancelUpload(task: UploadTask) {
    task.controller.abort();
    uploadSources.current.delete(task.id);
    setUploads(current => current.map(item => item.id === task.id ? { ...item, status: 'cancelled', error: undefined } : item));
  }
  async function paste() {
    if (!clipboard || readOnly || view.trash) return;
    try { await startOperation({ ...clipboard, destinationShare: view.share, destination: view.path }); if (clipboard.kind === 'move') setClipboard(null); }
    catch (error) { notify(error instanceof Error ? error.message : '粘贴失败', true); }
  }
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (!session || event.defaultPrevented) return;
      const target = event.target as HTMLElement;
      if (target.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="dialog"], dialog') || modal || conflict || sizeSelection || document.querySelector('dialog[open]')) return;
      const command = event.ctrlKey || event.metaKey;
      if (event.altKey && !command && !event.shiftKey && (event.key === 'ArrowLeft' || event.key === 'ArrowRight')) {
        event.preventDefault();
        if (!target.closest('[role="columnheader"]')) travelHistory(event.key === 'ArrowLeft' ? 'back' : 'forward');
      } else if (event.key === 'Escape') { setContextMenu(null); setDrawer(false); setSelected(new Set()); }
      else if (command && event.key.toLowerCase() === 'a') { event.preventDefault(); setSelected(new Set(visible.map(entry => entry.trashId ?? entry.path))); }
      else if (command && event.key.toLowerCase() === 'f') { event.preventDefault(); filterInput.current?.focus(); }
      else if (command && (event.key.toLowerCase() === 'c' || event.key.toLowerCase() === 'x') && picked.length && !view.trash) {
        const cut = event.key.toLowerCase() === 'x'; if (cut && readOnly) return;
        event.preventDefault(); setClipboard({ kind: cut ? 'move' : 'copy', share: view.share, paths: picked.map(entry => entry.path), conflict: 'error' }); notify(`${picked.length} 个项目已${cut ? '剪切' : '复制'}，到目标目录按 Ctrl / ⌘ + V 粘贴`);
      } else if (command && event.key.toLowerCase() === 'v' && clipboard) { event.preventDefault(); void paste(); }
      else if (event.key === 'F2' && single && !readOnly && !view.trash) { event.preventDefault(); setModal({ kind: 'rename', entry: single }); }
      else if (event.key === 'Delete' && picked.length && !readOnly) { event.preventDefault(); deleteSelection(); }
      else if (event.key === 'Enter' && single && !target.closest('button, a')) { event.preventDefault(); openEntry(single); }
      else if (event.altKey && event.key === 'ArrowUp' && view.path && !view.trash) { event.preventDefault(); navigate(view.share, pathParts.slice(0, -1).join('/')); }
    };
    window.addEventListener('keydown', onKey); return () => window.removeEventListener('keydown', onKey);
  });

  if (checking) return <main className="session-loading"><div className="brand-mark"><Icon name="folder" size={36} /></div><span className="spinner" /><p>正在连接 File Station…</p></main>;
  if (!session) return <Login onLogin={acceptSession} initialError={loginError} />;

  const actions: { label: string; icon: IconName; disabled: boolean; run: () => void; danger?: boolean }[] = view.trash ? [
    { label: '恢复', icon: 'restore', disabled: !picked.length || readOnly || actionBusy, run: () => { void restore(picked); } },
    { label: '永久删除', icon: 'trash', disabled: !picked.length || readOnly || actionBusy, run: () => deleteSelection(), danger: true },
  ] : [
    { label: '预览', icon: 'eye', disabled: !single || !!single.isDir || !!single.isSymlink, run: () => { if (single) openEntry(single); } },
    { label: '编辑文件', icon: 'edit', disabled: !single || !!single.isDir || !!single.isSymlink || readOnly, run: () => { if (single) setModal({ kind: 'edit', share: view.share, entry: single }); } },
    { label: '下载', icon: 'download', disabled: !picked.some(entry => !entry.isDir && !entry.isSymlink), run: downloadSelected },
    { label: '复制到', icon: 'copy', disabled: !picked.length || !shares.some(item => !item.readOnly), run: () => destination('copy') },
    { label: '移动到', icon: 'move', disabled: !picked.length || readOnly, run: () => destination('move') },
    { label: '重命名', icon: 'edit', disabled: !single || readOnly, run: () => { if (single) setModal({ kind: 'rename', entry: single }); } },
    { label: '删除', icon: 'trash', disabled: !picked.length || readOnly, run: () => deleteSelection(), danger: true },
  ];
  if (picked.length) actions.push({ label: '计算大小', icon: 'server', disabled: false, run: () => { setSizeSelection({ share: view.share, trash: view.trash, items: picked.map(entry => ({ path: entry.trashId ?? entry.path, name: entry.name })) }); } });
  return <div className="desktop">
    <header className="desktop-topbar"><a className="desktop-brand" href="/" onClick={event => { event.preventDefault(); if (shares[0]) navigate(shares[0].id); }}>文件管理工作空间</a><div className="topbar-right"><button className="topbar-button share-settings-entry" onClick={() => { setDrawer(false); setSidebar(false); setContextMenu(null); setModal({ kind: 'shares' }); }} aria-label="共享设置"><Icon name="folder" /><span>共享设置</span></button><button className={`topbar-button ${drawer ? 'active' : ''}`} onClick={() => setDrawer(!drawer)} aria-label="任务中心" aria-expanded={drawer}><Icon name="tasks" /><span className="topbar-button-text">任务中心</span>{running > 0 && <span className="task-badge">{running}</span>}</button><button className="topbar-button" aria-label="退出登录" onClick={async () => { try { await request('logout', {}); clearAuth(); } catch (error) { notify(error instanceof Error ? error.message : '退出失败', true); } }}><span className="avatar">A</span><span className="topbar-button-text">管理员</span><Icon name="logout" size={16} /></button></div></header>
    <div className="workspace"><aside className={`sidebar ${sidebar ? 'sidebar-open' : ''}`}><div className="sidebar-label">共享空间 <span>{shares.length}</span></div><DirectoryTree key={shareRevision} shares={shares} currentShare={view.share} currentPath={view.path} trash={view.trash} refresh={refresh} showHidden={showHidden} navigate={navigate} /><div className="sidebar-rule" /><button className={`share-button trash-link ${view.trash ? 'selected' : ''}`} onClick={() => { if (view.share) navigate(view.share, '', true); }} disabled={!view.share}><Icon name="trash" /><span>回收站</span></button><div className="sidebar-bottom"><button className="shortcut-link" onClick={() => setModal({ kind: 'help' })}><Icon name="help" size={16} />键盘快捷键<span>?</span></button></div></aside>{sidebar && <button className="sidebar-backdrop" aria-label="收起导航" onClick={() => setSidebar(false)} />}
      <main className="file-main">
        <input className="sr-only" ref={fileInput} type="file" multiple aria-label="选择上传文件" onChange={event => { queueUploads(Array.from(event.target.files ?? [])); event.target.value = ''; }} />
        <section className="file-panel" aria-label={view.trash ? '回收站文件' : '文件列表'} onDragOver={event => { if (!readOnly && !view.trash && event.dataTransfer.types.includes('Files')) { event.preventDefault(); setDragging(true); } }} onDragLeave={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragging(false); }} onDrop={event => { event.preventDefault(); setDragging(false); queueUploads(Array.from(event.dataTransfer.files)); }}>
          <div className="path-bar"><button className="icon-button mobile-menu" aria-label="展开共享目录" onClick={() => setSidebar(!sidebar)} aria-expanded={sidebar}><Icon name="menu" /></button><button className="icon-button" aria-label="上一级目录" disabled={!view.path || view.trash} onClick={() => navigate(view.share, pathParts.slice(0, -1).join('/'))}><Icon name="arrow" size={17} /></button><nav className="breadcrumbs" aria-label="当前路径"><button onClick={() => navigate(view.share, '', view.trash)}><Icon name={view.trash ? 'trash' : 'folder'} size={17} /><span>{share?.name ?? '共享目录'}{view.trash ? ' · 回收站' : ''}</span></button>{!view.trash && pathParts.map((part, index) => <span key={index}><Icon name="chevron" size={12} /><button onClick={() => navigate(view.share, pathParts.slice(0, index + 1).join('/'))}>{part}</button></span>)}</nav>{readOnly && share && <span className="readonly-badge"><Icon name="lock" size={12} />只读</span>}{view.trash ? <select aria-label="回收站共享目录" className="text-input trash-share-select" value={view.share} onChange={event => navigate(event.target.value, '', true)}>{shares.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select> : <div className="path-actions"><button className="button" disabled={readOnly} onClick={() => setModal({ kind: 'mkdir' })}><Icon name="plus" size={18} />新建文件夹</button><button className="button primary" disabled={readOnly} onClick={() => fileInput.current?.click()}><Icon name="upload" size={18} />上传文件</button></div>}<button className={`icon-button ${loading ? 'is-loading' : ''}`} aria-label="刷新文件列表" onClick={() => setRefresh(value => value + 1)}><Icon name="refresh" size={17} /></button></div>
          <div className="toolbar"><div className="selection-actions">{actions.map(action => <button key={action.label} className={`toolbar-button ${action.danger ? 'danger-text' : ''}`} disabled={action.disabled} onClick={action.run} title={action.label}><Icon name={action.icon} size={17} /><span>{action.label}</span></button>)}{clipboard && !view.trash && <button className="toolbar-button" disabled={readOnly} onClick={() => { void paste(); }}><Icon name="copy" size={17} /><span>粘贴</span></button>}</div><div className="list-tools"><button className="button advanced-settings-entry" disabled={settingsBusy} onClick={async () => { setContextMenu(null); try { const data = await request<FileSettings>('settings/files'); setFileSettings(data); setModal({ kind: 'advanced' }); } catch (error) { notify(error instanceof Error ? error.message : '读取高级设置失败', true); } }}><Icon name="menu" size={16} />高级设置</button><label className="filter-field"><Icon name="search" size={16} /><input ref={filterInput} aria-label="筛选当前目录" placeholder="筛选当前目录…" value={filter} onChange={event => setFilter(event.target.value)} />{filter && <button aria-label="清除筛选" onClick={() => setFilter('')}><Icon name="close" size={13} /></button>}</label><div className="view-switch" role="group" aria-label="文件视图"><button aria-label="列表视图" aria-pressed={layout === 'list'} onClick={() => setLayout('list')}><Icon name="list" size={17} /></button><button aria-label="网格视图" aria-pressed={layout === 'grid'} onClick={() => setLayout('grid')}><Icon name="grid" size={17} /></button></div></div></div>
          <div className="file-area" tabIndex={0} aria-label="文件选择区域" onClick={event => { if (event.target === event.currentTarget) setSelected(new Set()); }} onContextMenu={event => { if (event.target === event.currentTarget) { event.preventDefault(); setContextMenu({ x: Math.min(event.clientX, window.innerWidth - 210), y: Math.min(event.clientY, window.innerHeight - 310) }); } }}>
            {loading ? <div className="loading-state" role="status"><span className="spinner" />正在读取文件…</div> : listError ? <div className="empty-state" role="alert"><div className="empty-icon error-icon"><Icon name="warning" size={36} /></div><h3>暂时无法读取目录</h3><p>{listError}</p><button className="button" onClick={() => setRefresh(value => value + 1)}><Icon name="refresh" size={16} />重新加载</button><button className="button" onClick={() => setModal({ kind: 'shares' })}><Icon name="folder" size={16} />共享设置</button></div> : !shares.length ? <div className="empty-state"><div className="empty-icon"><Icon name="server" size={42} /></div><h3>尚未添加共享目录</h3><p>首次使用不预设共享或主目录。请打开共享设置，手动添加服务账号可访问的目录；Docker 挂载到 /mnt 等位置的目录也需要手动添加。</p><button className="button primary" onClick={() => setModal({ kind: 'shares' })}><Icon name="folder" size={17} />添加共享目录</button></div> : visible.length === 0 ? <div className="empty-state"><div className="empty-icon"><Icon name={filter ? 'search' : view.trash ? 'trash' : 'folder'} size={44} /></div><h3>{filter ? '没有匹配的文件' : view.trash ? '回收站是空的' : '给这里添一些内容'}</h3><p>{filter ? '试试其他名称，筛选仅在当前目录中进行。' : view.trash ? '移至回收站的文件会显示在这里。' : readOnly ? '此共享目录目前没有文件。' : '拖放文件到这里，或使用上方按钮上传。'}</p>{filter ? <button className="button" onClick={() => setFilter('')}>清除筛选</button> : !view.trash && !readOnly && <button className="button primary" onClick={() => fileInput.current?.click()}><Icon name="upload" size={17} />上传文件</button>}</div> : <>
              <div className={`file-table configured-table ${layout === 'grid' ? 'grid-table' : ''}`} style={tableStyle} role="table" aria-label="目录内容"><FileTableHeader columns={displayColumns} layout={layout} sort={sort} trash={view.trash} busy={settingsBusy} onSort={key => setSort(current => ({ key, ascending: current.key === key ? !current.ascending : true }))} gridSort={<GridSortMenu sort={sort} trash={view.trash} onChange={setSort} />} onReorder={reorderColumn} selection={<input type="checkbox" aria-label="选择全部文件" checked={visible.length > 0 && visible.every(entry => selected.has(entry.trashId ?? entry.path))} ref={node => { if (node) node.indeterminate = selected.size > 0 && !visible.every(entry => selected.has(entry.trashId ?? entry.path)); }} onChange={event => setSelected(event.target.checked ? new Set(visible.map(entry => entry.trashId ?? entry.path)) : new Set())} />} />
                <div className="table-body">{visible.map(entry => { const key = entry.trashId ?? entry.path; const isSelected = selected.has(key); return <div key={key} role="row" aria-selected={isSelected} className={`file-row ${isSelected ? 'row-selected' : ''} ${entry.isDir ? 'directory-entry' : ''}`} onClick={event => select(entry, event)} onDoubleClick={() => openEntry(entry)} onContextMenu={event => { event.preventDefault(); if (!selected.has(key)) setSelected(new Set([key])); setContextMenu({ x: Math.max(8, Math.min(event.clientX, window.innerWidth - 210)), y: Math.max(8, Math.min(event.clientY, window.innerHeight - 310)) }); }}><div role="cell" className="check-cell"><input type="checkbox" aria-label={`选择 ${entry.name}`} checked={isSelected} onClick={event => event.stopPropagation()} onChange={() => select(entry, undefined, true)} /></div>{displayColumns.map(column => column.key === 'name' ? <div key={column.key} role="cell" className="file-name-cell">{!entry.isDir && !entry.isSymlink && !view.trash && fileIcon(entry) === 'image' ? <ImageThumbnail share={view.share} entry={entry} onOpen={() => openEntry(entry)} size={layout === 'grid' ? 43 : 24} /> : <span className={`file-glyph ${entry.isDir ? 'folder-glyph' : fileIcon(entry) === 'image' ? 'image-glyph' : ''}`}><Icon name={fileIcon(entry)} size={layout === 'grid' ? 43 : 24} /></span>}<button className="file-name" title={entry.name} onClick={event => { event.stopPropagation(); if (view.trash) select(entry, event); else openEntry(entry); }}><span>{entry.name}</span>{entry.isSymlink && <span className="symlink-badge" title="符号链接">↗</span>}</button></div> : <div key={column.key} role="cell" className={`column-${column.key}`} title={view.trash && column.key === 'type' ? entry.path : undefined}>{cellValue(entry, column.key)}</div>)}</div>; })}</div></div>
            </>}
          </div><footer className="file-status"><span>{visible.length !== entries.length ? `${visible.length} / ${entries.length}` : visible.length} 个项目{selected.size > 0 && <><span className="status-divider">·</span>已选择 {selected.size} 个<span className="selected-size">（{formatBytes(picked.reduce((sum, entry) => sum + (entry.isDir ? 0 : entry.size), 0))}）</span></>}</span><span className="status-hint">{readOnly ? <><Icon name="lock" size={12} />只读共享目录</> : view.trash ? '永久删除后无法恢复' : <><Icon name="upload" size={13} />支持拖放上传</>}</span></footer>
          {dragging && <div className="drop-overlay"><Icon name="upload" size={48} /><h3>松开即可上传</h3><p>文件将保存到当前目录</p></div>}
        </section>
      </main>
    </div>
    {sizeSelection && <FileSizeDialog selection={sizeSelection} onClose={() => setSizeSelection(null)} />}
    {notice && <div className={`toast ${notice.error ? 'toast-error' : ''}`} role={notice.error ? 'alert' : 'status'}><Icon name={notice.error ? 'warning' : 'check'} size={19} /><span>{notice.text}</span><button aria-label="关闭提示" onClick={() => setNotice(null)}><Icon name="close" size={16} /></button></div>}
    {contextMenu && <div className="context-menu" role="menu" aria-label="文件操作菜单" style={{ left: contextMenu.x, top: contextMenu.y }} onKeyDown={event => { if (event.key === 'Escape') setContextMenu(null); if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); const buttons = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')); const index = buttons.indexOf(document.activeElement as HTMLButtonElement); buttons[(index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length]?.focus(); } }}>{actions.map((action, index) => <button key={action.label} autoFocus={index === 0} role="menuitem" disabled={action.disabled} className={action.danger ? 'danger-text' : ''} onClick={() => { setContextMenu(null); action.run(); }}><Icon name={action.icon} size={17} />{action.label}</button>)}{!view.trash && <><div className="menu-divider" /><button role="menuitem" disabled={readOnly} onClick={() => { setContextMenu(null); setModal({ kind: 'mkdir' }); }}><Icon name="plus" size={17} />新建文件夹</button><button role="menuitem" disabled={!clipboard || readOnly} onClick={() => { setContextMenu(null); void paste(); }}><Icon name="copy" size={17} />粘贴</button></>}</div>}
    {drawer && <>
      <button className="drawer-backdrop" aria-label="关闭任务中心" onClick={() => setDrawer(false)} />
      <aside className="task-drawer" aria-label="任务中心">
        <header className="drawer-header">
          <button className="button clear-successful-tasks" disabled={clearingTasks || completedTasks === 0} onClick={() => { void clearSuccessfulTasks(); }}><Icon name="trash" size={15} />{clearingTasks ? '正在清除…' : '一键清除已成功'}</button>
          <button className="icon-button" aria-label="关闭任务中心" onClick={() => setDrawer(false)}><Icon name="close" /></button>
        </header>
        <dl className="task-statistics" aria-label="任务统计" aria-live="polite">
          <div><dt>全部</dt><dd>{tasks.length + uploads.length}</dd></div>
          <div className="stat-done"><dt>已完成</dt><dd>{completedTasks}</dd></div>
          <div className="stat-running"><dt>进行中</dt><dd>{running}</dd></div>
          <div className="stat-error"><dt>失败</dt><dd>{failedTasks}</dd></div>
        </dl>
        <div className="task-list">
          {uploads.length === 0 && tasks.length === 0 && <div className="empty-state"><Icon name="tasks" size={42} /><h3>还没有任务</h3><p>上传、复制、移动和删除任务会显示在这里。</p></div>}
          {uploads.map(task => <article key={task.id} className="task-card"><div className="task-card-heading"><span className="task-icon"><Icon name="upload" size={19} /></span><div><h3 title={task.name}>{task.name}</h3><p>上传 · {formatBytes(task.size)}</p></div><span className={`task-state state-${task.status}`}>{statuses[task.status]}</span></div><progress aria-label={`${task.name} 上传进度`} max={Math.max(task.size, 1)} value={task.status === 'done' ? Math.max(task.size, 1) : task.loaded} /><div className="task-card-footer"><span>{task.status === 'running' && task.loaded >= task.size ? '服务器正在保存…' : `${formatBytes(task.loaded)} / ${formatBytes(task.size)}`}</span>{(task.status === 'running' || task.status === 'queued') && <button onClick={() => cancelUpload(task)} aria-label={`取消上传 ${task.name}`}>取消</button>}{task.status === 'error' && uploadSources.current.has(task.id) && <button disabled={enqueuedUploads.current.has(task.id)} onClick={() => retryUpload(task)} aria-label={`重试上传 ${task.name}`}>重试</button>}</div>{task.error && task.status === 'error' && <p className="task-error">{task.error}</p>}</article>)}
          {tasks.map(task => <article key={task.id} className="task-card"><div className="task-card-heading"><span className="task-icon"><Icon name={task.kind === 'copy' ? 'copy' : task.kind === 'move' ? 'move' : 'trash'} size={19} /></span><div><h3>{taskLabels[task.kind] ?? task.kind}</h3><p className="task-id">任务 {task.id.slice(0, 8)}</p></div><span className={`task-state state-${task.status}`}>{statuses[task.status]}</span></div><progress aria-label={`${taskLabels[task.kind] ?? task.kind}任务进度`} max={Math.max(task.total, task.completed, 1)} value={task.status === 'done' ? Math.max(task.total, task.completed, 1) : task.completed} /><div className="task-card-footer"><span>{task.completed} / {task.total} 个项目</span>{task.status === 'running' && <button aria-label={`取消任务 ${task.id}`} onClick={async () => { try { await request('tasks/cancel', { id: task.id }); notify('已发送任务取消请求'); } catch (error) { notify(error instanceof Error ? error.message : '取消失败', true); } }}>取消任务</button>}{task.status === 'error' && task.canRetry && <button disabled={retryingTasks.has(task.id)} onClick={() => { void retryTask(task); }} aria-label={`重试任务 ${task.id}`}>重试</button>}</div>{task.error && <p className="task-error">{task.error}</p>}</article>)}
        </div>
      </aside>
    </>}
    {modal?.kind === 'shares' && <ShareSettings onClose={() => setModal(null)} onChanged={refreshShares} />}
    {modal?.kind === 'advanced' && <AdvancedSettings value={fileSettings} onClose={() => setModal(null)} onAbout={() => setModal({ kind: 'about' })} onSaved={data => { setFileSettings(data); setSelected(new Set()); anchor.current = null; setContextMenu(null); setModal(null); notify('高级设置已保存'); }} />}
    {modal?.kind === 'about' && <About onClose={() => setModal(null)} />}
    {modal?.kind === 'edit' && <TextEditor share={modal.share} path={modal.entry.path} name={modal.entry.name} onClose={() => setModal(null)} onSaved={() => setRefresh(value => value + 1)} />}
    {modal?.kind === 'mkdir' && !conflict && <NameDialog title="新建文件夹" label="文件夹名称" initialName="" onClose={() => setModal(null)} onSubmit={async name => { await request('mkdir', { share: view.share, path: view.path, name }); setModal(null); setRefresh(value => value + 1); notify('文件夹已创建'); }} />}
    {modal?.kind === 'rename' && !conflict && <NameDialog title="重命名" label="新名称" initialName={modal.entry.name} onClose={() => setModal(null)} onSubmit={async name => { const entry = modal.entry; setModal(null); try { if (await withConflict(policy => request('rename', { share: view.share, path: entry.path, name, conflict: policy }), `重命名为「${name}」时发生冲突`)) { setRefresh(value => value + 1); notify('重命名已处理'); } } catch (error) { notify(error instanceof Error ? error.message : '重命名失败', true); } }} />}
    {modal?.kind === 'destination' && !conflict && <DestinationDialog shares={shares} initialShare={view.share} initialPath={view.path} count={modal.operation.paths.length} kind={modal.operation.kind as 'copy' | 'move'} onClose={() => setModal(null)} onConfirm={async (destinationShare, destinationPath) => { const operation = modal.operation; setModal(null); try { await startOperation({ ...operation, destinationShare, destination: destinationPath }); } catch (error) { notify(error instanceof Error ? error.message : '操作失败', true); } }} />}
    {modal?.kind === 'delete' && !conflict && <Modal title={modal.permanent ? '永久删除文件' : '移至回收站'} onClose={() => { if (!actionBusy) setModal(null); }}><div className="modal-body"><div className={`delete-illustration ${modal.permanent ? 'permanent' : ''}`}><Icon name="trash" size={30} /></div><h3 className="confirm-title">{modal.permanent ? `永久删除 ${modal.items.length} 个项目？` : `将 ${modal.items.length} 个项目移至回收站？`}</h3><p className="confirm-description">{modal.permanent ? '此操作不可撤销，文件与文件夹中的所有内容将永久删除。' : '文件将从当前位置移除，您可以稍后在回收站恢复。'}</p><ul className="delete-items">{modal.items.slice(0, 5).map(item => <li key={item.trashId ?? item.path}><Icon name={fileIcon(item)} size={16} /><span>{item.name}</span></li>)}{modal.items.length > 5 && <li>以及其他 {modal.items.length - 5} 个项目</li>}</ul></div><footer className="modal-actions"><button className="button" disabled={actionBusy} onClick={() => setModal(null)}>取消</button><button className={`button ${modal.permanent ? 'danger' : 'primary'}`} disabled={actionBusy} onClick={async () => { setActionBusy(true); try { if (modal.permanent) { for (const item of modal.items) await request('trash/delete', { share: view.share, id: item.trashId }); setRefresh(value => value + 1); notify('所选项目已永久删除'); } else await runOperation({ kind: 'trash', share: view.share, paths: modal.items.map(item => item.path), conflict: 'error' }); setModal(null); } catch (error) { notify(error instanceof Error ? error.message : '删除失败', true); setRefresh(value => value + 1); setModal(null); } finally { setActionBusy(false); } }}>{actionBusy ? '处理中…' : modal.permanent ? '永久删除' : '移至回收站'}</button></footer></Modal>}
    {modal?.kind === 'preview' && !conflict && <Preview share={view.share} entry={modal.entry} onClose={() => setModal(null)} onEdit={!readOnly && !view.trash && !modal.entry.isDir && !modal.entry.isSymlink ? () => setModal({ kind: 'edit', share: view.share, entry: modal.entry }) : undefined} />}
    {modal?.kind === 'help' && !conflict && <Modal title="键盘快捷键" onClose={() => setModal(null)}><div className="modal-body"><p className="muted">在文件区域中使用。输入文字或打开对话框时不会触发。列标题获得焦点时，Alt + 左右键用于调整列顺序，不会后退或前进。</p><dl className="shortcuts">{[['选择全部', 'Ctrl / ⌘ + A'], ['筛选当前目录', 'Ctrl / ⌘ + F'], ['复制 / 剪切', 'Ctrl / ⌘ + C / X'], ['粘贴到当前目录', 'Ctrl / ⌘ + V'], ['重命名', 'F2'], ['移至回收站 / 永久删除', 'Delete'], ['打开所选项目', 'Enter'], ['后退', 'Alt + ←'], ['前进', 'Alt + →'], ['上一级目录', 'Alt + ↑'], ['清除选择 / 关闭任务中心', 'Esc'], ['连续选择', 'Shift + 点击'], ['多选', 'Ctrl / ⌘ + 点击']].map(([label, key]) => <div key={label}><dt>{label}</dt><dd><kbd>{key}</kbd></dd></div>)}</dl></div><footer className="modal-actions"><button className="button primary" onClick={() => setModal(null)}>知道了</button></footer></Modal>}
    {conflict && <Modal title={conflict.title} onClose={() => conflict.resolve(null)}><div className="modal-body"><div className="conflict-heading"><Icon name="warning" size={25} /><p>{conflict.description}</p></div><p className="muted">文件不会自动覆盖，请选择处理方式：</p><div className="conflict-options"><button onClick={() => conflict.resolve('skip')}><Icon name="chevron" /><span><strong>跳过同名项目</strong><small>保留目标中的文件，继续处理其他项目</small></span></button><button onClick={() => conflict.resolve('keep')}><Icon name="copy" /><span><strong>保留两者</strong><small>为新文件自动添加序号，保留原有文件</small></span></button><button className="replace-option" onClick={() => conflict.resolve('replace')}><Icon name="warning" /><span><strong>替换目标项目</strong><small>覆盖目标中的同名内容，此操作不可撤销</small></span></button></div></div><footer className="modal-actions"><button className="button" onClick={() => conflict.resolve(null)}>取消此次操作</button></footer></Modal>}
  </div>;
}
