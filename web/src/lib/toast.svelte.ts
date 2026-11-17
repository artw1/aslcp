export interface Toast { id: number; kind: 'ok' | 'error' | 'info'; text: string }
let seq = 0;
export const toasts = $state<{ list: Toast[] }>({ list: [] });
export function toast(text: string, kind: Toast['kind'] = 'ok', ms = 4500) {
  const id = ++seq;
  toasts.list.push({ id, kind, text });
  setTimeout(() => dismiss(id), kind === 'error' ? ms + 2500 : ms);
}
export function dismiss(id: number) { toasts.list = toasts.list.filter((t) => t.id !== id); }
export const toastError = (e: unknown) => toast(e instanceof Error ? e.message : String(e), 'error');
