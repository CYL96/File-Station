import { useEffect, useState } from 'react';
import { request } from './api';
import { Icon, Modal, formatBytes } from './ui';

type Totals = { size: number; files: number; folders: number; skipped: number };
type SizeResult = Totals & { items: (Totals & { path: string })[] };
export type SizeSelection = { share: string; trash: boolean; items: { path: string; name: string }[] };
export function FileSizeDialog({ selection, onClose }: { selection: SizeSelection; onClose: () => void }) {
  const [result, setResult] = useState<SizeResult | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    const controller = new AbortController();
    request<SizeResult>('files/size', { share: selection.share, trash: selection.trash, paths: selection.items.map(item => item.path) }, undefined, controller.signal)
      .then(data => { if (!controller.signal.aborted) setResult(data); })
      .catch(cause => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : '计算大小失败'); });
    return () => controller.abort();
  }, [selection]);
  return <Modal title="计算大小" onClose={onClose}><div className="modal-body file-size-body">
    <p className="field-help">递归统计普通文件的内容大小（不是磁盘占用），包含隐藏文件，不跟随符号链接；服务内部文件和特殊文件不计入。统计期间文件变化可能影响结果。</p>
    {!result && !error && <p className="size-calculating" role="status"><span className="spinner" />正在计算 {selection.items.length} 个项目的大小…</p>}
    {error && <p className="form-error" role="alert">{error}；未生成完整统计，请检查文件权限或重新计算。</p>}
    {result && <><div className="size-summary" role="status"><span>总大小</span><strong>{formatBytes(result.size)}</strong><small>{result.size.toLocaleString()} 字节 · {result.files} 个文件 · {result.folders} 个文件夹</small></div>
      <ul className="size-results">{result.items.map(item => <li key={item.path}><span title={selection.items.find(selected => selected.path === item.path)?.name}>{selection.items.find(selected => selected.path === item.path)?.name ?? item.path}</span><strong title={`${item.size.toLocaleString()} 字节`}>{formatBytes(item.size)}</strong></li>)}</ul>
      {result.skipped > 0 && <p className="field-help">已跳过 {result.skipped} 个符号链接或特殊文件。</p>}</>}
  </div><footer className="modal-actions"><button className="button primary" onClick={onClose}><Icon name="close" size={16} />{!result && !error ? '取消计算' : '关闭'}</button></footer></Modal>;
}
