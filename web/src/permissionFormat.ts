import type { PermissionFormat } from './api';

export function formatPermissions(value: string, permissionFormat: PermissionFormat) {
  if (!value || !/^[0-7]{3,4}$/.test(value)) return '—';
  if (permissionFormat === 'octal') return value;
  const mode = parseInt(value, 8);
  let symbolic = '';
  for (let group = 0; group < 3; group++) {
    const bits = (mode >> (6 - group * 3)) & 7;
    const special = mode & (0o4000 >> group);
    symbolic += bits & 4 ? 'r' : '-';
    symbolic += bits & 2 ? 'w' : '-';
    symbolic += special ? group === 2 ? bits & 1 ? 't' : 'T' : bits & 1 ? 's' : 'S' : bits & 1 ? 'x' : '-';
  }
  return symbolic;
}
