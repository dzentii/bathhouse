import { Component, input, output } from '@angular/core';
import { Notice } from './notice';

@Component({
  selector: 'app-load-error',
  imports: [Notice],
  template: `
    <app-notice kind="empty" [title]="title()">
      {{ message() }} <button type="button" class="link-btn" (click)="retry.emit()">Повторить</button>
    </app-notice>
  `,
})
export class LoadError {
  readonly title = input('Не удалось загрузить данные');
  readonly message = input('Проверьте соединение и попробуйте ещё раз.');
  readonly retry = output<void>();
}
