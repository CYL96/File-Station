import { useEffect, useRef, useState } from 'react';
import { ApiError, request } from './api';
import type { DateFormat, GeneralSettings, ManagedShare, PermissionFormat, SettingsDirectories, ShareSettingsChange, ShareSettingsData } from './api';
import { Icon, Modal } from './ui';
import { formatDate } from './dateFormat';
import { formatPermissions } from './permissionFormat';
import { useI18n } from './i18n';

type ShareDraft = { id?: string; name: string; path: string; readOnly: boolean; originalPath?: string };
function errorMessage(cause: unknown, fallback: string) {
  const message = cause instanceof Error ? cause.message : fallback;
  if (cause instanceof ApiError && cause.status === 403) return `${message} 请确认目录已挂载到服务所在环境，并为运行服务的 UID/GID 授予目录读取和进入权限；读写共享还需要写入权限。应用私有状态和密码路径不能共享。`;
  return cause instanceof ApiError && cause.status === 409 ? `${message} 请等待相关任务结束；如回收站仍有项目，请先恢复或清空后重试。` : message;
}

export function Settings({ generalSettings, generalLoading, generalError, onSaveGeneral, onClose, onChanged }: { generalSettings: GeneralSettings; generalLoading: boolean; generalError: string; onSaveGeneral: (settings: GeneralSettings, signal: AbortSignal) => Promise<GeneralSettings | null>; onClose: () => void; onChanged: (change: ShareSettingsChange) => Promise<void> }) {
  const { t, locale } = useI18n();
  const [category, setCategory] = useState<'general' | 'shares' | null>(null);
  const [generalDraft, setGeneralDraft] = useState<GeneralSettings | null>(null);
  const [generalSaveError, setGeneralSaveError] = useState('');
  const [generalSuccess, setGeneralSuccess] = useState('');
  const saveController = useRef<AbortController | null>(null);
  const [data, setData] = useState<ShareSettingsData | null>(null);
  const [loading, setLoading] = useState(true);
  const [draft, setDraft] = useState<ShareDraft | null>(null);
  const [removing, setRemoving] = useState<ManagedShare | null>(null);
  const [busy, setBusy] = useState(false);
  const writing = useRef(false);
  const [error, setError] = useState<string | { refreshFailures: string[]; saved: boolean }>('');
  const [success, setSuccess] = useState('');
  const [pickerOpen, setPickerOpen] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true); setError('');
    request<ShareSettingsData>('settings/shares', undefined, undefined, controller.signal)
      .then(result => { if (!controller.signal.aborted) setData(result); })
      .catch(cause => { if (!controller.signal.aborted) setError(errorMessage(cause, '读取共享设置失败')); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => { controller.abort(); saveController.current?.abort(); };
  }, []);

  function edit(share?: ManagedShare) {
    setDraft(share ? { id: share.id, name: share.name, path: share.path, readOnly: share.readOnly, originalPath: share.path } : { name: '', path: '', readOnly: false });
    setRemoving(null); setPickerOpen(false); setError(''); setSuccess('');
  }
  async function mutate(endpoint: string, body: unknown, change: ShareSettingsChange, message: string) {
    if (writing.current) return;
    writing.current = true; setBusy(true); setError(''); setSuccess('');
    try {
      await request<unknown>(endpoint, body);
      if (endpoint !== 'settings/shares/order') { setDraft(null); setRemoving(null); setPickerOpen(false); }
      setSuccess(message);
      const results = await Promise.allSettled([
        request<ShareSettingsData>('settings/shares').then(setData),
        onChanged(change),
      ]);
      const failures = results.filter((result): result is PromiseRejectedResult => result.status === 'rejected');
      if (failures.length) setError({ refreshFailures: failures.map(result => errorMessage(result.reason, '无法读取最新状态')), saved: true });
    } catch (cause) {
      setError(errorMessage(cause, '保存共享设置失败'));
    } finally { writing.current = false; setBusy(false); }
  }
  function moveShare(index: number, direction: -1 | 1) {
    if (!data || writing.current) return;
    const target = index + direction;
    if (target < 0 || target >= data.shares.length) return;
    const ids = data.shares.map(share => share.id);
    [ids[index], ids[target]] = [ids[target], ids[index]];
    void mutate('settings/shares/order', { ids }, {}, '共享顺序已保存。');
  }
  async function saveGeneral() {
    if (writing.current || generalLoading) return;
    writing.current = true; setBusy(true); setGeneralSaveError(''); setGeneralSuccess('');
    const controller = new AbortController();
    saveController.current = controller;
    try {
      const saved = await onSaveGeneral(generalDraft ?? generalSettings, controller.signal);
      if (!controller.signal.aborted && saved) {
        setGeneralDraft(saved); setGeneralSuccess('通用设置已保存，日期和权限显示已更新。');
      }
    } catch (cause) {
      if (!controller.signal.aborted) setGeneralSaveError(cause instanceof Error ? cause.message : '保存通用设置失败');
    } finally {
      writing.current = false;
      if (!controller.signal.aborted) setBusy(false);
    }
  }
  const { dateFormat, permissionFormat } = generalDraft ?? generalSettings;
  const path = draft ? draft.path.replace(/\/+$/, '') || '/' : '';
  const invalid = !draft?.name.trim() || !draft.path.startsWith('/');
  return <><Modal title={t('设置')} wide onClose={() => { if (!writing.current) onClose(); }}>
    <div className="modal-body settings-body">
      {category === null ? <div className="settings-home">
        <p className="settings-home-description">{t('选择设置分类，管理文件工作空间。')}</p>
        <div className="settings-categories">
          <button type="button" className="settings-category-card" onClick={() => setCategory('general')} autoFocus>
            <span className="settings-category-icon"><Icon name="settings" size={24} /></span>
            <span className="settings-category-copy"><strong>{t('通用设置')}</strong><span>{t('设置全站日期与权限显示格式')}</span></span>
            <Icon name="chevron" size={17} />
          </button>
          <button type="button" className="settings-category-card" onClick={() => setCategory('shares')}>
            <span className="settings-category-icon"><Icon name="folder" size={24} /></span>
            <span className="settings-category-copy"><strong>{t('共享设置')}</strong><span>{t('管理共享目录、访问权限')}</span></span>
            <Icon name="chevron" size={17} />
          </button>
        </div>
      </div> : <>
        <div className="settings-category-heading">
          <button type="button" className="button settings-back" disabled={busy} onClick={() => { if (!writing.current) { setPickerOpen(false); setCategory(null); } }} autoFocus><Icon name="arrow" size={16} />{t('返回设置')}</button>
          <h3>{t(category === 'general' ? '通用设置' : '共享设置')}</h3>
        </div>
        {category === 'general' ? <form className="general-settings" aria-busy={busy || generalLoading} onSubmit={event => { event.preventDefault(); void saveGeneral(); }}>
          {generalLoading ? <div className="loading-state" role="status"><span className="spinner" />{t('正在读取通用设置…')}</div> : <>
            {generalError && <p className="form-error" role="alert">{t('未能读取已保存的通用设置：{detail}。当前以默认格式显示；保存将覆盖服务器中的通用设置。', { detail: t(generalError) })}</p>}
            {generalSaveError && <p className="form-error" role="alert">{t(generalSaveError)}</p>}
            {generalSuccess && <p className="share-settings-success" role="status"><Icon name="check" size={17} />{t(generalSuccess)}</p>}
            <fieldset disabled={busy}>
              <label className="field-label" htmlFor="general-date-format">{t('日期显示格式')}</label>
              <select id="general-date-format" className="text-input" value={dateFormat} onChange={event => { const value = event.target.value as DateFormat; setGeneralDraft(current => ({ ...(current ?? generalSettings), dateFormat: value })); setGeneralSaveError(''); setGeneralSuccess(''); }} aria-describedby="general-date-help general-date-preview">
                <option value="locale">{t('默认格式（跟随界面语言）')}</option>
                <option value="yyyy-MM-dd">{t('年-月-日（yyyy-MM-dd HH:mm:ss）')}</option>
                <option value="yyyy/MM/dd">{t('年/月/日（yyyy/MM/dd HH:mm:ss）')}</option>
                <option value="dd/MM/yyyy">{t('日/月/年（dd/MM/yyyy HH:mm:ss）')}</option>
                <option value="MM/dd/yyyy">{t('月/日/年（MM/dd/yyyy HH:mm:ss）')}</option>
              </select>
              <p id="general-date-help" className="field-help">{t('使用本地时区和 24 小时制，保留小时、分钟与秒。保存后应用到文件修改、创建、回收站删除时间和预览中的日期。')}</p>
              <p id="general-date-preview" className="general-format-preview" aria-live="polite">{t('显示示例：')}<time>{formatDate(new Date(2026, 10, 23, 14, 5, 9).toISOString(), dateFormat)}</time></p>
            </fieldset>
            <fieldset disabled={busy}>
              <label className="field-label" htmlFor="general-permission-format">{t('权限显示格式')}</label>
              <select id="general-permission-format" className="text-input" value={permissionFormat} onChange={event => { const value = event.target.value as PermissionFormat; setGeneralDraft(current => ({ ...(current ?? generalSettings), permissionFormat: value })); setGeneralSaveError(''); setGeneralSuccess(''); }} aria-describedby="general-permission-help general-permission-preview">
                <option value="octal">{t('八进制（0755）')}</option>
                <option value="symbolic">{t('符号格式（rwxr-xr-x）')}</option>
              </select>
              <p id="general-permission-help" className="field-help">{t('保存后应用到文件列表和回收站的权限列。仅改变显示格式，不修改实际权限或排序。')}</p>
              <p id="general-permission-preview" className="general-format-preview" aria-live="polite">{t('显示示例：')}<code>{formatPermissions('0755', permissionFormat)}</code></p>
            </fieldset>
            <div className="share-editor-actions"><span className="field-help">{t('选择后需保存才会生效。')}</span><button type="submit" className="button primary" disabled={busy}>{t(busy ? '正在保存…' : '保存通用设置')}</button></div>
          </>}
        </form> : <>
        <div className="share-settings-body" aria-busy={busy || loading}>
      {error && <div className="share-settings-feedback"><p className="form-error" role="alert">{typeof error === 'string' ? t(error) : error.saved ? t('更改已保存，但刷新失败：{detail}。请重新加载。', { detail: error.refreshFailures.map(detail => t(detail)).join(locale === 'zh-CN' ? '；' : '; ') }) : error.refreshFailures.map(detail => t(detail)).join(locale === 'zh-CN' ? '；' : '; ')}</p>{!busy && <button className="button" onClick={async () => {
        if (writing.current) return;
        writing.current = true; setBusy(true);
        try {
          const results = await Promise.allSettled([
            request<ShareSettingsData>('settings/shares').then(setData),
            onChanged({}),
          ]);
          const failures = results.filter((result): result is PromiseRejectedResult => result.status === 'rejected');
          setError(failures.length ? { refreshFailures: failures.map(result => errorMessage(result.reason, '刷新共享目录失败')), saved: false } : '');
        } finally { writing.current = false; setBusy(false); }
      }}><Icon name="refresh" size={16} />{t('重新加载设置')}</button>}</div>}
      {success && <p className="share-settings-success" role="status"><Icon name="check" size={17} />{t(success)}</p>}
      {loading ? <div className="loading-state" role="status"><span className="spinner" />{t('正在读取共享设置…')}</div> : data && <>
        <div className="share-settings-layout">
          <section className="managed-shares" aria-label={t('已有共享目录')}><div className="share-section-heading"><h3>{t('共享目录')} <span>{data.shares.length.toLocaleString(locale)}</span></h3><button className="button" disabled={busy} onClick={() => edit()}><Icon name="plus" size={16} />{t('添加共享')}</button></div>
            {data.shares.length === 0 ? <p className="share-settings-empty">{t('首次使用不预设任何共享目录。点击「添加共享」，手动选择服务账号可访问的文件夹；Docker 挂载目录（如 /mnt 下的目录）也需要在此添加。')}</p> : <ul className="managed-share-list">{data.shares.map((item, index) => <li key={item.id} className={draft?.id === item.id || removing?.id === item.id ? 'active' : ''}>
              <div className="managed-share-details">
                <h4><span title={item.name}>{item.name}</span>{item.readOnly && <span className="readonly-badge">{t('只读')}</span>}</h4>
                <code title={item.path}>{item.path}</code>
                {!item.available && <p className="form-error"><Icon name="warning" size={14} /><span>{t('不可用：{detail}', { detail: t(item.error || '目录当前无法访问') })}</span></p>}
              </div>
              <div className="managed-share-actions">
                <button type="button" className="icon-button" disabled={busy || index === 0} onClick={() => moveShare(index, -1)} aria-label={t('上移共享 {name}', { name: item.name })} title={t('上移共享 {name}', { name: item.name })}><Icon name="arrow" size={16} className="share-move-up" /></button>
                <button type="button" className="icon-button" disabled={busy || index === data.shares.length - 1} onClick={() => moveShare(index, 1)} aria-label={t('下移共享 {name}', { name: item.name })} title={t('下移共享 {name}', { name: item.name })}><Icon name="arrow" size={16} className="share-move-down" /></button>
                <button type="button" className="icon-button" disabled={busy} onClick={() => edit(item)} aria-label={t('编辑共享 {name}', { name: item.name })} title={t('编辑共享 {name}', { name: item.name })}><Icon name="edit" size={16} /></button>
                <button type="button" className="icon-button danger-text" disabled={busy} onClick={() => { setRemoving(item); setDraft(null); setPickerOpen(false); setError(''); setSuccess(''); }} aria-label={t('移除共享 {name}', { name: item.name })} title={t('移除共享 {name}', { name: item.name })}><Icon name="trash" size={16} /></button>
              </div>
            </li>)}</ul>}
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
        </>}
      </>}
    </div>
  </Modal>
    {category === 'shares' && pickerOpen && draft && <SettingsDirectoryPicker initialPath={path} onClose={() => setPickerOpen(false)} onChoose={value => { setDraft({ ...draft, path: value }); setPickerOpen(false); }} />}
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
