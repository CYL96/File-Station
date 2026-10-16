import { useEffect, useRef, useState } from 'react';
import { ApiError, request } from './api';
import type { ManagedShare, SettingsDirectories, ShareSettingsChange, ShareSettingsData } from './api';
import { Icon, Modal } from './ui';
import { useI18n } from './i18n';

type ShareDraft = { id?: string; name: string; path: string; readOnly: boolean; originalPath?: string };
function errorMessage(cause: unknown, fallback: string) {
  const message = cause instanceof Error ? cause.message : fallback;
  if (cause instanceof ApiError && cause.status === 403) return `${message} 请确认目录已挂载到服务所在环境，并为运行服务的 UID/GID 授予目录读取和进入权限；读写共享还需要写入权限。应用私有状态和密码路径不能共享。`;
  return cause instanceof ApiError && cause.status === 409 ? `${message} 请等待相关任务结束；如回收站仍有项目，请先恢复或清空后重试。` : message;
}

export function ShareSettings({ onClose, onChanged }: { onClose: () => void; onChanged: (change: ShareSettingsChange) => Promise<void> }) {
  const { t, locale } = useI18n();
  const [data, setData] = useState<ShareSettingsData | null>(null);
  const [loading, setLoading] = useState(true);
  const [reload, setReload] = useState(0);
  const [draft, setDraft] = useState<ShareDraft | null>(null);
  const [removing, setRemoving] = useState<ManagedShare | null>(null);
  const [busy, setBusy] = useState(false);
  const writing = useRef(false);
  const [error, setError] = useState<string | { refreshFailures: string[] }>('');
  const [success, setSuccess] = useState('');
  const [pickerOpen, setPickerOpen] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true); setError('');
    request<ShareSettingsData>('settings/shares', undefined, undefined, controller.signal)
      .then(result => { if (!controller.signal.aborted) setData(result); })
      .catch(cause => { if (!controller.signal.aborted) setError(errorMessage(cause, '读取共享设置失败')); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [reload]);

  function edit(share?: ManagedShare) {
    setDraft(share ? { id: share.id, name: share.name, path: share.path, readOnly: share.readOnly, originalPath: share.path } : { name: '', path: '', readOnly: false });
    setRemoving(null); setPickerOpen(false); setError(''); setSuccess('');
  }
  async function mutate(endpoint: string, body: unknown, change: ShareSettingsChange, message: string) {
    if (writing.current) return;
    writing.current = true; setBusy(true); setError(''); setSuccess('');
    try {
      await request<{ ok: boolean }>(endpoint, body);
      setDraft(null); setRemoving(null); setPickerOpen(false); setSuccess(message);
      const results = await Promise.allSettled([
        request<ShareSettingsData>('settings/shares').then(setData),
        onChanged(change),
      ]);
      const failures = results.filter((result): result is PromiseRejectedResult => result.status === 'rejected');
      if (failures.length) setError({ refreshFailures: failures.map(result => errorMessage(result.reason, '无法读取最新状态')) });
    } catch (cause) {
      setError(errorMessage(cause, '保存共享设置失败'));
    } finally { writing.current = false; setBusy(false); }
  }
  const path = draft ? draft.path.replace(/\/+$/, '') || '/' : '';
  const invalid = !draft?.name.trim() || !draft.path.startsWith('/');
  return <><Modal title={t('共享设置')} wide onClose={() => { if (!writing.current) onClose(); }}>
    <div className="modal-body share-settings-body" aria-busy={busy || loading}>
      {error && <div className="share-settings-feedback"><p className="form-error" role="alert">{typeof error === 'string' ? t(error) : t('更改已保存，但刷新失败：{detail}。请重新加载。', { detail: error.refreshFailures.map(detail => t(detail)).join(locale === 'zh-CN' ? '；' : '; ') })}</p>{!busy && <button className="button" onClick={async () => {
        writing.current = true; setBusy(true);
        try { await onChanged({}); setReload(value => value + 1); }
        catch (cause) { setError(errorMessage(cause, '刷新共享目录失败')); }
        finally { writing.current = false; setBusy(false); }
      }}><Icon name="refresh" size={16} />{t('重新加载设置')}</button>}</div>}
      {success && <p className="share-settings-success" role="status"><Icon name="check" size={17} />{t(success)}</p>}
      {loading ? <div className="loading-state" role="status"><span className="spinner" />{t('正在读取共享设置…')}</div> : data && <>
        <div className="share-settings-layout">
          <section className="managed-shares" aria-label={t('已有共享目录')}><div className="share-section-heading"><h3>{t('共享目录')} <span>{data.shares.length.toLocaleString(locale)}</span></h3><button className="button" disabled={busy} onClick={() => edit()}><Icon name="plus" size={16} />{t('添加共享')}</button></div>
            {data.shares.length === 0 ? <p className="share-settings-empty">{t('首次使用不预设任何共享目录。点击「添加共享」，手动选择服务账号可访问的文件夹；Docker 挂载目录（如 /mnt 下的目录）也需要在此添加。')}</p> : <ul className="managed-share-list">{data.shares.map(item => <li key={item.id} className={draft?.id === item.id || removing?.id === item.id ? 'active' : ''}><div className="managed-share-details"><h4><Icon name="folder" size={17} /><span>{item.name}</span>{item.readOnly && <span className="readonly-badge">{t('只读')}</span>}</h4><code>{item.path}</code>{!item.available && <p className="form-error"><Icon name="warning" size={14} />{t('不可用：{detail}', { detail: t(item.error || '目录当前无法访问') })}</p>}</div><div className="managed-share-actions"><button className="button" disabled={busy} onClick={() => edit(item)} aria-label={t('编辑共享 {name}', { name: item.name })}><Icon name="edit" size={15} />{t('编辑')}</button><button className="button danger-text" disabled={busy} onClick={() => { setRemoving(item); setDraft(null); setPickerOpen(false); setError(''); setSuccess(''); }} aria-label={t('移除共享 {name}', { name: item.name })}><Icon name="trash" size={15} />{t('移除')}</button></div></li>)}</ul>}
          </section>
          <section className="share-settings-editor" aria-label={t(removing ? '确认移除共享' : draft ? '编辑共享' : '共享编辑器')}>
            {removing ? <div className="share-remove-confirm"><h3>{t('移除「{name}」？', { name: removing.name })}</h3><p>{t('只移除共享入口，磁盘上的文件和文件夹会保留，不会被删除。')}</p><code>{removing.path}</code><p className="field-help">{t('有进行中的操作或回收站项目时，服务器会拒绝移除；请先处理后重试。')}</p><div className="share-editor-actions"><button className="button" disabled={busy} onClick={() => setRemoving(null)}>{t('取消')}</button><button className="button danger" disabled={busy} onClick={() => { void mutate('settings/shares/remove', { id: removing.id }, { id: removing.id, removed: true }, '共享已移除，磁盘文件保持不变。'); }}>{t(busy ? '正在移除…' : '确认移除共享')}</button></div></div> : draft ? <form onSubmit={event => {
              event.preventDefault(); if (invalid || busy) return;
              void mutate('settings/shares/save', { id: draft.id, name: draft.name.trim(), path, readOnly: draft.readOnly }, { id: draft.id, pathChanged: !!draft.id && path !== (draft.originalPath?.replace(/\/+$/, '') || '/') }, draft.id ? '共享设置已保存。' : '共享目录已添加。');
            }}><h3>{t(draft.id ? '编辑共享' : '添加共享')}</h3><fieldset disabled={busy}><label className="field-label" htmlFor="share-name">{t('共享名称')}</label><input id="share-name" className="text-input" value={draft.name} onChange={event => setDraft({ ...draft, name: event.target.value })} required autoFocus /><label className="field-label" htmlFor="share-path">{t('目录绝对路径')}</label><div className="share-path-input"><input id="share-path" className="text-input" value={draft.path} onChange={event => setDraft({ ...draft, path: event.target.value })} required placeholder={t('/mnt/files/文档')} spellCheck={false} /><button type="button" className="button" onClick={() => setPickerOpen(value => !value)} aria-expanded={pickerOpen}><Icon name="folder" size={16} />{t('浏览目录')}</button></div><p className="field-help">{t('可直接输入绝对路径，也可浏览后选择。目录必须已存在，实际访问权限由运行服务的 UID/GID 决定。Docker 中请使用容器内的挂载路径，例如 /mnt/files。')}</p>{draft.path && !draft.path.startsWith('/') && <p className="form-error">{t('请输入以 / 开头的绝对目录路径。')}</p>}
              <label className="share-readonly"><input type="checkbox" checked={draft.readOnly} onChange={event => setDraft({ ...draft, readOnly: event.target.checked })} />{t('只读共享（禁止上传、修改和删除文件）')}</label>
            </fieldset><div className="share-editor-actions"><button type="button" className="button" disabled={busy} onClick={() => { setDraft(null); setPickerOpen(false); }}>{t('取消')}</button><button className="button primary" disabled={busy || invalid}>{t(busy ? '正在保存…' : '保存共享')}</button></div></form> : <div className="share-settings-empty"><Icon name="folder" size={32} /><p>{t('选择已有共享进行编辑，或添加新的共享目录。')}</p></div>}
          </section>
        </div>
      </>}
    </div>
  </Modal>
    {pickerOpen && draft && <SettingsDirectoryPicker initialPath={path} onClose={() => setPickerOpen(false)} onChoose={value => { setDraft({ ...draft, path: value }); setPickerOpen(false); }} />}
  </>;
}

function SettingsDirectoryPicker({ initialPath, onClose, onChoose }: { initialPath: string; onClose: () => void; onChoose: (path: string) => void }) {
  const { t, locale } = useI18n();
  const [path, setPath] = useState(initialPath.startsWith('/') ? initialPath : '/');
  const [pathInput, setPathInput] = useState(path);
  const inputRevision = useRef(0);
  const [directory, setDirectory] = useState<SettingsDirectories | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [reload, setReload] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    const revision = inputRevision.current;
    setLoading(true); setError(''); setDirectory(null);
    request<SettingsDirectories>('settings/directories', undefined, { path }, controller.signal)
      .then(result => { if (!controller.signal.aborted) { setDirectory(result); if (inputRevision.current === revision) setPathInput(result.path); } })
      .catch(cause => { if (!controller.signal.aborted) setError(errorMessage(cause, '读取目录失败')); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [path, reload]);
  function navigate(value: string) {
    const next = value.replace(/\/+$/, '') || '/';
    inputRevision.current += 1;
    setPathInput(next); setDirectory(null); setLoading(true); setError('');
    if (next === path) setReload(value => value + 1);
    else setPath(next);
  }
  const currentPath = directory?.path ?? path;
  const pieces = currentPath.split('/').filter(Boolean);
  const invalidPath = !pathInput.startsWith('/');
  return <Modal title={t('选择共享目录')} wide onClose={onClose}>
    <div className="modal-body settings-directory-picker">
      <label className="field-label" htmlFor="settings-picker-path">{t('浏览目录绝对路径')}</label>
      <form className="share-path-input" onSubmit={event => { event.preventDefault(); if (!invalidPath) navigate(pathInput); }}><input id="settings-picker-path" className="text-input" value={pathInput} onChange={event => { inputRevision.current += 1; setPathInput(event.target.value); }} required autoFocus onFocus={event => event.target.select()} placeholder="/mnt/files" spellCheck={false} /><button type="submit" className="button" disabled={invalidPath}>{t('前往')}</button></form>
      <p className="field-help">{t('仅列出服务账号可读取和进入的子目录。若无法浏览上级目录，可直接输入已知的完整路径；Docker 请填写容器内的挂载路径（如 /mnt/files）。')}</p>
      {pathInput && invalidPath && <p className="form-error">{t('请输入以 / 开头的绝对目录路径。')}</p>}
      <div className="settings-picker-location"><button type="button" className="button" onClick={() => navigate('/')} aria-label={t('浏览根目录 /')}>{t('根目录 /')}</button><button type="button" className="icon-button" aria-label={t('返回上一级目录')} disabled={currentPath === '/' || loading} onClick={() => navigate('/' + pieces.slice(0, -1).join('/'))}><Icon name="arrow" size={16} /></button><code>{currentPath}</code><button type="button" className="icon-button" aria-label={t('刷新可选目录')} disabled={loading} onClick={() => navigate(currentPath)}><Icon name="refresh" size={16} /></button></div>
      <div className="directory-picker" aria-label={t('可选子目录')} aria-busy={loading}>{loading ? <div className="loading-state" role="status"><span className="spinner" />{t('正在读取目录…')}</div> : error ? <p className="form-error" role="alert">{t(error)}</p> : !directory?.entries.length ? <p className="picker-empty">{t('没有可进入的子目录；可以选择当前目录，也可输入其他绝对路径。')}</p> : [...directory.entries].sort((a, b) => a.name.localeCompare(b.name, locale)).map(entry => <button type="button" key={entry.path} className="directory-row" onClick={() => navigate(entry.path)}><Icon name="folder" size={18} className="folder-icon" /><span>{entry.name}</span><Icon name="chevron" size={14} /></button>)}</div>
    </div>
    <footer className="modal-actions"><button type="button" className="button" onClick={onClose}>{t('取消')}</button><button type="button" className="button primary" disabled={loading || !directory || !!error} onClick={() => { if (directory) onChoose(directory.path); }}><Icon name="check" size={16} />{t('选择当前目录')}</button></footer>
  </Modal>;
}
