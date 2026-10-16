import { createContext, useContext, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { appMessages } from './appMessages';
import { settingsMessages } from './settingsMessages';
import { previewMessages } from './previewMessages';
import { apiMessages } from './apiMessages';

export type Language = 'zh-CN' | 'en';
type Params = Record<string, string | number>;
const storageKey = 'file-station.language';

function initialLanguage(): Language {
  try {
    const saved = localStorage.getItem(storageKey);
    if (saved === 'zh-CN' || saved === 'en') return saved;
  } catch { /* Language selection still works when browser storage is unavailable. */ }
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en';
}

let activeLanguage = initialLanguage();
const messages: Record<string, string> = { ...appMessages, ...settingsMessages, ...previewMessages, ...apiMessages };
const privacyMessages: Record<string, string> = {
  'stateDir must be outside shares': '共享目录不能包含应用私有状态目录',
  'passwordFile must be outside shares': '共享目录不能包含密码文件',
  'passwordSourceFile must be outside shares': '共享目录不能包含密码源文件',
};
const parameterPattern = /\{(\w+)\}/g;
const templates = Object.entries(messages).flatMap(([source, english]) => {
  const parameters = [...source.matchAll(parameterPattern)];
  if (!parameters.length) return [];
  let cursor = 0;
  let pattern = '^';
  for (const parameter of parameters) {
    pattern += source.slice(cursor, parameter.index).replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '([\\s\\S]*?)';
    cursor = parameter.index! + parameter[0].length;
  }
  pattern += source.slice(cursor).replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$';
  return [{ english, keys: parameters.map(parameter => parameter[1]), pattern: new RegExp(pattern), specificity: source.length - parameters.reduce((length, parameter) => length + parameter[0].length, 0) }];
}).sort((a, b) => b.specificity - a.specificity);

function interpolate(message: string, params: Params): string {
  return message.replace(parameterPattern, (placeholder, key: string) => Object.hasOwn(params, key) ? String(params[key]) : placeholder);
}

function translate(source: string, depth = 0): string {
  if (Object.hasOwn(messages, source)) return messages[source];
  if (depth >= 8) return source;
  for (const template of templates) {
    const match = template.pattern.exec(source);
    if (!match) continue;
    const params: Params = {};
    template.keys.forEach((key, index) => {
      const value = match[index + 1];
      params[key] = /^(detail|error|reason|message)$/.test(key) ? translate(value, depth + 1) : value;
    });
    return interpolate(template.english, params);
  }
  return source;
}

export function t(source: string, params?: Params): string {
  for (const [diagnostic, canonical] of Object.entries(privacyMessages)) {
    if (source === diagnostic || source.startsWith(`${diagnostic} 请`)) {
      source = canonical + source.slice(diagnostic.length);
      break;
    }
  }
  const message = activeLanguage === 'zh-CN' ? source : params ? (messages[source] ?? source) : translate(source);
  return params ? interpolate(message, params) : message;
}

export function getLocale(): 'zh-CN' | 'en-US' {
  return activeLanguage === 'zh-CN' ? 'zh-CN' : 'en-US';
}

type I18n = { language: Language; locale: 'zh-CN' | 'en-US'; setLanguage: (language: Language) => void; t: typeof t };
const LanguageContext = createContext<I18n | null>(null);

export function LanguageProvider({ children }: { children: ReactNode }) {
  const [language, updateLanguage] = useState(activeLanguage);
  function setLanguage(next: Language) {
    activeLanguage = next;
    updateLanguage(next);
    document.documentElement.lang = next;
    try { localStorage.setItem(storageKey, next); } catch { /* Storage is optional, rendering is not. */ }
  }
  useEffect(() => {
    document.documentElement.lang = language;
  }, [language]);
  return <LanguageContext.Provider value={{ language, locale: language === 'zh-CN' ? 'zh-CN' : 'en-US', setLanguage, t }}>{children}</LanguageContext.Provider>;
}

export function useI18n(): I18n {
  const context = useContext(LanguageContext);
  if (!context) throw new Error('useI18n requires LanguageProvider');
  return context;
}

export function LanguageSwitcher() {
  const { language, setLanguage, t: translateLabel } = useI18n();
  return <label className="language-switcher"><span className="sr-only">{translateLabel('语言')}</span><select aria-label={translateLabel('语言')} value={language} onChange={event => setLanguage(event.target.value as Language)}><option value="zh-CN" lang="zh-CN">简体中文</option><option value="en" lang="en">English</option></select></label>;
}
