import { Component, inject, input } from '@angular/core';
import { RouterLink, RouterLinkActive } from '@angular/router';
import { CustomerAuth } from '../core/customer-auth.service';
import { toSignal } from '@angular/core/rxjs-interop';
import { catchError, of } from 'rxjs';
import { BookingApi } from '../core/api/booking-api';
import { Banya } from '../core/models';
import { Icon } from './icon';

const STEPS = ['Выбор', 'Оформление', 'Оплата', 'Билет'];

@Component({
  selector: 'app-site-header',
  imports: [RouterLink, RouterLinkActive, Icon],
  template: `
    <div class="bar">
      <header class="container inner">
        <a routerLink="/" class="logo">
          <span class="logo-name">Сандуновские бани</span>
          <span class="logo-city">Екатеринбург</span>
        </a>
        @if (step(); as current) {
          <ol class="steps" aria-label="Шаги оформления">
            @for (s of steps; track s; let i = $index) {
              <li [class.done]="i + 1 < current" [class.current]="i + 1 === current" [attr.aria-current]="i + 1 === current ? 'step' : null">
                <span class="num">{{ i + 1 }}</span><span class="txt">{{ s }}</span>
              </li>
            }
          </ol>
        } @else {
          <nav aria-label="Основная навигация" class="nav">
            @for (b of banyas(); track b.id) {
              <a [routerLink]="['/banya', b.id]" [class.active]="activeBanya() === b.id" [attr.aria-current]="activeBanya() === b.id ? 'page' : null">{{ b.name }}</a>
            }
            <a routerLink="/" fragment="how">Как проходит запись</a>
            <a routerLink="/" fragment="contacts">Контакты</a>
            @if (!cabinet) {
            } @else if (auth.isLoggedIn()) {
              <a routerLink="/cabinet" class="user" routerLinkActive="active" ariaCurrentWhenActive="page"><app-icon name="user" [stroke]="1.7" />{{ auth.displayName() }}</a>
            } @else {
              <a routerLink="/cabinet/login" class="cab-btn"><app-icon name="user" [stroke]="1.7" />Личный кабинет</a>
            }
          </nav>
        }
        <ng-content />
      </header>
    </div>
  `,
  styles: `
    .bar { border-bottom: 1px solid var(--line); background: var(--surface); }
    .inner { padding-top: 20px; padding-bottom: 20px; display: flex; align-items: center; justify-content: space-between; gap: 16px 24px; flex-wrap: wrap; }
    .logo { text-decoration: none; display: flex; flex-direction: column; }
    .logo-name { font-family: var(--serif); font-weight: 600; font-size: 28px; line-height: 1; color: var(--text); }
    .logo-city { font-size: 11px; letter-spacing: .2em; text-transform: uppercase; color: var(--muted); margin-top: 6px; }
    .nav { display: flex; gap: 12px 28px; flex-wrap: wrap; font-size: 15px; font-weight: 500; }
    .nav a { color: var(--text); text-decoration: none; padding-bottom: 4px; border-bottom: 1px solid transparent; }
    .nav { align-items: center; }
    .nav a:hover, .nav a.active { color: var(--green); border-bottom-color: var(--green); }
    .nav .user { display: inline-flex; gap: 8px; align-items: center; }
    .nav .cab-btn { display: inline-flex; gap: 8px; align-items: center; color: var(--green); border: 1px solid var(--green); padding: 8px 14px; margin: -9px 0; }
    .nav .cab-btn:hover { background: var(--surface-hover); }
    @media (max-width: 600px) { .nav .cab-btn { margin: 0; } }
    .steps { list-style: none; margin: 0; padding: 0; display: flex; gap: 12px 24px; flex-wrap: wrap; font-size: 14px; }
    .steps li { display: flex; gap: 8px; align-items: center; color: var(--muted); }
    .num { width: 26px; height: 26px; border-radius: 50%; border: 1px solid var(--line-quiet); display: inline-flex; align-items: center; justify-content: center; font-size: 13px; }
    .steps li.current { color: var(--green); font-weight: 600; }
    .steps li.current .num, .steps li.done .num { background: var(--green); border-color: var(--green); color: var(--on-green); }
    .steps li.done { color: var(--text-3); }
    @media (max-width: 600px) {
      .logo-name { font-size: 24px; }
      .steps li:not(.current) .txt { display: none; }
    }
  `,
})
export class SiteHeader {
  readonly step = input<number | null>(null);
  readonly activeBanya = input<string | null>(null);
  readonly steps = STEPS;
  readonly auth = inject(CustomerAuth);
  private api = inject(BookingApi);
  /** Личный кабинет показываем, только если его поддерживает сервер. */
  readonly cabinet = this.api.supportsCabinet;
  readonly banyas = toSignal(this.api.getBanyas().pipe(catchError(() => of([] as Banya[]))), { initialValue: [] as Banya[] });
}
