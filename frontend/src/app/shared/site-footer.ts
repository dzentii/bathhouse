import { Component, inject } from '@angular/core';
import { BookingApi } from '../core/api/booking-api';

@Component({
  selector: 'app-site-footer',
  template: `
    <footer id="contacts" class="foot">
      <div class="container grid">
        <div class="col">
          <span class="name">Сандуновские бани</span>
          <span>Онлайн-запись в бани Екатеринбурга</span>
        </div>
        <div class="col"><strong>Баня №1</strong><span>ул. Первомайская, 71</span></div>
        <div class="col"><strong>Баня №2</strong><span>ул. Лыжников, 38а</span></div>
        <div class="col">
          <strong>Тестовый режим</strong>
          <span>Оплата и билеты тестовые: деньги не списываются, билет не даёт права прохода.</span>
          @if (api.isDemo) { <span>Сейчас показаны демо-данные — сервер не подключён.</span> }
        </div>
      </div>
    </footer>
  `,
  styles: `
    .foot { margin-top: 112px; border-top: 1px solid var(--line); background: var(--surface); }
    .grid { padding-top: 48px; padding-bottom: 48px; display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 32px; font-size: 14px; color: var(--muted); line-height: 1.7; }
    .col { display: flex; flex-direction: column; gap: 2px; }
    .name { font-family: var(--serif); font-weight: 600; font-size: 24px; color: var(--text); line-height: 1.2; }
    strong { color: var(--text); font-weight: 600; }
  `,
})
export class SiteFooter {
  readonly api = inject(BookingApi);
}
