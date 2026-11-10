import { Component, inject } from '@angular/core';
import { RouterLink } from '@angular/router';
import { rxResource } from '@angular/core/rxjs-interop';
import { BookingApi } from '../../core/api/booking-api';
import { Department } from '../../core/models';
import { plural } from '../../core/format';
import { SiteHeader } from '../../shared/site-header';
import { SiteFooter } from '../../shared/site-footer';
import { Icon } from '../../shared/icon';
import { LoadError } from '../../shared/load-error';
import { forkJoin, switchMap } from 'rxjs';

@Component({
  selector: 'app-home',
  imports: [RouterLink, SiteHeader, SiteFooter, Icon, LoadError],
  templateUrl: './home.html',
  styleUrl: './home.scss',
})
export class HomePage {
  private api = inject(BookingApi);

  readonly data = rxResource({
    stream: () => this.api.getBanyas().pipe(switchMap((bs) => forkJoin(bs.map((b) => this.api.getBanya(b.id))))),
  });

  readonly steps = [
    { n: 'I', title: 'Баня и отделение', text: 'Выберите адрес, затем зал, сауну или справочное отделение.' },
    { n: 'II', title: 'Дата и время', text: 'Сеанс и места на схеме зала либо интервал аренды всей сауны.' },
    { n: 'III', title: 'Гости и контакт', text: 'Состав компании, имя и телефон. Без регистрации и SMS.' },
    { n: 'IV', title: 'Оплата и билет', text: 'Выбор удерживается 10 минут. После оплаты — один билет на весь заказ.' },
  ];

  totalDepts(): number {
    return ((this.data.hasValue() ? this.data.value() : undefined) ?? []).reduce((n, b) => n + b.departments.length, 0) || 16;
  }

  word(n: number): string { return plural(n, 'отделение', 'отделения', 'отделений'); }

  groupTags(ds: Department[]): string[] {
    const s = ds.filter((d) => d.mode === 'session').length;
    const a = ds.filter((d) => d.mode === 'sauna').length;
    const i = ds.filter((d) => d.mode === 'info').length;
    const res: string[] = [];
    if (s) res.push(`${s} ${plural(s, 'зал', 'зала', 'залов')} с местами на сеанс`);
    if (a) res.push(`${a} ${plural(a, 'сауна', 'сауны', 'саун')} целиком`);
    if (i) res.push(`${i} без предзаписи`);
    return res;
  }
}
