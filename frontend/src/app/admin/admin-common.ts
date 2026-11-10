import { inject } from '@angular/core';
import { rxResource } from '@angular/core/rxjs-interop';
import { forkJoin, switchMap } from 'rxjs';
import { BookingApi } from '../core/api/booking-api';
import { AdminAuth } from '../core/admin-auth.service';
import { Banya, Department } from '../core/models';
import { asApiError } from '../core/format';

export interface CatalogEntry { banya: Banya; departments: Department[] }

/** Справочник бань и отделений для фильтров админки. */
export function catalogResource() {
  const api = inject(BookingApi);
  return rxResource<CatalogEntry[], void>({
    stream: () => api.getBanyas().pipe(switchMap((bs) => forkJoin(bs.map((b) => api.getBanya(b.id))))),
  });
}

/** Обработка ошибки закрытого запроса: при потере доступа — на страницу входа. */
export function adminErrorHandler() {
  const auth = inject(AdminAuth);
  return (e: unknown): string => {
    const err = asApiError(e);
    if (err.code === 'unauthorized') auth.expired();
    return err.message;
  };
}
