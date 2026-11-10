import { Component, computed, effect, inject, signal, untracked } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { rxResource } from '@angular/core/rxjs-interop';
import { BookingApi } from '../../core/api/booking-api';
import { AdminAuth } from '../../core/admin-auth.service';
import { dayMonthShort, todayEkb, uuidv4, weekdayShort } from '../../core/format';
import { Notice } from '../../shared/notice';
import { LoadError } from '../../shared/load-error';
import { adminErrorHandler, catalogResource } from '../admin-common';

interface Picked { date: string; id: string; start: string; end: string; cancelled?: boolean }

@Component({
  selector: 'app-admin-schedule',
  imports: [FormsModule, Notice, LoadError],
  templateUrl: './schedule.html',
  styleUrl: './schedule.scss',
})
export class AdminSchedulePage {
  private api = inject(BookingApi);
  private auth = inject(AdminAuth);
  readonly err = adminErrorHandler();
  readonly catalog = catalogResource();
  readonly today = todayEkb();

  readonly banyaId = signal('');
  readonly departmentId = signal('');
  readonly weekStart = signal(todayEkb());
  readonly picked = signal<Picked | null>(null);
  readonly busy = signal(false);
  readonly error = signal<string | null>(null);
  readonly done = signal(false);

  readonly departments = computed(() => ((this.catalog.hasValue() ? this.catalog.value() : undefined) ?? []).find((c) => c.banya.id === this.banyaId())?.departments.filter((d) => d.mode === 'session') ?? []);

  readonly week = rxResource({
    params: () => (this.departmentId() ? { d: this.departmentId(), w: this.weekStart() } : undefined),
    stream: ({ params }) => this.api.adminWeekSchedule(this.auth.token, params.d, params.w),
  });

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
  }

  pick(p: Picked): void { this.picked.set(p); this.error.set(null); this.done.set(false); }

  apply(p: Picked): void {
    this.busy.set(true);
    this.error.set(null);
    this.api.adminCancelSession(this.auth.token, this.departmentId(), p.date, p.id, uuidv4()).subscribe({
      next: () => { this.busy.set(false); this.done.set(true); this.picked.set(null); this.week.reload(); },
      error: (e: unknown) => { this.busy.set(false); this.error.set(this.err(e)); },
    });
  }

  wd(d: string): string { return weekdayShort(d); }
  dm(d: string): string { return dayMonthShort(d); }
}
