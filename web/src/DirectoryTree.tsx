import { useEffect, useState } from 'react';
import { request } from './api';
import type { Entry, Share } from './api';
import { Icon } from './ui';

export function DirectoryTree({ shares, currentShare, currentPath, trash, refresh, showHidden, navigate }: {
  shares: Share[]; currentShare: string; currentPath: string; trash: boolean; refresh: number; showHidden: boolean;
  navigate: (share: string, path?: string) => void;
}) {
  return <nav aria-label="共享目录">{shares.length ? shares.map(share => <DirectoryNode key={share.id} share={share} path="" name={share.name} depth={0} currentShare={currentShare} currentPath={currentPath} trash={trash} refresh={refresh} showHidden={showHidden} navigate={navigate} />) : <p className="tree-note">尚无共享目录，请在「共享设置」中手动添加。</p>}</nav>;
}

function DirectoryNode(props: { share: Share; path: string; name: string; depth: number; currentShare: string; currentPath: string; trash: boolean; refresh: number; showHidden: boolean; navigate: (share: string, path?: string) => void }) {
  const { share, path, name, depth, currentShare, currentPath, trash, refresh, showHidden, navigate } = props;
  const active = share.id === currentShare && !trash;
  const ancestor = active && (path === '' || currentPath === path || currentPath.startsWith(path + '/'));
  const [expanded, setExpanded] = useState(false);
  const [children, setChildren] = useState<Entry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => { if (ancestor && currentPath !== path) setExpanded(true); }, [ancestor, currentPath, path]);
  useEffect(() => {
    if (!expanded) return;
    const controller = new AbortController(); setLoading(true); setError('');
    request<{ entries: Entry[] }>('files', undefined, { share: share.id, path }, controller.signal)
      .then(result => { if (!controller.signal.aborted) setChildren(result.entries.filter(entry => entry.isDir && !entry.isSymlink)); })
      .catch(reason => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : '读取失败'); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [expanded, share.id, path, refresh]);
  const visibleChildren = children.filter(child => showHidden || !child.name.startsWith('.'));
  return <div className="directory-node">
    <div className={`directory-node-row ${active && currentPath === path ? 'selected' : ''}`} style={{ paddingLeft: 10 + depth * 14 }}>
      <button className="tree-toggle" aria-label={`${expanded ? '收起' : '展开'}${name}目录`} aria-expanded={expanded} onClick={() => setExpanded(value => !value)}><span className={expanded ? 'tree-chevron expanded' : 'tree-chevron'}><Icon name="chevron" size={13} /></span></button>
      <button className="share-button" onClick={() => navigate(share.id, path)}><Icon name="folder" size={17} /><span>{name}</span>{depth === 0 && share.readOnly && <Icon name="lock" size={13} />}</button>
    </div>
    {expanded && <div className="directory-children">{loading ? <p className="tree-note" role="status">读取中…</p> : error ? <p className="tree-note" role="alert">{error}</p> : visibleChildren.length ? visibleChildren.map(child => <DirectoryNode key={child.path} {...props} path={child.path} name={child.name} depth={depth + 1} />) : <p className="tree-note">无子文件夹</p>}</div>}
  </div>;
}
