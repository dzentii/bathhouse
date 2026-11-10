import { Component, effect, inject, signal, untracked } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { Router } from '@angular/router';
import { rxResource } from '@angular/core/rxjs-interop';
import { BookingApi } from '../../core/api/booking-api';
import { CustomerAuth } from '../../core/customer-auth.service';
import { ApiError, CustomerProfile } from '../../core/models';
import { asApiError, dateTimeEkb, formatPhone, uuidv4 } from '../../core/format';
import { Notice } from '../../shared/notice';
import { LoadError } from '../../shared/load-error';
import { Icon } from '../../shared/icon';

interface ProfileForm {
  firstName: string;
  lastName: string;
  email: string;
  smsReminder: boolean;
  emailTicket: boolean;
  news: boolean;
}

function toForm(p: CustomerProfile): ProfileForm {
  return {
    firstName: p.firstName, lastName: p.lastName ?? '', email: p.email ?? '',
    smsReminder: p.notify.smsReminder, emailTicket: p.notify.emailTicket, news: p.notify.news,
  };
}

@Component({
  selector: 'app-cabinet-profile',
  imports: [FormsModule, Notice, LoadError, Icon],
  templateUrl: './profile.html',
  styleUrl: './profile.scss',
})
export class CabinetProfilePage {
  private api = inject(BookingApi);
  private auth = inject(CustomerAuth);
  private router = inject(Router);

  readonly profile = rxResource({ stream: () => this.api.getProfile(this.auth.token) });
  form: ProfileForm = { firstName: '', lastName: '', email: '', smsReminder: true, emailTicket: true, news: false };

  readonly busy = signal(false);
  readonly saved = signal(false);
  readonly error = signal<ApiError | null>(null);
  readonly confirmDelete = signal(false);
  readonly deleting = signal(false);

  constructor() {
    effect(() => {
      const p = (this.profile.hasValue() ? this.profile.value() : undefined);
      // untracked: setProfile читает и пишет сигнал сессии — без него эффект зациклится.
      if (p) untracked(() => { this.form = toForm(p); this.auth.setProfile(p); });
    });
    effect(() => {
      const e = this.profile.error();
      if (e && asApiError(e).code === 'unauthorized') this.auth.expired('/cabinet/profile');
    });
  }

  phone(p: CustomerProfile): string { return formatPhone(p.phone); }
  consent(p: CustomerProfile): string { return dateTimeEkb(p.consentAt).split(',')[0]; }
  fieldError(f: string): string | null { const e = this.error(); return e?.field === f ? e.message : null; }

  changed(): void { this.saved.set(false); }

  save(): void {
    if (this.busy()) return;
    if (!this.form.firstName.trim()) { this.error.set({ code: 'validation', field: 'firstName', message: 'Укажите имя.' }); return; }
    this.busy.set(true);
    this.error.set(null);
    this.saved.set(false);
    this.api.updateProfile(this.auth.token, {
      firstName: this.form.firstName, lastName: this.form.lastName, email: this.form.email,
      notify: { smsReminder: this.form.smsReminder, emailTicket: this.form.emailTicket, news: this.form.news },
    }, uuidv4()).subscribe({
      next: (p) => { this.busy.set(false); this.saved.set(true); this.profile.set(p); },
      error: (e: unknown) => {
        this.busy.set(false);
        const err = asApiError(e);
        if (err.code === 'unauthorized') { this.auth.expired('/cabinet/profile'); return; }
        this.error.set(err);
      },
    });
  }

  cancel(): void {
    const p = (this.profile.hasValue() ? this.profile.value() : undefined);
    if (p) this.form = toForm(p);
    this.error.set(null);
    this.saved.set(false);
  }

  deleteAccount(): void {
    this.deleting.set(true);
    this.api.deleteAccount(this.auth.token).subscribe({
      next: () => { this.auth.clear(); this.router.navigate(['/']); },
      error: (e: unknown) => { this.deleting.set(false); this.error.set(asApiError(e)); },
    });
  }

  msg(e: unknown): string { return asApiError(e).message; }
}
