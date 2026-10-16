import { useEffect, useRef, useState } from 'react';
import { ApiError, request } from './api';
import type { AboutData, UpdateCheck } from './api';
import { useI18n } from './i18n';
import { Icon, Modal } from './ui';

const repositoryUrl = 'https://github.com/CYL96/File-Station';
const releasesUrl = `${repositoryUrl}/releases`;

export function About({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const [about, setAbout] = useState<AboutData | null>(null);
  const [loading, setLoading] = useState(true);
  const [aboutError, setAboutError] = useState('');
  const [reload, setReload] = useState(0);
  const [checking, setChecking] = useState(false);
  const [update, setUpdate] = useState<UpdateCheck | null>(null);
  const [updateError, setUpdateError] = useState('');
  const aboutController = useRef<AbortController | null>(null);
  const updateController = useRef<AbortController | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    aboutController.current = controller;
    setLoading(true); setAboutError('');
    request<AboutData>('settings/about', undefined, undefined, controller.signal)
      .then(data => { if (!controller.signal.aborted) setAbout(data); })
      .catch(cause => { if (!controller.signal.aborted) setAboutError(cause instanceof ApiError ? cause.message : '读取版本信息失败，请检查网络后重试。'); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [reload]);
  useEffect(() => () => { updateController.current?.abort(); }, []);

  function close() {
    aboutController.current?.abort();
    updateController.current?.abort();
    onClose();
  }
  async function checkUpdate() {
    if (updateController.current) return;
    const controller = new AbortController();
    updateController.current = controller;
    setChecking(true); setUpdate(null); setUpdateError('');
    try {
      const result = await request<UpdateCheck>('settings/update', undefined, undefined, controller.signal);
      if (!controller.signal.aborted) setUpdate(result);
    } catch (cause) {
      if (!controller.signal.aborted) setUpdateError(cause instanceof ApiError ? cause.message : '检查更新失败，请检查网络后重试。');
    } finally {
      if (!controller.signal.aborted) {
        updateController.current = null;
        setChecking(false);
      }
    }
  }

  const updateMessages: Record<UpdateCheck['status'], string> = {
    'update-available': t('发现新版本 {version}，当前版本为 {current}。请前往 GitHub 查看发布说明并手动更新。', { version: update?.latestVersion ?? '', current: update?.currentVersion ?? '' }),
    'up-to-date': t('当前版本 {current} 无需更新（最新发布版本为 {latest}）。', { current: update?.currentVersion ?? '', latest: update?.latestVersion ?? '' }),
    unreleased: t('项目暂未发布 GitHub Release，无法确认是否有更新。'),
    unknown: t('无法比较当前版本 {current} 与最新发布版本 {latest}，请查看 GitHub 发布说明。', { current: update?.currentVersion ?? '', latest: update?.latestVersion ?? '' }),
  };
  const releaseUrl = update?.releaseUrl === releasesUrl || update?.releaseUrl.startsWith(`${releasesUrl}/tag/`) ? update.releaseUrl : releasesUrl;

  return <Modal title={t('关于 File Station')} onClose={close}>
    <div className="modal-body about-body">
      <div className="about-intro"><Icon name="server" size={32} /><div><h3>File Station</h3><p>{t('轻量的单管理员 Web 文件管理器')}</p></div></div>
      <p className="about-description">{t('在浏览器中管理共享目录，支持文件上传下载、文本编辑、回收站和后台任务。可在普通 Linux、Docker 或 NixOS 上部署，使用 Go、React 与 SQLite 构建。')}</p>
      <dl className="about-details">
        <div><dt>{t('当前版本')}</dt><dd>{loading ? t('正在读取…') : about?.version || update?.currentVersion || t('无法读取')}</dd></div>
        <div><dt>{t('项目地址')}</dt><dd><a href={repositoryUrl} target="_blank" rel="noreferrer">github.com/CYL96/File-Station<Icon name="chevron" size={13} /></a></dd></div>
        <div><dt>{t('开源许可')}</dt><dd>MIT License</dd></div>
      </dl>
      {loading && <p className="muted" role="status">{t('正在读取软件版本…')}</p>}
      {aboutError && <div className="about-feedback"><p className="form-error" role="alert">{t(aboutError)}</p><button type="button" className="button" onClick={() => setReload(value => value + 1)}><Icon name="refresh" size={16} />{t('重新读取版本')}</button></div>}
      <section className="about-updates" aria-label={t('软件更新')} aria-busy={checking}>
        <h3>{t('软件更新')}</h3>
        <p className="muted">{t('仅在点击时连接 GitHub 检查最新 Release，不会自动下载或安装。')}</p>
        <button type="button" className="button primary" disabled={checking} onClick={() => { void checkUpdate(); }}>{checking ? <><span className="spinner" />{t('正在检查…')}</> : <><Icon name="refresh" size={16} />{t(updateError ? '重试检查更新' : '检查更新')}</>}</button>
        <div className="about-update-status" role="status" aria-live="polite" aria-atomic="true">{checking ? <p>{t('正在检查 GitHub 发布版本…')}</p> : update && <><p>{updateMessages[update.status]}</p>{update.latestVersion && update.status !== 'unreleased' && <a className="button" href={releaseUrl} target="_blank" rel="noreferrer">{t('查看 GitHub Release')}<Icon name="chevron" size={14} /></a>}</>}</div>
        {updateError && <p className="form-error" role="alert">{t(updateError)}</p>}
      </section>
    </div>
  </Modal>;
}
