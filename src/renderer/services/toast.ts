import { ref } from 'vue';

export type ToastKind = 'success' | 'error' | 'info';

export interface Toast {
  id: number;
  message: string;
  kind: ToastKind;
}

const toasts = ref<Toast[]>([]);
let nextId = 1;

export function showToast(message: string, kind: ToastKind = 'info', duration = 3000): void {
  const id = nextId++;
  toasts.value = [...toasts.value, { id, message, kind }];
  if (duration > 0) {
    setTimeout(() => dismissToast(id), duration);
  }
}

export function dismissToast(id: number): void {
  toasts.value = toasts.value.filter((t) => t.id !== id);
}

export function useToasts() {
  return { toasts, showToast, dismissToast };
}
