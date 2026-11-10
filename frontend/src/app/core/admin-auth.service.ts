import { Injectable, computed, inject, signal } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { Observable, tap } from 'rxjs';
import { BookingApi } from './api/booking-api';

const KEY = 'sb-admin-session';

/**
 * Клиентская сторона сессии администратора. Защита маршрута — только удобство:
 * права проверяет сервер при каждом закрытом запросе.
 */
@Injectable({ providedIn: 'root' })
export class AdminAuth {
  private api = inject(BookingApi);
  private router = inject(Router);
  readonly session = signal<{ token: string; name: string } | null>(this.restore());
  readonly isLoggedIn = computed(() => this.session() !== null);

  private restore(): { token: string; name: string } | null {
    try { const raw = sessionStorage.getItem(KEY); return raw ? JSON.parse(raw) : null; } catch { return null; }
  }

  get token(): string { return this.session()?.token ?? ''; }

  login(login: string, password: string): Observable<{ token: string; name: string }> {
    return this.api.adminLogin(login, password).pipe(tap((s) => {
      this.session.set(s);
      try { sessionStorage.setItem(KEY, JSON.stringify(s)); } catch { /* */ }
    }));
  }

  logout(): void {
    const t = this.token;
    this.clear();
    if (t) this.api.adminLogout(t).subscribe();
    this.router.navigate(['/admin/login']);
  }

  /** Вызывается, когда сервер ответил «нет доступа». */
  expired(): void {
    this.clear();
    this.router.navigate(['/admin/login'], { queryParams: { expired: 1 } });
  }

  private clear(): void {
    this.session.set(null);
    try { sessionStorage.removeItem(KEY); } catch { /* */ }
  }
}

export const adminGuard: CanActivateFn = () => {
  const auth = inject(AdminAuth);
  return auth.isLoggedIn() ? true : inject(Router).createUrlTree(['/admin/login']);
};
