import { Component, computed, effect, inject, input, signal, untracked } from '@angular/core';
import { Router, RouterLink } from '@angular/router';
import { rxResource } from '@angular/core/rxjs-interop';
import { of } from 'rxjs';
import { BookingApi } from '../../core/api/booking-api';
import { CheckoutStore } from '../../core/checkout.store';
import { SessionSelection } from '../../core/models';
import { asApiError, dateLong, dayMonthShort, plural, sortSeats, weekdayShort } from '../../core/format';
import { SiteHeader } from '../../shared/site-header';
import { Notice } from '../../shared/notice';
import { LoadError } from '../../shared/load-error';
import { MoneyPipe } from '../../shared/money.pipe';

@Component({
  selector: 'app-seans',
  imports: [RouterLink, SiteHeader, Notice, LoadError, MoneyPipe],
  templateUrl: './seans.html',
  styleUrl: './seans.scss',
})
export class SeansPage {
  private api = inject(BookingApi);
  private store = inject(CheckoutStore);
  private router = inject(Router);

  readonly banyaId = input.required<string>();
  readonly departmentId = input.required<string>();

  readonly date = signal<string | null>(null);
  readonly sessionId = signal<string | null>(null);
  readonly picked = signal<string[]>([]);
  readonly lostSeats = signal<string[]>([]);
  private readonly navState = history.state as { conflict?: string; conflictSeats?: string[] } | null;
  readonly flash = signal<string | null>(this.navState?.conflict ?? null);
  /** Места, которые сервер назвал недоступными при удержании, — подсвечиваем на схеме. */
  readonly conflictSeats = signal<string[]>(this.navState?.conflictSeats ?? []);

  readonly dept = rxResource({
    params: () => ({ b: this.banyaId(), d: this.departmentId() }),
    stream: ({ params }) => this.api.getDepartment(params.b, params.d),
  });
  readonly dates = rxResource({ params: () => this.departmentId(), stream: ({ params }) => this.api.getSaleDates(params) });
  readonly sessions = rxResource({
    params: () => (this.date() ? { d: this.departmentId(), date: this.date()! } : undefined),
    stream: ({ params }) => this.api.getSessions(params.d, params.date),
  });
  readonly avail = rxResource({
    params: () => (this.date() && this.sessionId() ? { d: this.departmentId(), date: this.date()!, s: this.sessionId()! } : undefined),
    stream: ({ params }) => this.api.getSessionAvailability(params.d, params.date, params.s),
  });

  readonly session = computed(() => (this.sessions.hasValue() ? this.sessions.value() : undefined)?.find((s) => s.id === this.sessionId()) ?? null);
  readonly sortedPicked = computed(() => sortSeats(this.picked()));

  /** Схема по координатам сервера (0–1000), обрезанная по крайним местам. */
  readonly plan = computed(() => {
    const p = (this.avail.hasValue() ? this.avail.value() : undefined)?.scheme.plan;
    if (!p?.seats.length) return null;
    const pad = 45;
    const xs = p.seats.map((s) => s.x), ys = p.seats.map((s) => s.y);
    const x0 = Math.min(...xs) - pad, y0 = Math.min(...ys) - pad;
    const w = Math.max(...xs) + pad - x0, h = Math.max(...ys) + pad - y0;
    return {
      ratio: `${w} / ${h}`,
      seats: p.seats.map((s) => ({ label: s.label, left: ((s.x - x0) / w) * 100, top: ((s.y - y0) / h) * 100 })),
    };
  });

  private readonly selection = computed<SessionSelection | null>(() => {
    const s = this.session();
    if (!s || !this.date() || !this.picked().length) return null;
    return { kind: 'session', banyaId: this.banyaId(), departmentId: this.departmentId(), date: this.date()!, sessionId: s.id, start: s.start, end: s.end, seats: this.sortedPicked() };
  });

  readonly quote = rxResource({
    params: () => this.selection() ?? undefined,
    stream: ({ params }) => params ? this.api.quote(params, { adults: params.seats.length, children: [] }) : of(undefined),
  });

  /** Итог без исключения: в состоянии ошибки value() ресурса бросает. */
  readonly q = computed(() => (this.quote.hasValue() ? this.quote.value() : undefined));

  constructor() {
    // Восстанавливаем прежний выбор этого отделения (например, после «Изменить выбор»).
    const d = this.store.draft();
    effect(() => {
      const list = (this.dates.hasValue() ? this.dates.value() : undefined);
      if (!list || this.date()) return;
      const prev = d?.selection.kind === 'session' && d.selection.departmentId === untracked(this.departmentId) ? d.selection : null;
      const pick = prev && list.some((x) => x.date === prev.date && x.hasSessions) ? prev.date : list.find((x) => x.hasSessions)?.date ?? null;
      untracked(() => {
        this.date.set(pick);
        if (prev && pick === prev.date) { this.sessionId.set(prev.sessionId); this.picked.set(prev.seats); }
      });
    });
    // Автовыбор первого сеанса с местами.
    effect(() => {
      const list = (this.sessions.hasValue() ? this.sessions.value() : undefined);
      if (!list) return;
      const cur = untracked(this.sessionId);
      if (cur && list.some((s) => s.id === cur && s.free > 0)) return;
      untracked(() => { this.sessionId.set(list.find((s) => s.free > 0)?.id ?? null); this.picked.set([]); });
    });
    // Если выбранное место стало недоступно — убираем его и объясняем.
    effect(() => {
      const a = (this.avail.hasValue() ? this.avail.value() : undefined);
      if (!a) return;
      const cur = untracked(this.picked);
      const lost = cur.filter((s) => a.seats[s] !== 'free');
      untracked(() => {
        this.lostSeats.set(lost);
        if (lost.length) this.picked.set(cur.filter((s) => !lost.includes(s)));
      });
    });
  }

  pickDate(d: string): void { if (d !== this.date()) { this.date.set(d); this.sessionId.set(null); this.picked.set([]); this.lostSeats.set([]); } }
  pickSession(id: string): void { if (id !== this.sessionId()) { this.sessionId.set(id); this.picked.set([]); this.lostSeats.set([]); } }

  toggle(seat: string): void {
    this.flash.set(null);
    this.conflictSeats.set([]);
    this.picked.update((p) => (p.includes(seat) ? p.filter((x) => x !== seat) : [...p, seat]));
  }

  continue(): void {
    const sel = this.selection();
    if (!sel) return;
    const prev = this.store.draft();
    const sameSeats = prev?.selection.kind === 'session' && prev.selection.seats.length === sel.seats.length;
    this.store.setSelection(sel, sameSeats ? prev!.guests : undefined);
    this.router.navigate(['/order']);
  }

  nextDateHint(): string {
    const next = ((this.dates.hasValue() ? this.dates.value() : undefined) ?? []).find((d) => d.hasSessions && d.date > (this.date() ?? ''));
    return next ? ` — ближайшая с сеансами: ${dateLong(next.date).replace(/ \d{4}$/, '')}` : '';
  }

  seatsWord(): string { return plural(this.picked().length, 'место', 'места', 'мест'); }
  seatWord(st: string, on: boolean): string { return on ? 'выбрано' : st === 'occupied' ? 'занято' : st === 'blocked' ? 'закрыто' : 'свободно'; }
  wd(d: string): string { return weekdayShort(d); }
  dm(d: string): string { return dayMonthShort(d); }
  long(d: string): string { return dateLong(d); }
  msg(e: unknown): string { return asApiError(e).message; }
}
