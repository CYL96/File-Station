import { useLayoutEffect, useRef, useState } from 'react';
import { Compartment, EditorSelection, EditorState, Transaction } from '@codemirror/state';
import { EditorView, keymap } from '@codemirror/view';
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands';
import { openSearchPanel, search, searchKeymap } from '@codemirror/search';
import { LanguageDescription } from '@codemirror/language';
import { languages } from '@codemirror/language-data';
import { githubLight } from '@uiw/codemirror-theme-github';
import { useI18n } from './i18n';

type CodeViewProps = {
  value: string;
  name?: string;
  languageName?: string;
  editable?: boolean;
  disabled?: boolean;
  label?: string;
  contentId?: string;
  className?: string;
  onChange?: (value: string) => void;
  onScroll?: (node: HTMLElement) => void;
  onScroller?: (node: HTMLElement | null) => void;
};

function languageFor(name?: string, languageName?: string) {
  if (languageName) return LanguageDescription.matchLanguageName(languages, languageName, false);
  return name ? LanguageDescription.matchFilename(languages, name) ?? LanguageDescription.matchFilename(languages, name.toLowerCase()) : null;
}

const searchPhrases = {
  Find: '查找',
  next: '下一个',
  previous: '上一个',
  all: '全部',
  'match case': '区分大小写',
  regexp: '正则表达式',
  'by word': '全字匹配',
  close: '关闭',
  Replace: '替换为',
  replace: '替换',
  'replace all': '全部替换',
  'current match': '当前匹配',
  'on line': '所在行',
  'replaced match on line $': '已替换第 $ 行的匹配',
  'replaced $ matches': '已替换 $ 处匹配',
};

// Keep the view and its incremental parser/history alive across controlled value updates.
// Only explicit file names or fenced-code labels select a language; content is never guessed.
export function CodeView(props: CodeViewProps) {
  const { language: uiLanguage, t } = useI18n();
  const { value, name, languageName, editable = false, disabled = false, label, contentId, className = '' } = props;
  const description = languageFor(name, languageName);
  const [failed, setFailed] = useState<LanguageDescription | null>(null);
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const latest = useRef(props);
  latest.current = props;
  const language = useRef(new Compartment());
  const access = useRef(new Compartment());
  const currentValue = useRef(value);
  const attributes = useRef(new Compartment());
  const phrases = useRef(new Compartment());

  useLayoutEffect(() => {
    currentValue.current = latest.current.value;
    const editor = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: latest.current.value,
        extensions: [
          githubLight,
          EditorState.tabSize.of(4),
          language.current.of([]),
          access.current.of([]),
          attributes.current.of([]),
          phrases.current.of([]),
          search({ top: true }),
          keymap.of(searchKeymap.filter(binding => binding.key === 'Mod-f' || binding.key === 'F3' || binding.key === 'Mod-g')),
          EditorView.updateListener.of(update => {
            if (!update.docChanged) return;
            currentValue.current = update.state.doc.toString();
            if (update.transactions.some(transaction => transaction.docChanged && !transaction.annotation(Transaction.remote))) {
              latest.current.onChange?.(currentValue.current);
            }
          }),
          EditorView.domEventHandlers({
            scroll: (event, current) => {
              if (event.target === current.scrollDOM) latest.current.onScroll?.(current.scrollDOM);
            },
          }),
        ],
      }),
    });
    view.current = editor;
    latest.current.onScroller?.(editor.scrollDOM);
    return () => {
      latest.current.onScroller?.(null);
      view.current = null;
      editor.destroy();
    };
  }, []);

  useLayoutEffect(() => {
    view.current!.dispatch({ effects: phrases.current.reconfigure(EditorState.phrases.of(uiLanguage === 'zh-CN' ? searchPhrases : {})) });
  }, [uiLanguage]);

  useLayoutEffect(() => {
    const editor = view.current!;
    editor.dispatch({ effects: access.current.reconfigure([
      EditorState.readOnly.of(!editable || disabled),
      EditorView.editable.of(editable && !disabled),
      ...(editable ? [history(), keymap.of([
        {
          key: 'Tab',
          run: current => {
            if (current.state.readOnly) return false;
            current.dispatch(current.state.changeByRange(range => ({
              changes: { from: range.from, to: range.to, insert: '\t' },
              range: EditorSelection.cursor(range.from + 1),
            })), { scrollIntoView: true, userEvent: 'input' });
            return true;
          },
        },
        ...defaultKeymap.filter(binding => binding.key !== 'Escape' && binding.key !== 'Tab' && binding.key !== 'Shift-Tab'),
        ...historyKeymap,
      ])] : []),
    ]) });
  }, [editable, disabled]);

  useLayoutEffect(() => {
    view.current!.dispatch({ effects: attributes.current.reconfigure(EditorView.contentAttributes.of({
      'aria-label': label ?? t('文件内容'),
      'aria-readonly': String(!editable || disabled),
      'aria-disabled': String(disabled),
      ...(contentId ? { id: contentId } : {}),
      spellcheck: 'false',
      autocapitalize: 'off',
      autocorrect: 'off',
      // Read-only previews remain selectable without becoming an extra keyboard stop.
      ...(!editable ? { tabindex: '-1' } : {}),
    })) });
  }, [label, contentId, editable, disabled, t]);

  useLayoutEffect(() => {
    const editor = view.current!;
    if (currentValue.current === value) return;
    editor.dispatch({
      changes: { from: 0, to: editor.state.doc.length, insert: value },
      annotations: [Transaction.remote.of(true), Transaction.addToHistory.of(false)],
    });
  }, [value]);

  useLayoutEffect(() => {
    const editor = view.current!;
    let cancelled = false;
    setFailed(null);
    editor.dispatch({ effects: language.current.reconfigure([]) });
    if (description) void description.load().then(support => {
      if (!cancelled) editor.dispatch({ effects: language.current.reconfigure(support) });
    }).catch(() => { if (!cancelled) setFailed(description); });
    return () => { cancelled = true; };
  }, [description]);

  const unavailable = description !== null && failed === description;
  const languageLabel = description && !unavailable ? description.name : t('纯文本');
  return <div className={`code-view ${editable ? 'code-view-editable' : 'code-view-readonly'} ${className}`} data-language={description && !unavailable ? description.name : 'plain-text'}>
    <div className="code-view-language"><span>{languageLabel}</span>{unavailable && <span role="status">{t('语法高亮加载失败，已显示纯文本。')}</span>}<button type="button" className="code-view-find" onClick={() => { if (view.current) openSearchPanel(view.current); }}>{t('查找')}</button></div>
    <div ref={host} className="code-view-host" />
  </div>;
}
