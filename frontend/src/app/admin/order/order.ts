import { Component, inject, input } from '@angular/core';
import { RouterLink } from '@angular/router';
import { rxResource } from '@angular/core/rxjs-interop';
import { BookingApi } from '../../core/api/booking-api';
import { AdminAuth } from '../../core/admin-auth.service';
import { OrderStatus, OrderView } from '../../core/models';
import { STATUS_LABEL, childrenText, dateLong, timeEkb } from '../../core/format';
import { MoneyPipe } from '../../shared/money.pipe';
import { LoadError } from '../../shared/load-error';
import { adminErrorHandler } from '../admin-common';

@Component({
  selector: 'app-admin-order',
  imports: [RouterLink, MoneyPipe, LoadError],
  templateUrl: './order.html',
  styleUrl: './order.scss',
})
export class AdminOrderPage {
  private api = inject(BookingApi);
  private auth = inject(AdminAuth);
  readonly err = adminErrorHandler();
  readonly orderId = input.required<string>();
  readonly order = rxResource({ params: () => this.orderId(), stream: ({ params }) => this.api.adminOrder(this.auth.token, params) });

  kids(o: OrderView): string { return childrenText(o.guests.children); }
  label(s: OrderStatus): string { return STATUS_LABEL[s]; }
  long(d: string): string { return dateLong(d); }
  time(iso: string): string { return timeEkb(iso); }
  payText(s: OrderStatus): string {
    return ({ draft: 'Не выполнялась — выбор не удержан', confirmed: 'Успешно', declined: 'Отказ', held: 'Ожидает оплаты', expired: 'Не выполнена — удержание истекло', released: 'Не выполнена — выбор изменён', pending: 'Результат уточняется' } as Record<OrderStatus, string>)[s];
  }
}
