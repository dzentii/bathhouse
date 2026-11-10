import { Component, computed, inject, input, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { rxResource } from '@angular/core/rxjs-interop';
import { BookingApi } from '../../core/api/booking-api';
import { OrderView } from '../../core/models';
import { asApiError, childrenText, dateFull, dateTimeEkb, plural, resourcesText } from '../../core/format';
import { SiteHeader } from '../../shared/site-header';
import { LoadError } from '../../shared/load-error';
import { MoneyPipe } from '../../shared/money.pipe';
import { Icon } from '../../shared/icon';

@Component({
  selector: 'app-ticket',
  imports: [RouterLink, SiteHeader, LoadError, MoneyPipe, Icon],
  templateUrl: './ticket.html',
  styleUrl: './ticket.scss',
})
export class TicketPage {
  private api = inject(BookingApi);
  readonly token = input.required<string>();
  readonly copied = signal(false);

  readonly ticket = rxResource({ params: () => this.token(), stream: ({ params }) => this.api.getTicket(params) });
  /** PDF формирует сервер; ссылка содержит секретный токен — открываем, не логируем. */
  readonly pdf = computed(() => this.api.ticketPdfUrl(this.token()));

  full(d: string): string { return dateFull(d); }
  res(o: OrderView): string { return resourcesText(o.selection); }
  paidAt(o: OrderView): string { return o.paidAt ? dateTimeEkb(o.paidAt) : ''; }
  notFound(e: unknown): boolean { const c = asApiError(e).code; return c === 'not_found' || c === 'forbidden'; }

  guestLines(o: OrderView): string[] {
    const g = o.guests, r: string[] = [];
    if (g.adults) r.push(`${g.adults} ${plural(g.adults, 'взрослый', 'взрослых', 'взрослых')}`);
    if (g.children.length) r.push(childrenText(g.children));
    return r;
  }

  copy(): void {
    navigator.clipboard?.writeText(location.href).then(() => { this.copied.set(true); setTimeout(() => this.copied.set(false), 2500); }, () => undefined);
  }
  print(): void { window.print(); }
}
