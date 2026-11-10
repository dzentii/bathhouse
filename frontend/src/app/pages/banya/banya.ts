import { Component, computed, inject, input } from '@angular/core';
import { RouterLink } from '@angular/router';
import { rxResource, toSignal } from '@angular/core/rxjs-interop';
import { catchError, of } from 'rxjs';
import { BookingApi } from '../../core/api/booking-api';
import { Banya, Department, VisitMode } from '../../core/models';
import { asApiError } from '../../core/format';
import { SiteHeader } from '../../shared/site-header';
import { SiteFooter } from '../../shared/site-footer';
import { Icon } from '../../shared/icon';
import { LoadError } from '../../shared/load-error';
import { MoneyPipe } from '../../shared/money.pipe';

interface Group { id: string; mode: VisitMode; title: string; text: string; items: Department[] }

@Component({
  selector: 'app-banya',
  imports: [RouterLink, SiteHeader, SiteFooter, Icon, LoadError, MoneyPipe],
  templateUrl: './banya.html',
  styleUrl: './banya.scss',
})
export class BanyaPage {
  private api = inject(BookingApi);
  readonly banyaId = input.required<string>();

  readonly data = rxResource({
    params: () => this.banyaId(),
    stream: ({ params }) => this.api.getBanya(params),
  });

  msg(e: unknown): string { return asApiError(e).message; }

  private readonly all = toSignal(this.api.getBanyas().pipe(catchError(() => of([] as Banya[]))), { initialValue: [] as Banya[] });
  readonly other = computed(() => this.all().find((b) => b.id !== this.banyaId()) ?? null);
  /** «Баня №2» → «Баню №2». */
  readonly otherName = computed(() => (this.other()?.name ?? '').replace(/^Баня/, 'Баню'));

  readonly groups = computed<Group[]>(() => {
    const ds = (this.data.hasValue() ? this.data.value() : undefined)?.departments ?? [];
    const defs: Omit<Group, 'items'>[] = [
      { id: 'seans', mode: 'session', title: 'Места на сеанс', text: 'Выбираете сеанс и конкретные места по схеме зала.' },
      { id: 'sauna', mode: 'sauna', title: 'Сауны целиком', text: 'Помещение арендуется полностью на выбранный интервал — для своей компании.' },
      { id: 'info', mode: 'info', title: 'Посещение без предварительной продажи', text: 'Билеты покупаются только на месте, в кассе бани. Онлайн-оплаты нет.' },
    ];
    return defs.map((g) => ({ ...g, items: ds.filter((d) => d.mode === g.mode) })).filter((g) => g.items.length);
  });
}
