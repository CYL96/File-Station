import { useEffect, useRef, useState } from 'react';
import DOMPurify from 'dompurify';
import Editor from '@toast-ui/editor';
import type { CustomHTMLRenderer } from '@toast-ui/editor';
import '@toast-ui/editor/dist/i18n/zh-cn';
import '@toast-ui/editor/dist/toastui-editor.css';
import { markdownImageURL } from './Markdown';
import { useI18n } from './i18n';

export function MarkdownVisualEditor({ value, share, path, disabled, onChange }: { value: string; share: string; path: string; disabled: boolean; onChange: (value: string) => void }) {
  const { locale, t } = useI18n();
  const host = useRef<HTMLDivElement>(null);
  const initial = useRef(value);
  const changed = useRef(onChange);
  changed.current = onChange;
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  function safeImage(value: string) {
    const source = value.replace(/&amp;/g, '&');
    const generated = source.startsWith('/api/download?') ? new URL(source, location.origin) : null;
    return generated?.searchParams.get('share') === share && generated.searchParams.size === 2
      ? markdownImageURL(generated.searchParams.get('path') ?? '', share, '')
      : markdownImageURL(source, share, path);
  }
  const renderers: CustomHTMLRenderer = {
    link: (_node, context) => {
      const token = context.origin?.();
      if (token && !Array.isArray(token) && token.type === 'openTag' && token.attributes) {
        const href = String(token.attributes.href ?? '');
        token.attributes.href = /^(https?:|mailto:|#)/i.test(href) ? href : '';
      }
      return token ?? null;
    },
    image: (_node, context) => {
      const token = context.origin?.();
      if (token && !Array.isArray(token) && token.type === 'openTag' && token.attributes) {
        token.attributes.src = safeImage(String(token.attributes.src ?? ''));
        token.attributes.alt = String(token.attributes.alt ?? t('图片'));
      }
      return token ?? null;
    },
  };
  useEffect(() => {
    let destroy: (() => void) | undefined;
    try {
      if (!host.current) return;
      const editor = new Editor({
        el: host.current,
        height: '100%', minHeight: '240px', initialValue: initial.current,
        initialEditType: 'wysiwyg', hideModeSwitch: true, autofocus: false,
        language: locale, usageStatistics: false,
        toolbarItems: [['heading', 'bold', 'italic', 'strike'], ['hr', 'quote'], ['ul', 'ol', 'task'], ['table', 'link'], ['code', 'codeblock']],
        linkAttributes: { target: '_blank', rel: 'noopener noreferrer' },
        customHTMLRenderer: renderers,
        customHTMLSanitizer: html => {
          const clean = DOMPurify.sanitize(html, { USE_PROFILES: { html: true }, FORBID_TAGS: ['style', 'iframe', 'form', 'input', 'button', 'audio', 'video'], FORBID_ATTR: ['style', 'srcset'], RETURN_DOM_FRAGMENT: true });
          for (const image of clean.querySelectorAll('img')) image.setAttribute('src', safeImage(image.getAttribute('src') ?? ''));
          const container = document.createElement('div');
          container.append(clean);
          return container.innerHTML;
        },
        hooks: { addImageBlobHook: () => false },
      });
      // Merely entering visual mode must not rewrite the original Markdown.
      // Publish only changes from the visual editor's normalized baseline.
      let previous = editor.getMarkdown();
      editor.on('change', () => {
        const next = editor.getMarkdown();
        if (next !== previous) { previous = next; changed.current(next); }
      });
      destroy = () => editor.destroy();
      setLoading(false);
    } catch (cause) { setError(cause instanceof Error ? cause.message : '无法加载预览编辑器'); setLoading(false); }
    return () => { destroy?.(); };
  }, []);
  return <div className="markdown-visual" inert={disabled}>
    {loading && <div className="markdown-visual-loading" role="status">{t('正在加载预览编辑器…')}</div>}
    {error && <p className="form-error" role="alert">{t('{detail}；可切换到源代码继续编辑。', { detail: t(error) })}</p>}
    <div ref={host} className="markdown-visual-host" />
  </div>;
}
