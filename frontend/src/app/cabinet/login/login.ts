import { Component, DestroyRef, ElementRef, computed, inject, input, signal, viewChildren } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { Router, RouterLink } from '@angular/router';
import { BookingApi } from '../../core/api/booking-api';
import { CustomerAuth } from '../../core/customer-auth.service';
import { asApiError, formatPhone, isValidPhone, normalizePhone } from '../../core/format';
import { Notice } from '../../shared/notice';

const CODE_LENGTH = 4;

@Component({
  selector: 'app-cabinet-login',
  imports: [FormsModule, RouterLink, Notice],
  templateUrl: './login.html',
  styleUrl: './login.scss',
})
export class CabinetLoginPage {
  private api = inject(BookingApi);
  private auth = inject(CustomerAuth);
  private router = inject(Router);

  readonly returnUrl = input<string | undefined>();
  readonly expired = input<string | undefined>();

  readonly step = signal<'phone' | 'code'>('phone');
  readonly phone = signal('');
  readonly digits = signal<string[]>(Array(CODE_LENGTH).fill(''));
  readonly busy = signal(false);
  readonly error = signal<string | null>(null);
  readonly demoCode = signal<string | null>(null);
  readonly resendAt = signal(0);
  readonly now = signal(Date.now());

  private readonly codeInputs = viewChildren<ElementRef<HTMLInputElement>>('codeInput');

  readonly phonePretty = computed(() => formatPhone(normalizePhone(this.phone())));
  readonly resendIn = computed(() => Math.max(0, Math.ceil((this.resendAt() - this.now()) / 1000)));
  readonly resendLabel = computed(() => { const s = this.resendIn(); return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`; });
  readonly codeComplete = computed(() => this.digits().every((d) => /^\d$/.test(d)));

  constructor() {
    if (this.auth.isLoggedIn()) this.router.navigateByUrl(this.safeReturn());
    const t = setInterval(() => this.now.set(Date.now()), 1000);
    inject(DestroyRef).onDestroy(() => clearInterval(t));
  }

  requestCode(): void {
    if (this.busy()) return;
    if (!isValidPhone(this.phone())) { this.error.set('Введите номер полностью: +7 и 10 цифр'); return; }
    this.busy.set(true);
    this.error.set(null);
    this.api.requestLoginCode(normalizePhone(this.phone())).subscribe({
      next: (r) => {
        this.busy.set(false);
        this.demoCode.set(r.demoCode ?? null);
        this.resendAt.set(Date.now() + r.resendInSec * 1000);
        this.digits.set(Array(CODE_LENGTH).fill(''));
        this.step.set('code');
        setTimeout(() => this.focusDigit(0));
      },
      error: (e: unknown) => { this.busy.set(false); this.error.set(asApiError(e).message); },
    });
  }

  changePhone(): void {
    this.step.set('phone');
    this.error.set(null);
    this.demoCode.set(null);
  }

  onDigit(i: number, ev: Event): void {
    const el = ev.target as HTMLInputElement;
    const val = el.value.replace(/\D/g, '');
    if (val.length > 1) { this.fill(val, i); return; } // вставка или автоподстановка кода из SMS
    this.digits.update((d) => d.map((x, k) => (k === i ? val : x)));
    el.value = val;
    this.error.set(null);
    if (val && i < CODE_LENGTH - 1) this.focusDigit(i + 1);
    if (this.codeComplete()) this.verify();
  }

  onKey(i: number, ev: KeyboardEvent): void {
    if (ev.key === 'Backspace' && !this.digits()[i] && i > 0) this.focusDigit(i - 1);
    if (ev.key === 'ArrowLeft' && i > 0) this.focusDigit(i - 1);
    if (ev.key === 'ArrowRight' && i < CODE_LENGTH - 1) this.focusDigit(i + 1);
  }

  onPaste(ev: ClipboardEvent): void {
    const text = ev.clipboardData?.getData('text') ?? '';
    if (/\d/.test(text)) { ev.preventDefault(); this.fill(text.replace(/\D/g, ''), 0); }
  }

  private fill(value: string, from: number): void {
    const chars = value.slice(0, CODE_LENGTH - from).split('');
    this.digits.update((d) => d.map((x, k) => (k >= from && k - from < chars.length ? chars[k - from] : x)));
    this.codeInputs().forEach((ref, k) => (ref.nativeElement.value = this.digits()[k]));
    this.focusDigit(Math.min(from + chars.length, CODE_LENGTH - 1));
    if (this.codeComplete()) this.verify();
  }

  private focusDigit(i: number): void { this.codeInputs()[i]?.nativeElement.focus(); }

  verify(): void {
    if (this.busy() || !this.codeComplete()) return;
    this.busy.set(true);
    this.error.set(null);
    this.auth.verify(normalizePhone(this.phone()), this.digits().join('')).subscribe({
      next: () => this.router.navigateByUrl(this.safeReturn()),
      error: (e: unknown) => {
        this.busy.set(false);
        this.error.set(asApiError(e).message);
        this.digits.set(Array(CODE_LENGTH).fill(''));
        this.codeInputs().forEach((ref) => (ref.nativeElement.value = ''));
        this.focusDigit(0);
      },
    });
  }

  resend(): void {
    if (this.resendIn() > 0) return;
    this.requestCode();
  }

  /** Возврат только на внутренние адреса сайта. */
  private safeReturn(): string {
    const r = this.returnUrl();
    return r && r.startsWith('/') && !r.startsWith('//') ? r : '/cabinet';
  }

  readonly indexes = Array.from({ length: CODE_LENGTH }, (_, i) => i);
}
