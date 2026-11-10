import { Component, input } from '@angular/core';

export type NoticeKind = 'error' | 'warn' | 'info' | 'success' | 'empty';

/** Сообщения и состояния из UI-кита (ФЗ, раздел IV). */
@Component({
  selector: 'app-notice',
  template: `
    <div [class]="'n ' + kind()" [attr.role]="kind() === 'error' || kind() === 'warn' ? 'alert' : 'status'">
      @if (title()) { <b>{{ title() }}</b> }
      <div class="body"><ng-content /></div>
    </div>
  `,
  styles: `
    .n { padding: 16px 18px; font-size: 14px; line-height: 1.5; display: flex; flex-direction: column; gap: 4px; border: 1px solid; }
    .body:empty { display: none; }
    .error { border-color: var(--error-line); background: var(--error-bg); color: var(--error-text); } .error b { color: var(--error-strong); }
    .warn { border-color: var(--warn-line); background: var(--warn-bg); color: var(--warn-text); } .warn b { color: var(--warn-strong); }
    .info { border-color: var(--info-line); background: var(--info-bg); color: var(--info-text); } .info b { color: var(--info-strong); }
    .success { border-color: var(--success-line); background: var(--success-bg); color: var(--success-text); } .success b { color: var(--success-strong); }
    .empty { border: 1px dashed var(--line-quiet); color: var(--text-2); } .empty b { color: var(--text); }
    b { font-weight: 600; }
  `,
})
export class Notice {
  readonly kind = input<NoticeKind>('info');
  readonly title = input<string>('');
}
