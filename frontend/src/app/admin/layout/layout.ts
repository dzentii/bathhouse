import { Component, inject } from '@angular/core';
import { RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { AdminAuth } from '../../core/admin-auth.service';
import { BookingApi } from '../../core/api/booking-api';

@Component({
  selector: 'app-admin-layout',
  imports: [RouterLink, RouterLinkActive, RouterOutlet],
  templateUrl: './layout.html',
  styleUrl: './layout.scss',
})
export class AdminLayout {
  readonly auth = inject(AdminAuth);
  readonly api = inject(BookingApi);
  readonly links = [
    { path: '/admin/orders', label: 'Заказы' },
    { path: '/admin/schedule', label: 'Расписание' },
    { path: '/admin/tariffs', label: 'Тарифы' },
    { path: '/admin/blocks', label: 'Блокировки' },
  ];
}
