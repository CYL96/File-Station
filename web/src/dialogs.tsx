import { useEffect, useState } from 'react';
import { ApiError, request } from './api';
import type { Entry, Session, Share } from './api';
import { Icon, Modal } from './ui';

export function Login({ onLogin, initialError }: { onLogin: (session: Session) => void; initialError: string }) {
  const [password, setPassword] = useState('');
  const [error, setError] = useState(initialError);
  const [busy, setBusy] = useState(false);
  return <main className="login-screen"><div className="login-decoration" aria-hidden="true"><div className="orbit orbit-one" /><div className="orbit orbit-two" /></div><section className="login-card"><div className="brand-mark"><Icon name="folder" size={36} /></div><h1>NixOS File Station</h1><form onSubmit={async event => {
    event.preventDefault(); setBusy(true); setError('');
    try { await request('login', { password }); const session = await request<Session>('session'); onLogin(session); }
    catch (cause) { setError(cause instanceof ApiError ? cause.message : '无法连接服务器，请检查网络后重试'); }
    finally { setBusy(false); }
  }}><label className="field-label" htmlFor="password">管理员密码</label><div className="password-field"><Icon name="lock" /><input id="password" type="password" autoComplete="current-password" required autoFocus value={password} onChange={event => setPassword(event.target.value)} placeholder="输入管理员密码" /></div>{error && <p className="form-error" role="alert">{error}</p>}<button className="button primary login-submit" disabled={busy || !password}>{busy ? <><span className="spinner" />正在登录…</> : <>安全登录<Icon name="chevron" size={18} /></>}</button></form></section></main>;
}

export function NameDialog({ title, initialName, label, onClose, onSubmit }: { title: string; initialName: string; label: string; onClose: () => void; onSubmit: (name: string) => Promise<void> }) {
  const [name, setName] = useState(initialName);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const invalid = !name.trim() || name === '.' || name === '..' || /[/\\\0]/.test(name);
  return <Modal title={title} onClose={() => { if (!busy) onClose(); }}><form onSubmit={async event => {
    event.preventDefault(); if (invalid || busy) return;
    setBusy(true); setError('');
    try { await onSubmit(name); } catch (cause) { setError(cause instanceof Error ? cause.message : '操作失败'); } finally { setBusy(false); }
  }}><div className="modal-body"><label className="field-label" htmlFor="entry-name">{label}</label><input className="text-input" id="entry-name" value={name} onChange={event => setName(event.target.value)} autoFocus onFocus={event => { const dot = initialName.lastIndexOf('.'); event.target.setSelectionRange(0, dot > 0 ? dot : initialName.length); }} maxLength={255} required /><p className="field-help">名称不能包含斜杠，也不能为「.」或「..」。</p>{error && <p className="form-error" role="alert">{error}</p>}</div><footer className="modal-actions"><button type="button" className="button" onClick={onClose} disabled={busy}>取消</button><button className="button primary" disabled={invalid || busy}>{busy ? '处理中…' : '确定'}</button></footer></form></Modal>;
}

export function DestinationDialog({ shares, initialShare, initialPath, count, kind, onClose, onConfirm }: { shares: Share[]; initialShare: string; initialPath: string; count: number; kind: 'copy' | 'move'; onClose: () => void; onConfirm: (share: string, path: string) => Promise<void> }) {
  const available = shares.filter(share => !share.readOnly);
  const [share, setShare] = useState(available.some(item => item.id === initialShare) ? initialShare : available[0]?.id ?? '');
  const [path, setPath] = useState(share === initialShare ? initialPath : '');
  const [directories, setDirectories] = useState<Entry[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true); setError(''); setDirectories([]);
    if (!share) { setLoading(false); return; }
    request<{ entries: Entry[] }>('files', undefined, { share, path }, controller.signal).then(data => { setDirectories(data.entries.filter(entry => entry.isDir && !entry.isSymlink).sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'))); }).catch(cause => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : '加载目录失败'); }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [share, path]);
  const pieces = path.split('/').filter(Boolean);
  return <Modal title={`${kind === 'copy' ? '复制' : '移动'}到…`} onClose={() => { if (!busy) onClose(); }}><div className="modal-body destination-body"><p className="muted">为选中的 {count} 个项目选择目标文件夹。</p><label className="field-label" htmlFor="destination-share">目标共享目录</label><select className="text-input" id="destination-share" value={share} onChange={event => { setShare(event.target.value); setPath(''); }}>{available.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select>{available.length === 0 && <p className="form-error">没有可写的共享目录。</p>}<nav className="destination-breadcrumb" aria-label="目标路径"><button onClick={() => setPath('')}><Icon name="folder" size={16} />根目录</button>{pieces.map((part, index) => <span key={index}><Icon name="chevron" size={12} /><button onClick={() => setPath(pieces.slice(0, index + 1).join('/'))}>{part}</button></span>)}</nav><div className="directory-picker" aria-label="目标文件夹列表">{loading ? <div className="loading-state" role="status"><span className="spinner" />正在加载…</div> : error ? <p className="form-error" role="alert">{error}</p> : <>{path && <button className="directory-row" onClick={() => setPath(pieces.slice(0, -1).join('/'))}><Icon name="arrow" />上一级目录</button>}{directories.map(directory => <button key={directory.path} className="directory-row" onClick={() => setPath(directory.path)}><Icon name="folder" className="folder-icon" /><span>{directory.name}</span><Icon name="chevron" size={16} /></button>)}{directories.length === 0 && <p className="picker-empty">此文件夹中没有子目录，可直接选择当前目录。</p>}</>}</div><div className="destination-summary"><Icon name="folder" size={16} /><span>{shares.find(item => item.id === share)?.name ?? '未选择'} / {path || '根目录'}</span></div></div><footer className="modal-actions"><button className="button" onClick={onClose} disabled={busy}>取消</button><button className="button primary" disabled={!share || loading || !!error || busy} onClick={async () => { setBusy(true); try { await onConfirm(share, path); } catch (cause) { setError(cause instanceof Error ? cause.message : '操作失败'); } finally { setBusy(false); } }}>{busy ? '处理中…' : kind === 'copy' ? '复制到这里' : '移动到这里'}</button></footer></Modal>;
}
