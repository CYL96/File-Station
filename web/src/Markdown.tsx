import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import type { Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { apiURL } from './api';
import { useI18n } from './i18n';
import { CodeView } from './CodeView';

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

const MarkdownCodeBlock: Components['pre'] = ({ node, children }) => {
  const code = node?.children[0];
  if (code?.type !== 'element' || code.tagName !== 'code') return <pre>{children}</pre>;
  const classes = Array.isArray(code.properties.className) ? code.properties.className.map(String) : [];
  const language = classes.find(value => value.startsWith('language-'))?.slice(9);
  const content = code.children.map(child => child.type === 'text' ? child.value : '').join('').replace(/\n$/, '');
  return <CodeView value={content} languageName={language} className="markdown-code" />;
};

export function Markdown({ text, share, path }: { text: string; share: string; path: string }) {
  const { t } = useI18n();
  return <div className="markdown-content"><ReactMarkdown remarkPlugins={[remarkGfm]} urlTransform={(url, key) => key === 'src' ? markdownImageURL(url, share, path) : defaultUrlTransform(url)} components={{
    pre: MarkdownCodeBlock,
    a: ({ href, children }) => href && /^(https?:|mailto:|#)/i.test(href) ? <a href={href} target={href.startsWith('#') ? undefined : '_blank'} rel="noopener noreferrer">{children}</a> : <span>{children}</span>,
    img: ({ src, alt }) => src ? <img src={src} alt={alt ?? ''} loading="lazy" /> : <span className="markdown-image-note">{t('[图片：{alt}]', { alt: alt || t('外部或不支持的图片') })}</span>
  }}>{text}</ReactMarkdown></div>;
}
