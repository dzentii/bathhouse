import { Observable } from 'rxjs';
import {
  AdminOrderFilter, AdminSeatState, Banya, Buyer, CustomerProfile, CustomerSession, DateAvailability, Department,
  DepartmentDetails, Guests, HoldResult, LoginCodeRequest, OrderView, ProfilePatch, Quote, SaunaDay, ScheduleException, Selection, SessionAvailability, SessionSlot,
  TariffRule, TestPaymentOutcome, WeekScheduleDay,
} from '../models';

/**
 * Единая точка обмена с сервером. Компоненты знают только этот контракт.
 *
 * Реализации: HttpBookingApi — бекенд MVP (api/openapi.yaml), DemoBookingApi — данные в памяти браузера.
 * Выбор — environment.api (см. src/environments).
 *
 * Ошибки приходят как ApiError (см. models.ts) через throwError.
 */
export abstract class BookingApi {
  /** true — данные демонстрационные, не с сервера. */
  abstract readonly isDemo: boolean;
  /** Сервер поддерживает личный кабинет покупателя (вход по SMS). В бекенде MVP его нет. */
  abstract readonly supportsCabinet: boolean;
  /** Ссылка на PDF-билет, если сервер его формирует. */
  abstract ticketPdfUrl(ticketToken: string): string | null;

  // Каталог
  abstract getBanyas(): Observable<Banya[]>;
  abstract getBanya(banyaId: string): Observable<{ banya: Banya; departments: Department[] }>;
  abstract getDepartment(banyaId: string, departmentId: string): Observable<DepartmentDetails>;

  // Доступность
  abstract getSaleDates(departmentId: string): Observable<DateAvailability[]>;
  abstract getSessions(departmentId: string, date: string): Observable<SessionSlot[]>;
  abstract getSessionAvailability(departmentId: string, date: string, sessionId: string): Observable<SessionAvailability>;
  abstract getSaunaDay(departmentId: string, date: string): Observable<SaunaDay>;

  // Расчёт, удержание, оплата
  abstract quote(selection: Selection, guests: Guests): Observable<Quote>;
  /** Переход к оплате: сервер проверяет весь выбор и удерживает его на 10 минут («всё или ничего»). */
  /** customerToken — если покупатель вошёл в кабинет, заказ привязывается к нему. */
  abstract createHold(selection: Selection, guests: Guests, buyer: Buyer, expectedTotal: number, idempotencyKey: string, customerToken?: string): Observable<HoldResult>;
  abstract getOrder(orderId: string, accessToken: string): Observable<OrderView>;
  /** Явный возврат к выбору: снимает всё неоплаченное удержание. */
  abstract releaseHold(orderId: string, accessToken: string, idempotencyKey: string): Observable<void>;
  abstract confirmNewTotal(orderId: string, accessToken: string): Observable<OrderView>;
  abstract payTest(orderId: string, accessToken: string, outcome: TestPaymentOutcome, idempotencyKey: string): Observable<OrderView>;
  abstract getTicket(ticketToken: string): Observable<OrderView>;

  // Личный кабинет покупателя (вход по телефону и одноразовому коду)
  abstract requestLoginCode(phone: string): Observable<LoginCodeRequest>;
  abstract verifyLoginCode(phone: string, code: string): Observable<CustomerSession>;
  abstract customerLogout(token: string): Observable<void>;
  abstract getProfile(token: string): Observable<CustomerProfile>;
  abstract updateProfile(token: string, patch: ProfilePatch, idempotencyKey: string): Observable<CustomerProfile>;
  abstract deleteAccount(token: string): Observable<void>;
  /** Оплаченные заказы покупателя (по аккаунту и подтверждённому номеру телефона). */
  abstract myOrders(token: string): Observable<OrderView[]>;

  // Администратор
  abstract adminLogin(login: string, password: string): Observable<{ token: string; name: string }>;
  abstract adminLogout(token: string): Observable<void>;
  abstract adminOrders(token: string, filter: AdminOrderFilter): Observable<OrderView[]>;
  abstract adminOrder(token: string, orderId: string): Observable<OrderView>;
  abstract adminWeekSchedule(token: string, departmentId: string, weekStart: string): Observable<{ days: WeekScheduleDay[]; exceptions: ScheduleException[] }>;
  abstract adminCancelSession(token: string, departmentId: string, date: string, sessionId: string, idempotencyKey: string): Observable<void>;
  abstract adminTariffs(token: string, departmentId: string): Observable<TariffRule[]>;
  abstract adminSaveTariff(token: string, rule: Omit<TariffRule, 'id'>, idempotencyKey: string): Observable<TariffRule>;
  abstract adminSeatStates(token: string, departmentId: string, date: string, sessionId: string): Observable<{ scheme: { version: string; zones: { name: string; seats: AdminSeatState[] }[] } }>;
  abstract adminSetBlock(token: string, departmentId: string, date: string, sessionId: string, seats: string[], block: boolean, reason: string, idempotencyKey: string): Observable<void>;
}
