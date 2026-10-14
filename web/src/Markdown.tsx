import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { apiURL } from './api';

export function isMarkdown(name: string) {
  return /\.(md|markdown)$/i.test(name);
}

export function markdownImageURL(value: string, share: string, path: string) {
    if (/^(?:[a-z][a-z\d+.-]*:|\/|\\)/i.test(value)) return '';
    const parts = path.split('/').slice(0, -1);
    let decoded: string;
    try { decoded = decodeURIComponent(value); } catch { return ''; }
    for (const part of decoded.split('/')) {
      if (part === '..') { if (!parts.length) return ''; parts.pop(); }
      else if (part && part !== '.') {
        if (part.startsWith('.webui-') || /[\\\x00?#]/.test(part)) return '';
        parts.push(part);
      }
    }
    const target = parts.join('/');
    return /\.(png|jpe?g|gif|webp)$/i.test(target) ? apiURL('download', { share, path: target }) : '';
}

export function Markdown({ text, share, path }: { text: string; share: string; path: string }) {
  return <div className="markdown-content"><ReactMarkdown remarkPlugins={[remarkGfm]} urlTransform={(url, key) => key === 'src' ? markdownImageURL(url, share, path) : defaultUrlTransform(url)} components={{
    a: ({ href, children }) => href && /^(https?:|mailto:|#)/i.test(href) ? <a href={href} target={href.startsWith('#') ? undefined : '_blank'} rel="noopener noreferrer">{children}</a> : <span>{children}</span>,
    img: ({ src, alt }) => src ? <img src={src} alt={alt ?? ''} loading="lazy" /> : <span className="markdown-image-note">[图片：{alt || '外部或不支持的图片'}]</span>,
  }}>{text}</ReactMarkdown></div>;
}
