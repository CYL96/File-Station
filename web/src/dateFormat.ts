import type { DateFormat, GeneralSettings } from './api';

export const defaultGeneralSettings: GeneralSettings = { dateFormat: 'locale' };

export function formatDate(value: string, dateFormat: DateFormat) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  if (dateFormat === 'locale') return date.toLocaleString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false });
  const year = String(date.getFullYear()).padStart(4, '0');
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  const time = `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`;
  switch (dateFormat) {
    case 'yyyy-MM-dd': return `${year}-${month}-${day} ${time}`;
    case 'yyyy/MM/dd': return `${year}/${month}/${day} ${time}`;
    case 'dd/MM/yyyy': return `${day}/${month}/${year} ${time}`;
    case 'MM/dd/yyyy': return `${month}/${day}/${year} ${time}`;
  }
}
