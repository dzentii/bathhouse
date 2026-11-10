import { Component, computed, effect, inject, signal, untracked } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { rxResource } from '@angular/core/rxjs-interop';
import { BookingApi } from '../../core/api/booking-api';
import { AdminAuth } from '../../core/admin-auth.service';
import { AdminSeatState } from '../../core/models';
import { dateLong, dayMonthShort, sortSeats, uuidv4, weekdayShort } from '../../core/format';
import { Notice } from '../../shared/notice';
import { LoadError } from '../../shared/load-error';
import { adminErrorHandler, catalogResource } from '../admin-common';

@Component({
  selector: 'app-admin-blocks',
  imports: [FormsModule, Notice, LoadError],
  templateUrl: './blocks.html',
  styleUrl: './blocks.scss',
})
export class AdminBlocksPage {
  private api = inject(BookingApi);
  private auth = inject(AdminAuth);
  readonly err = adminErrorHandler();
  readonly catalog = catalogResource();

  readonly banyaId = signal('');
  readonly departmentId = signal('');
  readonly date = signal('');
  readonly sessionId = signal('');
  readonly picked = signal<string[]>([]);
  readonly busy = signal(false);
  readonly error = signal<string | null>(null);
  readonly done = signal<string | null>(null);
  reason = '';

  readonly departments = computed(() => ((this.catalog.hasValue() ? this.catalog.value() : undefined) ?? []).find((c) => c.banya.id === this.banyaId())?.departments.filter((d) => d.mode === 'session') ?? []);
  readonly dates = rxResource({ params: () => this.departmentId() || undefined, stream: ({ params }) => this.api.getSaleDates(params) });
  readonly sessions = rxResource({
    params: () => (this.departmentId() && this.date() ? { d: this.departmentId(), date: this.date() } : undefined),
    stream: ({ params }) => this.api.getSessions(params.d, params.date),
  });
  readonly seats = rxResource({
    params: () => (this.departmentId() && this.date() && this.sessionId() ? { d: this.departmentId(), date: this.date(), s: this.sessionId() } : undefined),
    stream: ({ params }) => this.api.adminSeatStates(this.auth.token, params.d, params.date, params.s),
  });
  readonly session = computed(() => (this.sessions.hasValue() ? this.sessions.value() : undefined)?.find((s) => s.id === this.sessionId()) ?? null);
  readonly sorted = computed(() => sortSeats(this.picked()));

  constructor() {
    // По умолчанию — первая баня из каталога.
    effect(() => {
      const all = (this.catalog.hasValue() ? this.catalog.value() : undefined);
      if (all?.length && !all.some((c) => c.banya.id === untracked(this.banyaId))) untracked(() => this.banyaId.set(all[0].banya.id));
    });
    effect(() => {
      const list = this.departments();
      if (list.length && !list.some((d) => d.id === untracked(this.departmentId))) untracked(() => this.departmentId.set(list[0].id));
    });
    effect(() => {
      const list = (this.dates.hasValue() ? this.dates.value() : undefined);
      if (list?.length && !list.some((d) => d.date === untracked(this.date))) untracked(() => this.date.set(list.find((d) => d.hasSessions)?.date ?? list[0].date));
    });
    effect(() => {
      const list = (this.sessions.hasValue() ? this.sessions.value() : undefined);
      if (!list) return;
      untracked(() => { if (!list.some((s) => s.id === this.sessionId())) this.sessionId.set(list[0]?.id ?? ''); this.picked.set([]); });
    });
  }

  toggle(l: string): void {
    this.done.set(null);
    this.error.set(null);
    this.picked.update((p) => (p.includes(l) ? p.filter((x) => x !== l) : [...p, l]));
  }

  apply(block: boolean): void {
    this.busy.set(true);
    this.error.set(null);
    this.done.set(null);
    const seats = this.sorted();
    this.api.adminSetBlock(this.auth.token, this.departmentId(), this.date(), this.sessionId(), seats, block, this.reason.trim(), uuidv4()).subscribe({
      next: () => {
        this.busy.set(false);
        this.done.set(block ? `Места ${seats.join(', ')} закрыты для продажи.` : `Блокировка мест ${seats.join(', ')} снята.`);
        this.picked.set([]);
        this.seats.reload();
      },
      error: (e: unknown) => { this.busy.set(false); this.error.set(this.err(e)); },
    });
  }

  aria(s: AdminSeatState): string {
    const st = s.state === 'sold' ? `продано${s.orderId ? ', заказ ' + s.orderId : ''}` : s.state === 'blocked' ? 'заблокировано' : 'свободно';
    return `Место ${s.label}, ${st}`;
  }
  label(d: string): string { return `${weekdayShort(d)}, ${dayMonthShort(d)}`; }
  long(d: string): string { return dateLong(d); }
}
