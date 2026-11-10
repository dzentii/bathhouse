import { Component, DestroyRef, computed, effect, inject, input, signal } from '@angular/core';
import { Router, RouterLink } from '@angular/router';
import { rxResource } from '@angular/core/rxjs-interop';
import { throwError } from 'rxjs';
import { BookingApi } from '../../core/api/booking-api';
import { CheckoutStore } from '../../core/checkout.store';
import { ApiError, OrderView, TestPaymentOutcome } from '../../core/models';
import { asApiError, dateLong, guestsText, resourcesText, uuidv4 } from '../../core/format';
import { SiteHeader } from '../../shared/site-header';
import { Notice } from '../../shared/notice';
import { LoadError } from '../../shared/load-error';
import { MoneyPipe } from '../../shared/money.pipe';
import { Icon } from '../../shared/icon';

const HOLD_SECONDS = 600;

@Component({
  selector: 'app-payment',
  imports: [RouterLink, SiteHeader, Notice, LoadError, MoneyPipe, Icon],
  templateUrl: './payment.html',
  styleUrl: './payment.scss',
})
export class PaymentPage {
  private api = inject(BookingApi);
  private store = inject(CheckoutStore);
  private router = inject(Router);

  readonly orderId = input.required<string>();
  token: string | null = null;

  readonly now = signal(Date.now());
  readonly paying = signal(false);
  readonly releasing = signal(false);
  readonly confirming = signal(false);
  readonly payError = signal<ApiError | null>(null);
  readonly releaseError = signal<string | null>(null);
  readonly outcome = signal<TestPaymentOutcome>('success');
  private payKey = uuidv4();

  readonly outcomes: { value: TestPaymentOutcome; label: string }[] = [
    { value: 'success', label: 'Успех' },
    { value: 'decline', label: 'Отказ' },
    { value: 'lost', label: 'Ответ потерян' },
  ];

  readonly order = rxResource({
    params: () => this.orderId(),
    stream: ({ params }) => {
      this.token = this.store.access(params);
      return this.token ? this.api.getOrder(params, this.token) : throwError(() => ({ code: 'not_found', message: 'Оформление не найдено.' }));
    },
  });

  readonly remaining = computed(() => {
    const exp = (this.order.hasValue() ? this.order.value() : undefined)?.holdExpiresAt;
    return exp ? Math.max(0, Math.min(HOLD_SECONDS, Math.floor((Date.parse(exp) - this.now()) / 1000))) : 0;
  });
  readonly clock = computed(() => {
    const r = this.remaining();
    return `${String(Math.floor(r / 60)).padStart(2, '0')}:${String(r % 60).padStart(2, '0')}`;
  });

  constructor() {
    const id = setInterval(() => this.now.set(Date.now()), 1000);
    inject(DestroyRef).onDestroy(() => clearInterval(id));

    // Оформление уже не ждёт оплаты — показываем фактический результат.
    effect(() => {
      const o = (this.order.hasValue() ? this.order.value() : undefined);
      if (o && o.status !== 'held') this.router.navigate(['/result', o.id], { replaceUrl: true });
    });
    // Срок вышел по часам — уточняем у сервера, он источник истины.
    let checkedFor = '';
    effect(() => {
      const o = (this.order.hasValue() ? this.order.value() : undefined);
      if (o?.status === 'held' && this.remaining() === 0 && !this.paying() && checkedFor !== o.holdExpiresAt) {
        checkedFor = o.holdExpiresAt ?? '';
        setTimeout(() => this.order.reload(), 1500);
      }
    });
  }

  heldTitle(o: OrderView): string {
    if (o.selection.kind === 'sauna') return 'Сауна удержана за вами';
    const s = o.selection.seats;
    return s.length === 1 ? `Место ${s[0]} удержано за вами` : `Места ${s.slice(0, -1).join(', ')} и ${s[s.length - 1]} удержаны за вами`;
  }

  needsConfirm(o: OrderView): boolean { return o.previousTotal != null && !o.newTotalConfirmed; }

  confirmTotal(): void {
    const o = (this.order.hasValue() ? this.order.value() : undefined);
    if (!o || !this.token) return;
    this.confirming.set(true);
    this.api.confirmNewTotal(o.id, this.token).subscribe({
      next: (v) => { this.confirming.set(false); this.order.set(v); },
      error: (e: unknown) => { this.confirming.set(false); this.payError.set(asApiError(e)); },
    });
  }

  setOutcome(v: TestPaymentOutcome): void {
    if (v === this.outcome()) return;
    // Другой исход — другое тело запроса, значит и другой ключ повтора.
    this.outcome.set(v);
    this.payKey = uuidv4();
  }

  pay(o: OrderView): void {
    if (!this.token || this.paying()) return;
    this.paying.set(true);
    this.payError.set(null);
    this.api.payTest(o.id, this.token, this.outcome(), this.payKey).subscribe({
      next: () => this.router.navigate(['/result', o.id]),
      error: (e: unknown) => {
        const err = asApiError(e);
        this.paying.set(false);
        if (err.code === 'network' || err.code === 'unknown') {
          // Ответ не получен: это не успех и не отказ — уточняем сохранённый итог.
          this.router.navigate(['/result', o.id], { state: { pending: true } });
        } else if (err.code === 'expired') {
          this.router.navigate(['/result', o.id]);
        } else if (err.code === 'declined') {
          // Отказ не снимает удержание: можно повторить до конца срока, но это уже новая попытка — новый ключ.
          this.payKey = uuidv4();
          this.payError.set({ ...err, message: `${err.message} Удержание сохраняется до конца срока — можно попробовать ещё раз.` });
        } else {
          this.payError.set(err);
        }
      },
    });
  }

  changeSelection(o: OrderView): void {
    if (!this.token) return;
    this.releasing.set(true);
    this.releaseError.set(null);
    this.api.releaseHold(o.id, this.token, uuidv4()).subscribe({
      next: () => {
        const s = o.selection;
        this.router.navigate(['/banya', s.banyaId, s.departmentId, s.kind === 'session' ? 'seans' : 'sauna']);
      },
      error: (e: unknown) => {
        this.releasing.set(false);
        this.releaseError.set(asApiError(e).message);
      },
    });
  }

  long(d: string): string { return dateLong(d); }
  res(o: OrderView): string { return resourcesText(o.selection); }
  guests(o: OrderView): string { return guestsText(o.guests); }
  msg(e: unknown): string { return asApiError(e).message; }
}
