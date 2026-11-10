import { Component, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { Router, RouterLink } from '@angular/router';
import { rxResource } from '@angular/core/rxjs-interop';
import { of } from 'rxjs';
import { BookingApi } from '../../core/api/booking-api';
import { CheckoutStore } from '../../core/checkout.store';
import { ApiError, Guests } from '../../core/models';
import { ageText, asApiError, dateLong, guestsCount, isValidPhone, normalizePhone, resourcesText, uuidv4 } from '../../core/format';
import { CustomerAuth } from '../../core/customer-auth.service';
import { SiteHeader } from '../../shared/site-header';
import { Notice } from '../../shared/notice';
import { MoneyPipe } from '../../shared/money.pipe';

@Component({
  selector: 'app-order',
  imports: [FormsModule, RouterLink, SiteHeader, Notice, MoneyPipe],
  templateUrl: './order.html',
  styleUrl: './order.scss',
})
export class OrderPage {
  readonly api = inject(BookingApi);
  private store = inject(CheckoutStore);
  private router = inject(Router);
  readonly customer = inject(CustomerAuth);

  readonly draft = this.store.draft;

  constructor() {
    // Вошедшему покупателю подставляем имя и телефон из кабинета, если поля ещё пустые.
    const p = this.customer.profile();
    const d = this.draft();
    if (p && d && !d.buyer.name.trim() && !d.buyer.phone.trim()) {
      this.store.update({ buyer: { name: [p.firstName, p.lastName].filter(Boolean).join(' '), phone: p.phone } });
    }
  }
  readonly busy = signal(false);
  readonly showErrors = signal(false);
  readonly error = signal<ApiError | null>(null);
  private idemKey: { payload: string; key: string } | null = null;

  readonly dept = rxResource({
    params: () => { const d = this.draft(); return d ? { b: d.selection.banyaId, d: d.selection.departmentId } : undefined; },
    stream: ({ params }) => this.api.getDepartment(params.b, params.d),
  });

  /** Пересчёт только при изменении выбора или гостей, а не при вводе имени/телефона. */
  private readonly quoteParams = computed(
    () => { const d = this.draft(); return d && this.guestsValid() ? { s: d.selection, g: d.guests } : undefined; },
    { equal: (a, b) => JSON.stringify(a) === JSON.stringify(b) },
  );

  readonly quote = rxResource({
    params: () => this.quoteParams(),
    stream: ({ params }) => params ? this.api.quote(params.s, params.g) : of(undefined),
  });

  /** Итог без исключения: в состоянии ошибки value() ресурса бросает. */
  readonly q = computed(() => (this.quote.hasValue() ? this.quote.value() : undefined));
  readonly deptName = computed(() => { const v = this.dept.hasValue() ? this.dept.value() : undefined; return v ? `${v.banya.name} · ${v.department.name}` : ''; });

  readonly seatCount = computed(() => { const s = this.draft()?.selection; return s?.kind === 'session' ? s.seats.length : 0; });
  /** Возраст ребёнка 0–17 полных лет (контракт API). */
  readonly ages = Array.from({ length: 18 }, (_, i) => i);
  readonly missingAges = computed(() => (this.draft()?.guests.children ?? []).some((a) => a == null));
  readonly guestsValid = computed(() => { const d = this.draft(); return !!d && d.guests.adults >= 1 && !this.missingAges(); });
  private readonly maxGuests = computed(() => (this.draft()?.selection.kind === 'sauna' && this.dept.hasValue() ? this.dept.value()?.department.maxGuests ?? null : null));
  readonly adultsAtMax = computed(() => {
    const d = this.draft();
    if (!d) return true;
    if (d.selection.kind === 'session') return d.guests.adults >= this.seatCount();
    const max = this.maxGuests();
    return max != null && guestsCount(d.guests) >= max;
  });
  readonly childrenAtMax = computed(() => {
    const d = this.draft();
    if (!d) return true;
    const max = this.maxGuests();
    return d.guests.children.length >= 10 || (max != null && guestsCount(d.guests) >= max);
  });
  readonly resources = computed(() => { const d = this.draft(); return d ? resourcesText(d.selection) : ''; });
  readonly backLink = computed(() => {
    const s = this.draft()?.selection;
    return s ? ['/banya', s.banyaId, s.departmentId, s.kind === 'session' ? 'seans' : 'sauna'] : ['/'];
  });

  readonly nameError = computed(() => (this.draft()?.buyer.name.trim() ? null : 'Укажите имя'));
  readonly phoneError = computed(() => (isValidPhone(this.draft()?.buyer.phone ?? '') ? null : 'Введите номер полностью: +7 и 10 цифр'));

  fieldError(field: string): string | null { const e = this.error(); return e?.field === field ? e.message : null; }

  private setGuests(g: Guests): void {
    this.error.set(null);
    this.store.update({ guests: g });
  }

  setAdults(n: number): void { const d = this.draft(); if (d) this.setGuests({ ...d.guests, adults: Math.max(1, n) }); }
  addChild(): void { const d = this.draft(); if (d) this.setGuests({ ...d.guests, children: [...d.guests.children, null] }); }
  removeChild(i: number): void { const d = this.draft(); if (d) this.setGuests({ ...d.guests, children: d.guests.children.filter((_, j) => j !== i) }); }
  setChildAge(i: number, age: number | null): void {
    const d = this.draft();
    if (d) this.setGuests({ ...d.guests, children: d.guests.children.map((a, j) => (j === i ? age : a)) });
  }
  ageLabel(a: number): string { return a === 0 ? 'до 1 года' : ageText(a); }

  setBuyer(patch: Partial<{ name: string; phone: string }>): void {
    const d = this.draft();
    if (!d) return;
    this.store.update({ buyer: { ...d.buyer, ...patch } });
  }

  normalize(p: string): string { return normalizePhone(p); }

  submit(): void {
    const d = this.draft();
    const q = this.q();
    this.showErrors.set(true);
    if (!d || !q || this.busy()) return;
    if (!this.guestsValid() || this.nameError() || this.phoneError()) {
      setTimeout(() => document.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
      return;
    }
    const buyer = { name: d.buyer.name.trim(), phone: normalizePhone(d.buyer.phone) };
    // Один ключ на одно и то же содержимое: повторное нажатие или сетевой повтор не создаст второе удержание.
    const payload = JSON.stringify([d.selection, d.guests, buyer]);
    if (this.idemKey?.payload !== payload) this.idemKey = { payload, key: uuidv4() };

    this.busy.set(true);
    this.error.set(null);
    this.api.createHold(d.selection, d.guests, buyer, q.total, this.idemKey.key, this.customer.token || undefined).subscribe({
      next: (r) => {
        this.store.saveAccess(r.order.id, r.accessToken);
        this.router.navigate(['/payment', r.order.id]);
      },
      error: (e: unknown) => {
        this.busy.set(false);
        const err = asApiError(e);
        if (err.code === 'conflict') {
          // Ничего не удержано — возвращаем к выбору с объяснением.
          this.router.navigate(this.backLink(), { state: { conflict: err.message, conflictSeats: err.conflictSeats ?? [] } });
          return;
        }
        this.error.set(err);
      },
    });
  }

  long(d: string): string { return dateLong(d); }
  msg(e: unknown): string { return asApiError(e).message; }
}
