import { Injectable, signal } from '@angular/core';
import { Buyer, Guests, Selection } from './models';

const KEY = 'sb-checkout-v2';
const TOKENS = 'sb-order-access';

interface Draft { selection: Selection; guests: Guests; buyer: Buyer }

function read<T>(storage: () => Storage, key: string): T | null {
  try { const raw = storage().getItem(key); return raw ? (JSON.parse(raw) as T) : null; } catch { return null; }
}
function write(storage: () => Storage, key: string, value: unknown): void {
  try { storage().setItem(key, JSON.stringify(value)); } catch { /* хранилище недоступно — работаем в памяти */ }
}

/**
 * Предварительный выбор посетителя (не удержание!) и секреты доступа к своим оформлениям.
 * Хранится только в этой вкладке/браузере.
 */
@Injectable({ providedIn: 'root' })
export class CheckoutStore {
  readonly draft = signal<Draft | null>(read<Draft>(() => sessionStorage, KEY));

  setSelection(selection: Selection, guests?: Partial<Guests>): void {
    const prev = this.draft();
    const baseGuests: Guests = selection.kind === 'session'
      ? { adults: selection.seats.length, children: [] }
      : { adults: 2, children: [] };
    const next: Draft = { selection, guests: { ...baseGuests, ...guests }, buyer: prev?.buyer ?? { name: '', phone: '' } };
    this.draft.set(next);
    write(() => sessionStorage, KEY, next);
  }

  update(patch: Partial<Draft>): void {
    const d = this.draft();
    if (!d) return;
    const next = { ...d, ...patch };
    this.draft.set(next);
    write(() => sessionStorage, KEY, next);
  }

  /** Секрет доступа к оформлению хранится локально: номер заказа сам по себе доступа не даёт. */
  saveAccess(orderId: string, token: string): void {
    const all = read<Record<string, string>>(() => localStorage, TOKENS) ?? {};
    all[orderId] = token;
    write(() => localStorage, TOKENS, all);
  }

  access(orderId: string): string | null {
    return (read<Record<string, string>>(() => localStorage, TOKENS) ?? {})[orderId] ?? null;
  }
}
