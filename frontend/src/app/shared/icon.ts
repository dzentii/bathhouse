import { Component, input } from '@angular/core';

export type IconName = 'pin' | 'info' | 'warn' | 'check' | 'cross' | 'timer' | 'copy' | 'user';

/** Небольшой набор линейных иконок из макета. */
@Component({
  selector: 'app-icon',
  template: `
    <svg [attr.width]="size()" [attr.height]="size()" viewBox="0 0 24 24" fill="none" stroke="currentColor"
         [attr.stroke-width]="stroke()" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      @switch (name()) {
        @case ('pin') { <svg:path d="M12 21s-7-6.2-7-11.5A7 7 0 0 1 19 9.5C19 14.8 12 21 12 21z"/><svg:circle cx="12" cy="9.5" r="2.5"/> }
        @case ('info') { <svg:circle cx="12" cy="12" r="9"/><svg:path d="M12 8v5M12 16.5v.5"/> }
        @case ('warn') { <svg:path d="M12 3l9 16H3z"/><svg:path d="M12 10v4M12 17v.5"/> }
        @case ('check') { <svg:path d="M5 12.5l4.5 4.5L19 7.5"/> }
        @case ('cross') { <svg:path d="M6 6l12 12M18 6L6 18"/> }
        @case ('timer') { <svg:circle cx="12" cy="13" r="8"/><svg:path d="M12 9v4l2.5 2M9 2h6"/> }
        @case ('user') { <svg:circle cx="12" cy="8" r="4"/><svg:path d="M4 21c1.5-4 4.5-6 8-6s6.5 2 8 6"/> }
        @case ('copy') { <svg:rect x="9" y="9" width="11" height="11" rx="1"/><svg:path d="M5 15V5a1 1 0 0 1 1-1h10"/> }
      }
    </svg>
  `,
  styles: `:host { display: inline-flex; flex: none; }`,
})
export class Icon {
  readonly name = input.required<IconName>();
  readonly size = input(18);
  readonly stroke = input(1.6);
}
