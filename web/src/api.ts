export type Share = { id: string; name: string; readOnly: boolean };
export type ManagedShare = Share & { path: string; available: boolean; error?: string };
export type ShareSettingsData = { shares: ManagedShare[] };
export type ShareSettingsChange = { id?: string; pathChanged?: boolean; removed?: boolean };
export type SettingsDirectory = { name: string; path: string };
export type SettingsDirectories = { path: string; entries: SettingsDirectory[] };
export type FileColumnKey = 'name' | 'size' | 'modified' | 'type' | 'extension' | 'permissions' | 'owner' | 'group' | 'created';
export type FileSort = { key: FileColumnKey; ascending: boolean };
export type FileSettings = { showHidden: boolean; columns: { key: FileColumnKey; visible: boolean }[]; sort: FileSort };
export type DateFormat = 'locale' | 'yyyy-MM-dd' | 'yyyy/MM/dd' | 'dd/MM/yyyy' | 'MM/dd/yyyy';
export type PermissionFormat = 'octal' | 'symbolic';
export type GeneralSettings = { dateFormat: DateFormat; permissionFormat: PermissionFormat };
export type FileMetadata = { extension: string; permissions: string; owner: string; group: string; created: string | null };
export type Entry = FileMetadata & { name: string; path: string; isDir: boolean; size: number; modified: string; isSymlink: boolean };
export type TrashEntry = FileMetadata & { id: string; path: string; deletedAt: string; size: number; isDir: boolean; isSymlink: boolean };
export type Task = { id: string; kind: string; status: 'running' | 'done' | 'error' | 'cancelled'; completed: number; total: number; canRetry: boolean; error?: string };
export type Session = { username: string; csrf: string };
export type Conflict = 'error' | 'skip' | 'replace' | 'keep';
export type Operation = { kind: 'copy' | 'move' | 'trash'; share: string; paths: string[]; destinationShare?: string; destination?: string; conflict: Conflict };

export class ApiError extends Error {
  constructor(message: string, public status: number) { super(message); }
}

let csrf = '';
let unauthorized: () => void = () => {};
export function configureSession(session: Session | null, onUnauthorized: () => void) {
  csrf = session?.csrf ?? '';
  unauthorized = onUnauthorized;
}
export function apiURL(endpoint: string, params: Record<string, string> = {}) {
  const query = new URLSearchParams(params).toString();
  return `/api/${endpoint}${query ? `?${query}` : ''}`;
}
async function failure(response: Response): Promise<never> {
  const payload = await response.json().catch(() => null) as { error?: string } | null;
  if (response.status === 401) unauthorized();
  throw new ApiError(payload?.error ?? `请求失败（${response.status}）`, response.status);
}
export async function request<T>(endpoint: string, body?: unknown, params?: Record<string, string>, signal?: AbortSignal): Promise<T> {
  const response = await fetch(apiURL(endpoint, params), {
    method: body === undefined ? 'GET' : 'POST',
    credentials: 'same-origin',
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });
  if (!response.ok) return failure(response);
  return response.json() as Promise<T>;
}
export async function preview(share: string, path: string, signal: AbortSignal, method: 'GET' | 'HEAD' = 'GET'): Promise<Response> {
  const response = await fetch(apiURL('preview', { share, path }), { method, signal, credentials: 'same-origin' });
  if (!response.ok) return failure(response);
  return response;
}
export function upload(file: File, share: string, path: string, conflict: Conflict, progress: (loaded: number) => void, signal: AbortSignal): Promise<{ skipped?: boolean }> {
  const { promise, resolve, reject } = Promise.withResolvers<{ skipped?: boolean }>();
  const xhr = new XMLHttpRequest();
  const abort = () => xhr.abort();
  const cleanup = () => signal.removeEventListener('abort', abort);
  xhr.open('POST', apiURL('upload', { share, path, name: file.name, conflict }));
  xhr.setRequestHeader('X-CSRF-Token', csrf);
  xhr.setRequestHeader('Content-Type', 'application/octet-stream');
  xhr.upload.onprogress = event => progress(event.loaded);
  xhr.onerror = () => { cleanup(); reject(new ApiError('网络连接中断，请重试上传', 0)); };
  xhr.onabort = () => { cleanup(); reject(new DOMException('上传已取消', 'AbortError')); };
  xhr.onload = () => {
    cleanup();
    let data: { error?: string; skipped?: boolean } = {};
    try { data = JSON.parse(xhr.responseText); } catch { /* Status below still determines failure. */ }
    if (xhr.status === 401) unauthorized();
    if (xhr.status >= 200 && xhr.status < 300) resolve(data);
    else reject(new ApiError(data.error ?? `上传失败（${xhr.status}）`, xhr.status));
  };
  signal.addEventListener('abort', abort, { once: true });
  if (signal.aborted) {
    cleanup();
    reject(new DOMException('上传已取消', 'AbortError'));
  } else {
    xhr.send(file);
  }
  return promise;
}
