import { Component, DestroyRef, computed, inject, input, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { rxResource } from '@angular/core/rxjs-interop';
import { throwError } from 'rxjs';
import { BookingApi } from '../../core/api/booking-api';
import { CheckoutStore } from '../../core/checkout.store';
import { OrderView } from '../../core/models';
import { asApiError } from '../../core/format';
import { SiteHeader } from '../../shared/site-header';
import { Notice } from '../../shared/notice';
import { MoneyPipe } from '../../shared/money.pipe';
import { Icon } from '../../shared/icon';

type View = 'success' | 'declined' | 'pending' | 'expired' | 'released';

@Component({
  selector: 'app-result',
  imports: [RouterLink, SiteHeader, Notice, MoneyPipe, Icon],
  templateUrl: './result.html',
  styleUrl: './result.scss',
})
export class ResultPage {
  private api = inject(BookingApi);
  private store = inject(CheckoutStore);
  readonly orderId = input.required<string>();
  token: string | null = null;
  readonly copied = signal(false);
  private initialPending = !!(history.state as { pending?: boolean } | null)?.pending;
  private autoChecks = 0;

  readonly order = rxResource({
    params: () => this.orderId(),
    stream: ({ params }) => {
      this.token = this.store.access(params);
      return this.token ? this.api.getOrder(params, this.token) : throwError(() => ({ code: 'not_found', message: 'Оформление не найдено.' }));
    },
  });

  readonly view = computed<View>(() => {
    const o = (this.order.hasValue() ? this.order.value() : undefined);
    if (!o || this.order.isLoading() || this.order.error()) return 'pending';
    if (this.initialPending && this.autoChecks === 0) return 'pending';
    switch (o.status) {
      case 'confirmed': return 'success';
      case 'declined': return 'declined';
      case 'expired': return 'expired';
      case 'released': case 'draft': return 'released';
      case 'held': return o.holdExpiresAt && Date.parse(o.holdExpiresAt) < Date.now() ? 'expired' : 'pending';
      default: return 'pending';
    }
  });

  readonly backLink = computed(() => {
    const s = (this.order.hasValue() ? this.order.value() : undefined)?.selection;
    return s ? ['/banya', s.banyaId, s.departmentId, s.kind === 'session' ? 'seans' : 'sauna'] : ['/'];
  });

  constructor() {
    if (this.initialPending) {
      // Ответ оплаты был потерян: через пару секунд запрашиваем сохранённый итог.
      const t = setTimeout(() => this.check(), 2500);
      inject(DestroyRef).onDestroy(() => clearTimeout(t));
    }
  }

  check(): void {
    this.autoChecks++;
    this.order.reload();
  }

  copy(o: OrderView): void {
    const url = `${location.origin}/ticket/${o.ticketToken}`;
    navigator.clipboard?.writeText(url).then(() => { this.copied.set(true); setTimeout(() => this.copied.set(false), 2500); }, () => undefined);
  }

  msg(e: unknown): string { return asApiError(e).message; }
}
