import { Component, computed, effect, inject, input, signal, untracked } from '@angular/core';
import { Router, RouterLink } from '@angular/router';
import { rxResource } from '@angular/core/rxjs-interop';
import { of } from 'rxjs';
import { BookingApi } from '../../core/api/booking-api';
import { CheckoutStore } from '../../core/checkout.store';
import { SaunaSelection } from '../../core/models';
import { asApiError, dateLong, dayMonthShort, hh, plural, weekdayShort } from '../../core/format';
import { SiteHeader } from '../../shared/site-header';
import { Notice } from '../../shared/notice';
import { LoadError } from '../../shared/load-error';
import { MoneyPipe } from '../../shared/money.pipe';
import { Icon } from '../../shared/icon';

@Component({
  selector: 'app-sauna',
  imports: [RouterLink, SiteHeader, Notice, LoadError, MoneyPipe, Icon],
  templateUrl: './sauna.html',
  styleUrl: './sauna.scss',
})
export class SaunaPage {
  private api = inject(BookingApi);
  private store = inject(CheckoutStore);
  private router = inject(Router);

  readonly banyaId = input.required<string>();
  readonly departmentId = input.required<string>();

  readonly date = signal<string | null>(null);
  readonly start = signal<number | null>(null);
  readonly hoursCount = signal(3);
  readonly adults = signal(2);
  readonly kids = signal(0);
  readonly flash = signal<string | null>((history.state as { conflict?: string } | null)?.conflict ?? null);

  readonly dept = rxResource({
    params: () => ({ b: this.banyaId(), d: this.departmentId() }),
    stream: ({ params }) => this.api.getDepartment(params.b, params.d),
  });
  readonly dates = rxResource({ params: () => this.departmentId(), stream: ({ params }) => this.api.getSaleDates(params) });
  readonly day = rxResource({
    params: () => (this.date() ? { d: this.departmentId(), date: this.date()! } : undefined),
    stream: ({ params }) => this.api.getSaunaDay(params.d, params.date),
  });

  readonly hours = computed(() => {
    const d = (this.day.hasValue() ? this.day.value() : undefined);
    if (!d) return [];
    return Array.from({ length: d.close - d.open }, (_, i) => d.open + i);
  });

  readonly starts = computed(() => {
    const d = (this.day.hasValue() ? this.day.value() : undefined);
    if (!d) return [];
    const min = Math.min(...d.durations);
    const res: { h: number; label: string; busy: boolean }[] = [];
    for (let h = d.open; h <= d.close - min; h++) res.push({ h, label: hh(h), busy: d.busy.includes(h) });
    return res;
  });

  readonly conflict = computed(() => {
    const d = (this.day.hasValue() ? this.day.value() : undefined);
    const s = this.start();
    if (!d || s == null) return null;
    const end = s + this.hoursCount();
    if (end > d.close) return 'Интервал выходит за часы работы сауны. Выберите более раннее начало или короче длительность.';
    for (let h = s; h < end; h++) if (d.busy.includes(h)) return 'Интервал пересекается с занятым временем. Выберите другое начало или длительность.';
    return null;
  });

  readonly atMax = computed(() => {
    const max = (this.dept.hasValue() ? this.dept.value() : undefined)?.department.maxGuests;
    return max != null && this.adults() + this.kids() >= max;
  });

  readonly selection = computed<SaunaSelection | null>(() => {
    const s = this.start();
    if (s == null || !this.date() || this.conflict()) return null;
    return { kind: 'sauna', banyaId: this.banyaId(), departmentId: this.departmentId(), date: this.date()!, start: hh(s), hours: this.hoursCount(), end: hh(s + this.hoursCount()) };
  });

  readonly quote = rxResource({
    params: () => (this.selection() ? { sel: this.selection()!, a: this.adults() } : undefined),
    stream: ({ params }) => params ? this.api.quote(params.sel, { adults: params.a, children: [] }) : of(undefined),
  });

  /** Итог без исключения: в состоянии ошибки value() ресурса бросает. */
  readonly q = computed(() => (this.quote.hasValue() ? this.quote.value() : undefined));

  readonly canContinue = computed(() => !!this.selection() && !!this.q() && !this.quote.error() && !this.quote.isLoading());

  constructor() {
    // Прежний выбор этой сауны (например, после конфликта). Входные параметры доступны только после создания,
    // поэтому читаем их в эффекте, а не в конструкторе.
    const d = this.store.draft();
    const prevFor = (id: string) => (d?.selection.kind === 'sauna' && d.selection.departmentId === id ? d.selection : null);
    let restored = false;
    effect(() => {
      const prev = prevFor(this.departmentId());
      if (restored) return;
      restored = true;
      if (prev && d) untracked(() => { this.hoursCount.set(prev.hours); this.adults.set(d.guests.adults); this.kids.set(d.guests.children.length); });
    });
    effect(() => {
      const list = (this.dates.hasValue() ? this.dates.value() : undefined);
      if (!list || untracked(this.date)) return;
      const prev = prevFor(untracked(this.departmentId));
      const pick = prev && list.some((x) => x.date === prev.date) ? prev.date : list.find((x) => x.hasSessions)?.date ?? null;
      untracked(() => this.date.set(pick));
    });
    effect(() => {
      const dv = (this.day.hasValue() ? this.day.value() : undefined);
      if (!dv) return;
      untracked(() => {
        const prev = prevFor(this.departmentId());
        const want = prev && this.date() === prev.date ? Number(prev.start.slice(0, 2)) : this.start();
        const fits = (h: number | null, dur: number) => {
          if (h == null || h + dur > dv.close) return false;
          for (let x = h; x < h + dur; x++) if (dv.busy.includes(x)) return false;
          return true;
        };
        if (fits(want, this.hoursCount())) { this.start.set(want); return; }
        // Первый интервал, который целиком помещается в свободное время.
        for (const dur of [this.hoursCount(), ...dv.durations]) {
          const s = this.starts().find((x) => fits(x.h, dur));
          if (s) { this.start.set(s.h); this.hoursCount.set(dur); return; }
        }
        this.start.set(null);
      });
    });
  }

  pickDate(d: string): void { if (d !== this.date()) { this.date.set(d); this.start.set(null); } }
  inRange(h: number): boolean { const s = this.start(); return s != null && h >= s && h < s + this.hoursCount(); }
  rangeLabel(): string { const s = this.start()!; return `${hh(s)} – ${hh(s + this.hoursCount())}`; }
  guestsLabel(): string {
    const g = this.adults() + this.kids();
    return `${g} (${this.adults()} ${plural(this.adults(), 'взрослый', 'взрослых', 'взрослых')}${this.kids() ? `, ${this.kids()} ${plural(this.kids(), 'ребёнок', 'ребёнка', 'детей')}` : ''})`;
  }

  continue(): void {
    const sel = this.selection();
    if (!sel) return;
    // Возраст детей спрашиваем при оформлении; уже указанный сохраняем.
    const prevAges = this.store.draft()?.guests.children ?? [];
    this.store.setSelection(sel, { adults: this.adults(), children: Array.from({ length: this.kids() }, (_, i) => prevAges[i] ?? null) });
    this.router.navigate(['/order']);
  }

  wd(d: string): string { return weekdayShort(d); }
  dm(d: string): string { return dayMonthShort(d); }
  long(d: string): string { return dateLong(d); }
  msg(e: unknown): string { return asApiError(e).message; }
}
