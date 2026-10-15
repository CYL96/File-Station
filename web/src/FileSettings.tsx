import { useRef, useState } from 'react';
import { request } from './api';
import type { FileColumnKey, FileSettings } from './api';
import { Icon, Modal } from './ui';

export const columnLabels: Record<FileColumnKey, string> = { name: '名称', size: '大小', modified: '修改时间', type: '类型', extension: '扩展名', permissions: '权限', owner: '所有者', group: '所属组', created: '创建时间' };
export const defaultFileSettings: FileSettings = { showHidden: false, columns: (Object.keys(columnLabels) as FileColumnKey[]).map((key, index) => ({ key, visible: index < 4 })), sort: { key: 'name', ascending: true } };

export function AdvancedSettings({ value, onClose, onSaved }: { value: FileSettings; onClose: () => void; onSaved: (settings: FileSettings) => void }) {
  const [draft, setDraft] = useState(value);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const writing = useRef(false);
  return <Modal title="高级设置" onClose={() => { if (!writing.current) onClose(); }}><form onSubmit={async event => {
    event.preventDefault(); if (writing.current) return;
    writing.current = true; setBusy(true); setError('');
    try { const saved = await request<FileSettings>('settings/files/save', draft); onSaved(saved); }
    catch (cause) { setError(cause instanceof Error ? cause.message : '保存设置失败'); }
    finally { writing.current = false; setBusy(false); }
  }}><div className="modal-body advanced-settings-body"><fieldset disabled={busy}>
    <h3>文件显示</h3>
    <label className="settings-checkbox"><input type="checkbox" checked={draft.showHidden} onChange={event => setDraft({ ...draft, showHidden: event.target.checked })} />显示隐藏项目</label>
    <h3>列显示</h3><p className="field-help">勾选要显示的列。名称列始终显示；列顺序在文件列表表头拖动调整。</p>
    <p className="field-help">权限以 Unix 八进制显示（含特殊权限位）。所有者与所属组无法解析名称时显示 UID/GID；创建时间不受文件系统支持时显示「—」。</p>
    <ul className="settings-columns">{draft.columns.map(column => <li key={column.key}><label className="settings-checkbox"><input type="checkbox" checked={column.visible} disabled={busy || column.key === 'name'} onChange={event => setDraft({ ...draft, columns: draft.columns.map(item => item.key === column.key ? { ...item, visible: event.target.checked } : item) })} />{columnLabels[column.key]}</label></li>)}</ul>
    <p className="field-help">保存后应用于全部共享和回收站，并在刷新、重新登录或服务重启后保留。文件排序通过列表表头调整。</p>
  </fieldset>{error && <p className="form-error" role="alert">{error}</p>}</div><footer className="modal-actions"><button type="button" className="button" disabled={busy} onClick={onClose}>取消</button><button className="button primary" disabled={busy}>{busy ? <><span className="spinner" />正在保存…</> : <><Icon name="check" size={16} />保存设置</>}</button></footer></form></Modal>;
}
