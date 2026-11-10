import { Component, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';
import { rxResource } from '@angular/core/rxjs-interop';
import { BookingApi } from '../../core/api/booking-api';
import { AdminAuth } from '../../core/admin-auth.service';
import { OrderStatus, OrderView } from '../../core/models';
import { STATUS_LABEL, addDays, dateLong, guestsText, resourcesText, todayEkb } from '../../core/format';
import { MoneyPipe } from '../../shared/money.pipe';
import { Notice } from '../../shared/notice';
import { LoadError } from '../../shared/load-error';
import { adminErrorHandler, catalogResource } from '../admin-common';

@Component({
  selector: 'app-admin-orders',
  imports: [FormsModule, RouterLink, MoneyPipe, Notice, LoadError],
  templateUrl: './orders.html',
  styleUrl: './orders.scss',
})
export class AdminOrdersPage {
  private api = inject(BookingApi);
  private auth = inject(AdminAuth);
  readonly err = adminErrorHandler();
  readonly catalog = catalogResource();

  readonly banyaId = signal('');
  readonly departmentId = signal('');
  readonly from = signal(todayEkb());
  readonly to = signal(addDays(todayEkb(), 6));
  readonly status = signal<OrderStatus | 'all'>('all');

  readonly departments = computed(() => {
    const all = (this.catalog.hasValue() ? this.catalog.value() : undefined) ?? [];
    return all.filter((c) => !this.banyaId() || c.banya.id === this.banyaId()).flatMap((c) => c.departments.filter((d) => d.mode !== 'info'));
  });

  readonly orders = rxResource({
    params: () => ({ banyaId: this.banyaId() || undefined, departmentId: this.departmentId() || undefined, from: this.from() || undefined, to: this.to() || undefined }),
    stream: ({ params }) => this.api.adminOrders(this.auth.token, params),
  });

  readonly tabs = computed(() => {
    const list = (this.orders.hasValue() ? this.orders.value() : undefined) ?? [];
    const keys: (OrderStatus | 'all')[] = ['all', 'confirmed', 'held', 'pending', 'declined', 'expired', 'released', 'draft'];
    return keys.map((k) => ({ key: k, label: k === 'all' ? 'Все' : STATUS_LABEL[k], count: k === 'all' ? list.length : list.filter((o) => o.status === k).length }))
      .filter((t) => t.key === 'all' || t.count > 0 || t.key === this.status());
  });

  readonly rows = computed(() => ((this.orders.hasValue() ? this.orders.value() : undefined) ?? []).filter((o) => this.status() === 'all' || o.status === this.status()));

  label(s: OrderStatus): string { return STATUS_LABEL[s]; }
  long(d: string): string { return dateLong(d); }
  res(o: OrderView): string { return resourcesText(o.selection); }
  guests(o: OrderView): string { return guestsText(o.guests); }
}
