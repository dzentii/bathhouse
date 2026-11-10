import { Component, computed, inject, input } from '@angular/core';
import { RouterLink } from '@angular/router';
import { rxResource } from '@angular/core/rxjs-interop';
import { BookingApi } from '../../core/api/booking-api';
import { asApiError } from '../../core/format';
import { SiteHeader } from '../../shared/site-header';
import { SiteFooter } from '../../shared/site-footer';
import { Icon } from '../../shared/icon';
import { LoadError } from '../../shared/load-error';
import { MoneyPipe } from '../../shared/money.pipe';

@Component({
  selector: 'app-department',
  imports: [RouterLink, SiteHeader, SiteFooter, Icon, LoadError, MoneyPipe],
  templateUrl: './department.html',
  styleUrl: './department.scss',
})
export class DepartmentPage {
  private api = inject(BookingApi);
  readonly banyaId = input.required<string>();
  readonly departmentId = input.required<string>();

  readonly data = rxResource({
    params: () => ({ b: this.banyaId(), d: this.departmentId() }),
    stream: ({ params }) => this.api.getDepartment(params.b, params.d),
  });

  readonly isSauna = computed(() => (this.data.hasValue() ? this.data.value() : undefined)?.department.mode === 'sauna');

  msg(e: unknown): string { return asApiError(e).message; }
}
