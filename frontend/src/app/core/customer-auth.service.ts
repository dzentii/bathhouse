import { Injectable, computed, inject, signal } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { Observable, tap } from 'rxjs';
import { BookingApi } from './api/booking-api';
import { CustomerProfile, CustomerSession } from './models';

const KEY = 'sb-customer-session';

/**
 * Сессия покупателя в личном кабинете (необязательном — покупка без входа сохраняется).
 * Защита маршрута — только удобство: доступ к данным проверяет сервер по токену.
 */
@Injectable({ providedIn: 'root' })
export class CustomerAuth {
  private api = inject(BookingApi);
  private router = inject(Router);

  readonly session = signal<CustomerSession | null>(this.restore());
  readonly isLoggedIn = computed(() => this.session() !== null);
  readonly profile = computed(() => this.session()?.profile ?? null);
  readonly displayName = computed(() => this.profile()?.firstName || 'Кабинет');

  get token(): string { return this.session()?.token ?? ''; }

  private restore(): CustomerSession | null {
    try { const raw = localStorage.getItem(KEY); return raw ? (JSON.parse(raw) as CustomerSession) : null; } catch { return null; }
  }

  private persist(s: CustomerSession | null): void {
    this.session.set(s);
    try { s ? localStorage.setItem(KEY, JSON.stringify(s)) : localStorage.removeItem(KEY); } catch { /* хранилище недоступно */ }
  }

  verify(phone: string, code: string): Observable<CustomerSession> {
    return this.api.verifyLoginCode(phone, code).pipe(tap((s) => this.persist(s)));
  }

  setProfile(profile: CustomerProfile): void {
    const s = this.session();
    if (s) this.persist({ ...s, profile });
  }

  logout(): void {
    const t = this.token;
    this.persist(null);
    if (t) this.api.customerLogout(t).subscribe();
    this.router.navigate(['/']);
  }

  /** Сервер ответил «сессия недействительна». */
  expired(returnUrl?: string): void {
    this.persist(null);
    this.router.navigate(['/cabinet/login'], { queryParams: { returnUrl, expired: 1 } });
  }

  /** Локально завершить сессию без запроса (после удаления аккаунта). */
  clear(): void { this.persist(null); }
}

export const customerGuard: CanActivateFn = (_route, state) => {
  const auth = inject(CustomerAuth);
  if (!inject(BookingApi).supportsCabinet) return inject(Router).createUrlTree(['/']);
  return auth.isLoggedIn() ? true : inject(Router).createUrlTree(['/cabinet/login'], { queryParams: { returnUrl: state.url } });
};
