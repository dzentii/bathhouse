import { Component, DestroyRef, computed, effect, inject, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { rxResource } from '@angular/core/rxjs-interop';
import { BookingApi } from '../../core/api/booking-api';
import { CustomerAuth } from '../../core/customer-auth.service';
import { OrderView } from '../../core/models';
import { asApiError, dateLong, daysBetween, nowEkbMinutes, plural, sortSeats, toMinutes, todayEkb } from '../../core/format';
import { MoneyPipe } from '../../shared/money.pipe';
import { Notice } from '../../shared/notice';
import { LoadError } from '../../shared/load-error';

/** Окончание в минутах от полуночи даты посещения (00:00 у сауны — это полночь следующих суток). */
function endMin(o: OrderView): number {
  const s = toMinutes(o.selection.start), e = toMinutes(o.selection.end);
  return e <= s ? e + 24 * 60 : e;
}

@Component({
  selector: 'app-cabinet-tickets',
  imports: [RouterLink, MoneyPipe, Notice, LoadError],
  templateUrl: './tickets.html',
  styleUrl: './tickets.scss',
})
export class CabinetTicketsPage {
  private api = inject(BookingApi);
  private auth = inject(CustomerAuth);

  readonly orders = rxResource({ stream: () => this.api.myOrders(this.auth.token) });
  readonly nowMin = signal(nowEkbMinutes());
  readonly today = signal(todayEkb());
  readonly tab = signal<'cur' | 'hist'>('cur');
  readonly banyaFilter = signal<'all' | 'banya-1' | 'banya-2'>('all');

  readonly filters = [
    { key: 'all' as const, label: 'Все' },
    { key: 'banya-1' as const, label: 'Баня №1' },
    { key: 'banya-2' as const, label: 'Баня №2' },
  ];

  constructor() {
    const t = setInterval(() => { this.nowMin.set(nowEkbMinutes()); this.today.set(todayEkb()); }, 30_000);
    inject(DestroyRef).onDestroy(() => clearInterval(t));
    effect(() => {
      const e = this.orders.error();
      if (e && asApiError(e).code === 'unauthorized') this.auth.expired('/cabinet');
    });
  }

  private readonly all = computed(() => (this.orders.hasValue() ? this.orders.value() : undefined) ?? []);

  /** Сегодняшние посещения, которые ещё не закончились. */
  readonly todayList = computed(() => this.all()
    .filter((o) => o.selection.date === this.today() && endMin(o) > this.nowMin())
    .sort((a, b) => a.selection.start.localeCompare(b.selection.start)));

  readonly upcoming = computed(() => this.all()
    .filter((o) => o.selection.date > this.today())
    .sort((a, b) => (a.selection.date + a.selection.start).localeCompare(b.selection.date + b.selection.start)));

  readonly history = computed(() => this.all()
    .filter((o) => o.selection.date < this.today() || (o.selection.date === this.today() && endMin(o) <= this.nowMin()))
    .sort((a, b) => (b.selection.date + b.selection.start).localeCompare(a.selection.date + a.selection.start)));

  readonly historyFiltered = computed(() => this.history().filter((o) => this.banyaFilter() === 'all' || o.banya.id === this.banyaFilter()));
  readonly currentCount = computed(() => this.todayList().length + this.upcoming().length);

  isActive(o: OrderView): boolean { return toMinutes(o.selection.start) <= this.nowMin(); }

  timeNote(o: OrderView): string {
    if (this.isActive(o)) return `До конца ${o.selection.kind === 'session' ? 'сеанса' : 'аренды'} ${this.duration(endMin(o) - this.nowMin())}`;
    return `Начало через ${this.duration(toMinutes(o.selection.start) - this.nowMin())}`;
  }

  private duration(min: number): string {
    const h = Math.floor(min / 60), m = min % 60;
    return h ? `${h} ч ${m} мин` : `${m} мин`;
  }

  whenLabel(o: OrderView): string {
    const n = daysBetween(this.today(), o.selection.date);
    const rel = n === 1 ? 'завтра' : `через ${n} ${plural(n, 'день', 'дня', 'дней')}`;
    return `${dateLong(o.selection.date).replace(/ \d{4}$/, '')} · ${rel}`;
  }

  dateShort(d: string): string { return dateLong(d).replace(/ \d{4}$/, ''); }
  dateFull(d: string): string { return dateLong(d).replace(/^[^,]+, /, ''); }

  resources(o: OrderView): string {
    if (o.selection.kind === 'sauna') return 'Сауна целиком';
    const s = sortSeats(o.selection.seats);
    return `${s.length > 1 ? 'Места' : 'Место'} ${s.join(', ')}`;
  }

  seatsOnly(o: OrderView): string { return o.selection.kind === 'session' ? sortSeats(o.selection.seats).join(', ') : 'Сауна целиком'; }

  guestsShort(o: OrderView): string {
    const g = o.guests;
    const kids = g.children.length;
    return kids ? `${g.adults} взр. · ${kids} реб.` : `${g.adults}`;
  }

  guestsCount(o: OrderView): number { return o.guests.adults + o.guests.children.length; }

  msg(e: unknown): string { return asApiError(e).message; }
}
