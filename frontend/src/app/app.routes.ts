import { inject } from '@angular/core';
import { CanActivateFn, Router, Routes } from '@angular/router';
import { BookingApi } from './core/api/booking-api';
import { adminGuard } from './core/admin-auth.service';
import { customerGuard } from './core/customer-auth.service';

/** Кабинет есть только там, где его поддерживает сервер (в бекенде MVP — нет). */
const cabinetEnabled: CanActivateFn = () => inject(BookingApi).supportsCabinet || inject(Router).createUrlTree(['/']);

export const routes: Routes = [
  { path: '', loadComponent: () => import('./pages/home/home').then((m) => m.HomePage), title: 'Сандуновские бани — онлайн-запись' },
  { path: 'banya/:banyaId', loadComponent: () => import('./pages/banya/banya').then((m) => m.BanyaPage), title: 'Отделения бани' },
  { path: 'banya/:banyaId/:departmentId', loadComponent: () => import('./pages/department/department').then((m) => m.DepartmentPage), title: 'Отделение' },
  { path: 'banya/:banyaId/:departmentId/seans', loadComponent: () => import('./pages/seans/seans').then((m) => m.SeansPage), title: 'Выбор сеанса и мест' },
  { path: 'banya/:banyaId/:departmentId/sauna', loadComponent: () => import('./pages/sauna/sauna').then((m) => m.SaunaPage), title: 'Выбор времени сауны' },
  { path: 'order', loadComponent: () => import('./pages/order/order').then((m) => m.OrderPage), title: 'Оформление заказа' },
  { path: 'payment/:orderId', loadComponent: () => import('./pages/payment/payment').then((m) => m.PaymentPage), title: 'Проверка суммы и оплата' },
  { path: 'result/:orderId', loadComponent: () => import('./pages/result/result').then((m) => m.ResultPage), title: 'Результат оплаты' },
  { path: 'ticket/:token', loadComponent: () => import('./pages/ticket/ticket').then((m) => m.TicketPage), title: 'Электронный билет' },

  { path: 'cabinet/login', canActivate: [cabinetEnabled], loadComponent: () => import('./cabinet/login/login').then((m) => m.CabinetLoginPage), title: 'Вход в личный кабинет' },
  {
    path: 'cabinet',
    canActivate: [customerGuard],
    loadComponent: () => import('./cabinet/layout/layout').then((m) => m.CabinetLayout),
    children: [
      { path: '', loadComponent: () => import('./cabinet/tickets/tickets').then((m) => m.CabinetTicketsPage), title: 'Мои билеты' },
      { path: 'profile', loadComponent: () => import('./cabinet/profile/profile').then((m) => m.CabinetProfilePage), title: 'Личные данные' },
    ],
  },

  { path: 'admin/login', loadComponent: () => import('./admin/login/login').then((m) => m.AdminLoginPage), title: 'Вход администратора' },
  {
    path: 'admin',
    canActivate: [adminGuard],
    loadComponent: () => import('./admin/layout/layout').then((m) => m.AdminLayout),
    children: [
      { path: '', pathMatch: 'full', redirectTo: 'orders' },
      { path: 'orders', loadComponent: () => import('./admin/orders/orders').then((m) => m.AdminOrdersPage), title: 'Заказы · админка' },
      { path: 'orders/:orderId', loadComponent: () => import('./admin/order/order').then((m) => m.AdminOrderPage), title: 'Заказ · админка' },
      { path: 'schedule', loadComponent: () => import('./admin/schedule/schedule').then((m) => m.AdminSchedulePage), title: 'Расписание · админка' },
      { path: 'tariffs', loadComponent: () => import('./admin/tariffs/tariffs').then((m) => m.AdminTariffsPage), title: 'Тарифы · админка' },
      { path: 'blocks', loadComponent: () => import('./admin/blocks/blocks').then((m) => m.AdminBlocksPage), title: 'Блокировки · админка' },
    ],
  },
  { path: '**', loadComponent: () => import('./pages/not-found/not-found').then((m) => m.NotFoundPage), title: 'Страница не найдена' },
];
