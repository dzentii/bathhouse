import { Component, computed, effect, inject, signal, untracked } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { rxResource } from '@angular/core/rxjs-interop';
import { BookingApi } from '../../core/api/booking-api';
import { AdminAuth } from '../../core/admin-auth.service';
import { TariffRule } from '../../core/models';
import { addDays, todayEkb, uuidv4 } from '../../core/format';
import { MoneyPipe } from '../../shared/money.pipe';
import { Notice } from '../../shared/notice';
import { LoadError } from '../../shared/load-error';
import { adminErrorHandler, catalogResource } from '../admin-common';

/** «1 500,50» → 150050 копеек; null при ошибке. Без потери копеек на float. */
export function parseRub(input: string): number | null {
  const s = input.replace(/\s/g, '').replace(',', '.');
  if (!/^\d+(\.\d{1,2})?$/.test(s)) return null;
  const [r, k = ''] = s.split('.');
  return Number(r) * 100 + Number(k.padEnd(2, '0'));
}

function ru(date: string): string { return date.split('-').reverse().join('.'); }

@Component({
  selector: 'app-admin-tariffs',
  imports: [FormsModule, MoneyPipe, Notice, LoadError],
  templateUrl: './tariffs.html',
  styleUrl: './tariffs.scss',
})
export class AdminTariffsPage {
  private api = inject(BookingApi);
  private auth = inject(AdminAuth);
  readonly err = adminErrorHandler();
  readonly catalog = catalogResource();

  readonly banyaId = signal('');
  readonly departmentId = signal('');
  readonly busy = signal(false);
  readonly error = signal<string | null>(null);
  readonly savedId = signal<string | null>(null);
  form = { condition: '', days: 'Пн–Пт', from: addDays(todayEkb(), 1), to: '', price: '' };

  readonly departments = computed(() => ((this.catalog.hasValue() ? this.catalog.value() : undefined) ?? []).find((c) => c.banya.id === this.banyaId())?.departments.filter((d) => d.mode !== 'info') ?? []);
  readonly rules = rxResource({
    params: () => this.departmentId() || undefined,
    stream: ({ params }) => this.api.adminTariffs(this.auth.token, params),
  });
  readonly conditions = computed(() => [...new Set(((this.rules.hasValue() ? this.rules.value() : undefined) ?? []).map((r) => r.condition))]);

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
      const c = this.conditions();
      if (c.length && !c.includes(this.form.condition)) this.form.condition = c[0];
    });
  }

  state(r: TariffRule): string { const t = todayEkb(); return r.from > t ? 'Запланирован' : r.to && r.to < t ? 'Завершён' : 'Действует'; }
  stateClass(r: TariffRule): string { const s = this.state(r); return s === 'Действует' ? 'badge-confirmed' : s === 'Запланирован' ? 'badge-held' : 'badge-expired'; }
  ru(d: string): string { return ru(d); }

  reset(): void { this.form = { ...this.form, to: '', price: '' }; this.error.set(null); this.savedId.set(null); }

  save(): void {
    const price = parseRub(this.form.price);
    this.savedId.set(null);
    if (price == null) { this.error.set('Укажите цену числом, например 1 500 или 1 500,50.'); return; }
    const sample = ((this.rules.hasValue() ? this.rules.value() : undefined) ?? []).find((r) => r.condition === this.form.condition);
    this.busy.set(true);
    this.error.set(null);
    this.api.adminSaveTariff(this.auth.token, {
      departmentId: this.departmentId(), condition: this.form.condition, days: this.form.days, time: 'Весь день',
      from: this.form.from, to: this.form.to || undefined, price, unit: sample?.unit ?? 'место',
    }, uuidv4()).subscribe({
      next: (r) => { this.busy.set(false); this.savedId.set(r.id); this.rules.reload(); },
      error: (e: unknown) => { this.busy.set(false); this.error.set(this.err(e)); },
    });
  }
}
