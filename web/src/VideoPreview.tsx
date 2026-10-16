import { useEffect, useRef, useState } from 'react';
import { ApiError, preview } from './api';

export function VideoPreview({ share, path, url, name }: { share: string; path: string; url: string; name: string }) {
  const ref = useRef<HTMLVideoElement>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    const video = ref.current!;
    const controller = new AbortController();
    setLoading(true);
    setError('');
    const loaded = () => {
      if (!controller.signal.aborted) setLoading(false);
    };
    const failed = () => {
      if (controller.signal.aborted) return;
      setLoading(false);
      const message = video.error?.code === MediaError.MEDIA_ERR_NETWORK
        ? '视频网络连接中断，无法继续加载。请检查网络连接，或下载文件查看。'
        : '浏览器不支持此视频的编码，或文件已损坏，无法播放。请下载文件查看。';
      setError(message);
      void preview(share, path, controller.signal, 'HEAD').catch(cause => {
        if (controller.signal.aborted) return;
        setError(cause instanceof ApiError
          ? `视频请求失败（HTTP ${cause.status}）：${cause.message}`
          : '无法连接服务器以检查视频，请检查网络连接，或下载文件查看。');
      });
    };
    video.addEventListener('loadedmetadata', loaded);
    video.addEventListener('error', failed, { once: true });
    video.src = url;
    return () => {
      controller.abort();
      video.removeEventListener('loadedmetadata', loaded);
      video.removeEventListener('error', failed);
      video.pause();
      video.removeAttribute('src');
      video.load();
    };
  }, [share, path, url]);

  return <div className="video-preview">
    {loading && <div className="loading-state video-preview-status" role="status"><span className="spinner" />正在加载视频信息…</div>}
    {error && <p className="video-preview-error" role="alert">{error}</p>}
    <div className="video-preview-stage"><video ref={ref} className="video-preview-player" controls playsInline preload="metadata" aria-label={name}>浏览器不支持视频播放，请下载文件查看。</video></div>
  </div>;
}
