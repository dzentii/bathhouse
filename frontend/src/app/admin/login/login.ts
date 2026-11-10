import { Component, inject, input, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { Router } from '@angular/router';
import { AdminAuth } from '../../core/admin-auth.service';
import { BookingApi } from '../../core/api/booking-api';
import { asApiError } from '../../core/format';
import { Notice } from '../../shared/notice';

@Component({
  selector: 'app-admin-login',
  imports: [FormsModule, Notice],
  templateUrl: './login.html',
  styleUrl: './login.scss',
})
export class AdminLoginPage {
  private auth = inject(AdminAuth);
  private router = inject(Router);
  readonly api = inject(BookingApi);
  readonly expired = input<string | undefined>();
  login = '';
  password = '';
  readonly busy = signal(false);
  readonly error = signal<string | null>(null);

  submit(): void {
    if (!this.login.trim() || !this.password) { this.error.set('Введите логин и пароль'); return; }
    this.busy.set(true);
    this.error.set(null);
    this.auth.login(this.login.trim(), this.password).subscribe({
      next: () => this.router.navigate(['/admin/orders']),
      error: (e: unknown) => { this.busy.set(false); this.password = ''; this.error.set(asApiError(e).message); },
    });
  }
}
