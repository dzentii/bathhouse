import { Pipe, PipeTransform } from '@angular/core';
import { formatMoney } from '../core/format';

@Pipe({ name: 'money' })
export class MoneyPipe implements PipeTransform {
  transform(value: number | null | undefined): string {
    return value == null ? '—' : formatMoney(value);
  }
}
