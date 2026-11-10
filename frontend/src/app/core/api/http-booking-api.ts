import { Injectable, inject } from '@angular/core';
import { HttpClient, HttpErrorResponse, HttpHeaders, HttpParameterCodec, HttpParams } from '@angular/common/http';
import { Observable, catchError, defer, forkJoin, map, of, shareReplay, switchMap, throwError, timer } from 'rxjs';
import { environment } from '../../../environments/environment';
import { BookingApi } from './booking-api';
import {
  AdminOrderFilter, AdminSeatState, ApiError, Banya, Buyer, CustomerProfile, CustomerSession, DateAvailability, Department,
  DepartmentDetails, Guests, HoldResult, LoginCodeRequest, OrderStatus, OrderView, Quote, SaunaDay, ScheduleException,
  Selection, SessionAvailability, SessionSlot, TariffLine, TariffRule, TestPaymentOutcome, WeekScheduleDay,
} from '../models';
import { TIME_ZONE, addDays, formatMoney, sortSeats, todayEkb, uuidv4 } from '../format';
import {
  WAttempt, WAvailability, WBath, WBlock, WCalendar, WConfig, WCreatedOrder, WDayType, WDepartment, WDepartments, WError, WGuest,
  WHeldOrder, WHistoryEntry, WLayout, WList, WOrder, WProfile, WQuote, WSelection, WSession, WSlot, WTariff, WTicket,
} from './wire';

/** Смещение Екатеринбурга: перехода на летнее время нет. */
const EKB_OFFSET = '+05:00';
const SESSION_SALE_DAYS = 7;
const SAUNA_SALE_DAYS = 14;
const CACHE_MS = 30_000;
const OPEN_END = '2100-01-01';
const PRICE_CHANGES = 'sb-price-changes';
const WEEK_ORDER = [1, 2, 3, 4, 5, 6, 0];
const WD_SHORT = ['Вс', 'Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб'];
/** Коды бекенда, которые для интерфейса означают «срок вышел». */
const EXPIRED_CODES = new Set(['order_inactive', 'order_access_expiring', 'hold_expired', 'expired']);

const STRICT_CODEC: HttpParameterCodec = {
  encodeKey: (k) => encodeURIComponent(k),
  encodeValue: (v) => encodeURIComponent(v),
  decodeKey: (k) => decodeURIComponent(k),
  decodeValue: (v) => decodeURIComponent(v),
};

interface DeptCtx { dept: WDepartment; banya: Banya; department: Department }
interface PriceChange { previous: number; confirmed: boolean }

/* ---------- Время ---------- */

const ekbParts = new Intl.DateTimeFormat('en-CA', {
  timeZone: TIME_ZONE, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
});
function ekb(iso: string): { date: string; time: string } {
  const p = Object.fromEntries(ekbParts.formatToParts(new Date(iso)).map((x) => [x.type, x.value]));
  return { date: `${p['year']}-${p['month']}-${p['day']}`, time: `${p['hour']}:${p['minute']}` };
}
/** Дата + минуты от полуночи (могут быть ≥ 1440) → RFC3339 по Екатеринбургу. */
function ekbIso(date: string, minutes: number): string {
  const d = addDays(date, Math.floor(minutes / 1440));
  const m = minutes % 1440;
  return `${d}T${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}:00${EKB_OFFSET}`;
}
function minutesOf(t: string): number { const [h, m] = t.split(':').map(Number); return h * 60 + m; }
function hm(min: number): string { return `${String(Math.floor(min / 60) % 24).padStart(2, '0')}:${String(min % 60).padStart(2, '0')}`; }
function weekday(date: string): number { return new Date(date + 'T12:00:00Z').getUTCDay(); }
function slotEnd(s: WSlot): number { const e = minutesOf(s.end); return e <= minutesOf(s.start) ? e + 1440 : e; }
function slotKey(s: { start: string; end: string }): string { return `${s.start}-${s.end}`; }

/* ---------- Гости ---------- */

function toWireGuests(g: Guests): WGuest[] {
  if (g.children.some((a) => a == null)) throw { code: 'validation', field: 'guests', message: 'Укажите возраст каждого ребёнка.' } satisfies ApiError;
  const res: WGuest[] = g.adults ? [{ category: 'adult', count: g.adults }] : [];
  const byAge = new Map<number, number>();
  for (const a of g.children as number[]) byAge.set(a, (byAge.get(a) ?? 0) + 1);
  [...byAge.entries()].sort((a, b) => a[0] - b[0]).forEach(([age, count]) => res.push({ category: 'child', age, count }));
  return res;
}
function fromWireGuests(list: WGuest[]): Guests {
  const g: Guests = { adults: 0, children: [] };
  for (const x of list) {
    const isChild = x.category === 'child' || (x.category == null && (x.age ?? 18) < 18);
    if (isChild) for (let i = 0; i < x.count; i++) g.children.push(x.age ?? null);
    else g.adults += x.count;
  }
  return g;
}

/* ---------- Тарифы ---------- */

type PriceField = 'adult_kopecks' | 'child_kopecks' | 'hourly_kopecks' | 'extra_hourly_kopecks';
const DAY_SETS: Record<string, WDayType[]> = { 'Пн–Пт': ['weekday'], 'Сб–Вс': ['weekend'], 'Все дни': ['weekday', 'weekend', 'holiday'] };

function windowText(r: WTariff): string {
  if (r.start_minute === 0 && r.end_minute === 1440) return '';
  if (r.start_minute === 0) return `до ${hm(r.end_minute)}`;
  if (r.end_minute === 1440) return `с ${hm(r.start_minute)}`;
  return `${hm(r.start_minute)}–${hm(r.end_minute)}`;
}
function durationText(min: number): string { return `${String(min / 60).replace('.', ',')} ч`; }
function fieldsFor(mode: string): PriceField[] { return mode === 'sauna' ? ['hourly_kopecks', 'extra_hourly_kopecks'] : ['adult_kopecks', 'child_kopecks']; }
function fieldLabel(f: PriceField, base: number): string {
  return ({ adult_kopecks: 'Взрослый', child_kopecks: 'Детский', hourly_kopecks: 'Аренда, час', extra_hourly_kopecks: `Гость сверх ${base}, час` } as const)[f];
}
function conditionOf(r: WTariff, f: PriceField, mode: string, base: number): string {
  const parts = [fieldLabel(f, base)];
  if (mode !== 'sauna' && r.duration_minutes) parts.push(durationText(r.duration_minutes));
  const w = windowText(r);
  if (w) parts.push(w);
  return parts.join(' · ');
}
function daysText(types: WDayType[]): string {
  const s = new Set(types);
  if (s.has('weekday') && s.has('weekend')) return 'Все дни';
  const parts = [s.has('weekday') ? 'Пн–Пт' : '', s.has('weekend') ? 'Сб–Вс' : '', s.has('holiday') ? 'праздники' : ''].filter(Boolean);
  return parts.join(', ');
}
function activeOn(r: WTariff, date: string): boolean { return r.valid_from <= date && date < r.valid_until; }

/* ---------- Ошибки ---------- */

function toApiError(e: unknown): ApiError {
  if (e && typeof e === 'object' && 'code' in e && 'message' in e && !(e instanceof HttpErrorResponse)) return e as ApiError;
  if (!(e instanceof HttpErrorResponse)) return { code: 'unknown', message: 'Непредвиденная ошибка. Попробуйте ещё раз.' };
  if (e.status === 0) return { code: 'network', message: 'Не удалось связаться с сервером. Проверьте соединение и попробуйте ещё раз.' };
  const body = e.error as Partial<WError> | null;
  const code = body?.error?.code ?? '';
  const message = body?.error?.message ?? 'Сервер не смог выполнить запрос.';
  if (EXPIRED_CODES.has(code)) return { code: 'expired', message };
  switch (e.status) {
    case 400: case 422: return { code: 'validation', message };
    case 401: return { code: 'unauthorized', message };
    case 403: return { code: 'forbidden', message };
    case 404: case 410: return { code: 'not_found', message };
    // Здесь в conflictSeats пока внутренние id ресурсов; в номера мест их переводит createHold.
    case 409: return { code: 'conflict', message, conflictSeats: body?.error?.details?.resource_ids };
    case 429: return { code: 'unknown', message: `${message} Повторите чуть позже.` };
    default: return { code: 'unknown', message: e.status >= 500 ? 'Сервис временно недоступен. Попробуйте ещё раз через несколько секунд.' : message };
  }
}

/**
 * Клиент бекенда MVP «Сандуновские бани» через единый HTTP-шлюз.
 * Приводит ответы к моделям интерфейса (models.ts), поэтому страницы не знают о формате API.
 * Токен заказа и токен сотрудника передаются только в свои запросы.
 */
@Injectable()
export class HttpBookingApi extends BookingApi {
  private http = inject(HttpClient);
  private base = environment.apiBaseUrl;

  readonly isDemo = false;
  readonly supportsCabinet = false;

  ticketPdfUrl(ticketToken: string): string | null { return `${this.base}/tickets/${encodeURIComponent(ticketToken)}/pdf`; }

  /* ---------- HTTP ---------- */

  private headers(opts: { token?: string; key?: string } = {}): HttpHeaders {
    let h = new HttpHeaders();
    if (opts.token) h = h.set('Authorization', `Bearer ${opts.token}`);
    if (opts.key) h = h.set('X-Idempotency-Key', opts.key);
    return h;
  }
  private get<T>(path: string, params?: Record<string, string | number | boolean | undefined>, token?: string): Observable<T> {
    // Строгое кодирование: стандартный кодировщик не экранирует «+» в «+05:00», и сервер читает его как пробел.
    let p = new HttpParams({ encoder: STRICT_CODEC });
    for (const [k, v] of Object.entries(params ?? {})) if (v !== undefined && v !== '') p = p.set(k, String(v));
    return this.http.get<T>(this.base + path, { params: p, headers: this.headers({ token }) }).pipe(catchError((e) => throwError(() => toApiError(e))));
  }
  private send<T>(method: 'POST' | 'PUT' | 'PATCH' | 'DELETE', path: string, body: unknown, opts: { token?: string; key?: string } = {}): Observable<T> {
    return this.http.request<T>(method, this.base + path, { body, headers: this.headers(opts) }).pipe(catchError((e) => throwError(() => toApiError(e))));
  }

  /* ---------- Кэш справочников ---------- */

  private cache = new Map<string, { at: number; value: Observable<unknown> }>();
  private cached<T>(key: string, factory: () => Observable<T>, ttl = CACHE_MS): Observable<T> {
    const hit = this.cache.get(key);
    if (hit && Date.now() - hit.at < ttl) return hit.value as Observable<T>;
    const value = factory().pipe(
      catchError((e) => { this.cache.delete(key); return throwError(() => e); }),
      shareReplay({ bufferSize: 1, refCount: false }),
    );
    this.cache.set(key, { at: Date.now(), value });
    return value;
  }
  private invalidate(prefix: string): void { for (const k of [...this.cache.keys()]) if (k.startsWith(prefix)) this.cache.delete(k); }

  private baths(): Observable<Banya[]> {
    return this.cached('baths', () => this.get<WList<WBath>>('/baths', { limit: 100 }).pipe(map((r) => r.items.map(toBanya))), 60_000);
  }
  private bath(id: string): Observable<Banya> {
    return this.baths().pipe(switchMap((list) => {
      const b = list.find((x) => x.id === id);
      return b ? of(b) : throwError(() => ({ code: 'not_found', message: 'Баня не найдена.' } satisfies ApiError));
    }));
  }
  private dept(id: string): Observable<WDepartment> { return this.cached(`dept:${id}`, () => this.get<WDepartment>(`/departments/${encodeURIComponent(id)}`), 60_000); }
  private config(id: string): Observable<WConfig> { return this.cached(`cfg:${id}`, () => this.get<WConfig>(`/departments/${encodeURIComponent(id)}/booking-config`)); }
  private layout(id: string): Observable<WLayout> { return this.cached(`layout:${id}`, () => this.get<WLayout>(`/departments/${encodeURIComponent(id)}/resources`), 60_000); }
  private availability(id: string, date: string, durationMinutes?: number): Observable<WAvailability> {
    return this.get<WAvailability>(`/departments/${encodeURIComponent(id)}/availability`, { date, duration_minutes: durationMinutes });
  }

  /** Отделение с баней и моделью интерфейса — для разбора заказов и билетов. */
  private ctx(deptId: string): Observable<DeptCtx> {
    return this.cached(`ctx:${deptId}`, () => this.dept(deptId).pipe(
      switchMap((d) => forkJoin({
        banya: this.bath(String(d.bath_id)),
        cfg: d.mode === 'info' ? of(undefined) : this.config(d.id).pipe(catchError(() => of(undefined))),
        layout: d.mode === 'session' ? this.layout(d.id).pipe(catchError(() => of(undefined))) : of(undefined),
      }).pipe(map(({ banya, cfg, layout }) => ({ dept: d, banya, department: toDepartment(d, cfg, layout) })))),
    ));
  }

  /* ---------- Каталог ---------- */

  getBanyas(): Observable<Banya[]> { return this.baths(); }

  getBanya(banyaId: string): Observable<{ banya: Banya; departments: Department[] }> {
    return forkJoin({
      banya: this.bath(banyaId),
      list: this.get<WDepartments>(`/baths/${encodeURIComponent(banyaId)}/departments`, { limit: 100 }),
    }).pipe(switchMap(({ banya, list }) => {
      if (!list.items.length) return of({ banya, departments: [] });
      // Цена «от» и вместимость — из опубликованных правил отделения.
      return forkJoin(list.items.map((d) => d.mode === 'info'
        ? of(toDepartment(d))
        : this.config(d.id).pipe(map((c) => toDepartment(d, c)), catchError(() => of(toDepartment(d))))))
        .pipe(map((departments) => ({ banya, departments })));
    }));
  }

  getDepartment(banyaId: string, departmentId: string): Observable<DepartmentDetails> {
    return this.dept(departmentId).pipe(switchMap((d) => {
      if (String(d.bath_id) !== banyaId) return throwError(() => ({ code: 'not_found', message: 'Отделение не найдено в этой бане.' } satisfies ApiError));
      return forkJoin({
        banya: this.bath(banyaId),
        cfg: d.mode === 'info' ? of(undefined) : this.config(d.id),
        layout: d.mode === 'session' ? this.layout(d.id).pipe(catchError(() => of(undefined))) : of(undefined),
      }).pipe(map(({ banya, cfg, layout }) => ({
        banya,
        department: toDepartment(d, cfg, layout),
        scheduleRows: cfg ? scheduleRows(cfg) : [],
        tariffRows: cfg ? tariffRows(cfg) : [],
      })));
    }));
  }

  /* ---------- Доступность ---------- */

  getSaleDates(departmentId: string): Observable<DateAvailability[]> {
    return this.dept(departmentId).pipe(switchMap((d) => {
      const from = todayEkb();
      const to = addDays(from, (d.mode === 'sauna' ? SAUNA_SALE_DAYS : SESSION_SALE_DAYS) - 1);
      return this.get<WCalendar>(`/departments/${encodeURIComponent(departmentId)}/availability-calendar`, { from, to });
    }), map((c) => c.items.map((i) => ({ date: i.date, hasSessions: i.status === 'available' }))));
  }

  getSessions(departmentId: string, date: string): Observable<SessionSlot[]> {
    return this.availability(departmentId, date).pipe(map((a) => a.items.map((i) => ({
      id: `${i.starts_at}|${i.ends_at}`,
      start: ekb(i.starts_at).time,
      end: ekb(i.ends_at).time,
      free: i.resources.filter((r) => r.state === 'free').length,
    }))));
  }

  getSessionAvailability(departmentId: string, date: string, sessionId: string): Observable<SessionAvailability> {
    return forkJoin({ layout: this.layout(departmentId), a: this.availability(departmentId, date) }).pipe(switchMap(({ layout, a }) => {
      const item = a.items.find((i) => `${i.starts_at}|${i.ends_at}` === sessionId);
      if (!item) return throwError(() => ({ code: 'conflict', message: 'Этот сеанс больше недоступен. Выберите другой.' } satisfies ApiError));
      const state = new Map(item.resources.map((r) => [r.id, r.state ?? 'free']));
      const active = layout.resources.filter((r) => r.active !== false);
      const seats: SessionAvailability['seats'] = {};
      for (const r of active) {
        const st = state.get(r.id);
        seats[r.label] = st === 'free' ? 'free' : st === 'blocked' || st === undefined ? 'blocked' : 'occupied';
      }
      return of({
        scheme: {
          version: String(layout.version),
          zones: [{ name: 'Места', seats: sortSeats(active.map((r) => r.label)) }],
          plan: { width: layout.width, height: layout.height, seats: active.map((r) => ({ label: r.label, x: r.x, y: r.y })) },
        },
        seats,
      });
    }));
  }

  getSaunaDay(departmentId: string, date: string): Observable<SaunaDay> {
    return this.config(departmentId).pipe(switchMap((cfg) => {
      const slots = cfg.schedule.exceptions[date] ?? cfg.schedule.week[String(weekday(date))] ?? [];
      if (!slots.length) return of({ open: 0, close: 0, busy: [], durations: [] });
      const open = Math.floor(Math.min(...slots.map((s) => minutesOf(s.start))) / 60);
      const close = Math.ceil(Math.max(...slots.map(slotEnd)) / 60);
      const step = Math.max(60, Math.ceil((cfg.duration_step_minutes || 60) / 60) * 60);
      const min = Math.max(60, cfg.min_duration_minutes || 60);
      const durations: number[] = [];
      for (let m = min; m <= (close - open) * 60; m += step) durations.push(m / 60);
      // Час свободен, если с него можно начать минимальную аренду (сервер учитывает перерыв после брони).
      return this.availability(departmentId, date, 60).pipe(map((a) => {
        const freeStarts = new Set(a.items.filter((i) => i.resources.some((r) => r.state === 'free')).map((i) => ekb(i.starts_at).time));
        const busy: number[] = [];
        for (let h = open; h < close; h++) if (!freeStarts.has(hm(h * 60))) busy.push(h);
        return { open, close, busy, durations };
      }));
    }));
  }

  /* ---------- Расчёт и заказ ---------- */

  private toWireSelection(sel: Selection, guests: Guests): Observable<WSelection> {
    return defer(() => {
      const g = toWireGuests(guests);
      return this.layout(sel.departmentId).pipe(map((layout): WSelection => {
        if (sel.kind === 'session') {
          const [starts_at, ends_at] = sel.sessionId.split('|');
          const ids = sel.seats.map((label) => layout.resources.find((r) => r.label === label)?.id);
          if (ids.some((x) => !x)) throw { code: 'conflict', message: 'Схема зала изменилась. Выберите места заново.' } satisfies ApiError;
          return { department_id: sel.departmentId, resource_ids: ids as string[], starts_at, ends_at, guests: g };
        }
        const start = minutesOf(sel.start);
        const room = layout.resources.find((r) => r.active !== false) ?? layout.resources[0];
        if (!room) throw { code: 'not_found', message: 'Сауна не найдена.' } satisfies ApiError;
        return { department_id: sel.departmentId, resource_ids: [room.id], starts_at: ekbIso(sel.date, start), ends_at: ekbIso(sel.date, start + sel.hours * 60), guests: g };
      }));
    });
  }

  quote(selection: Selection, guests: Guests): Observable<Quote> {
    return this.toWireSelection(selection, guests).pipe(
      switchMap((s) => this.send<WQuote>('POST', '/quotes', s, { key: uuidv4() })),
      map(toQuote),
    );
  }

  createHold(selection: Selection, guests: Guests, buyer: Buyer, expectedTotal: number, idempotencyKey: string): Observable<HoldResult> {
    return this.toWireSelection(selection, guests).pipe(
      // Тот же ключ и то же тело при повторе вернут уже созданный черновик, а не второй заказ.
      switchMap((s) => this.send<WCreatedOrder>('POST', '/orders', { selection: s, buyer }, { key: idempotencyKey })),
      switchMap((created) => this.send<WHeldOrder>('POST', `/orders/${created.order.id}/hold`, {}, { token: created.order_token, key: uuidv4() }).pipe(
        switchMap((held) => {
          if (held.order.amount_kopecks !== expectedTotal || held.price_changed) this.savePriceChange(held.order.id, { previous: expectedTotal, confirmed: false });
          return this.view(held.order);
        }),
        map((order) => ({ order, accessToken: created.order_token })),
      )),
      catchError((e: ApiError) => (e.code === 'conflict' && e.conflictSeats?.length ? this.conflictWithLabels(selection, e) : throwError(() => e))),
    );
  }

  /** 409 resource_unavailable: id недоступных ресурсов → номера мест для подсветки и текста. */
  private conflictWithLabels(sel: Selection, e: ApiError): Observable<never> {
    return this.layout(sel.departmentId).pipe(
      catchError(() => of(undefined)),
      switchMap((layout) => {
        if (sel.kind === 'sauna') return throwError(() => ({ ...e, conflictSeats: undefined, message: 'Это время сауны уже занято. Ничего не удержано — выберите другой интервал.' } satisfies ApiError));
        const labels = sortSeats((e.conflictSeats ?? []).map((id) => layout?.resources.find((r) => r.id === id)?.label).filter((l): l is string => !!l));
        if (!labels.length) return throwError(() => ({ ...e, conflictSeats: undefined }));
        const many = labels.length > 1;
        return throwError(() => ({
          ...e,
          conflictSeats: labels,
          message: `${many ? 'Места' : 'Место'} ${labels.join(', ')} уже ${many ? 'недоступны' : 'недоступно'}. Ничего не удержано — выберите ${many ? 'другие места' : 'другое место'}.`,
        } satisfies ApiError));
      }),
    );
  }

  getOrder(orderId: string, accessToken: string): Observable<OrderView> {
    return this.get<WOrder>(`/orders/${encodeURIComponent(orderId)}`, undefined, accessToken).pipe(
      catchError((e: ApiError) => throwError(() => (e.code === 'unauthorized' ? { code: 'not_found', message: 'Оформление не найдено или доступ к нему истёк.' } : e))),
      switchMap((o) => this.view(o)),
    );
  }

  releaseHold(orderId: string, accessToken: string, idempotencyKey: string): Observable<void> {
    return this.send<WOrder>('POST', `/orders/${encodeURIComponent(orderId)}/release`, {}, { token: accessToken, key: idempotencyKey }).pipe(map(() => undefined));
  }

  confirmNewTotal(orderId: string, accessToken: string): Observable<OrderView> {
    // Сервер не хранит согласие: сумма и price_hash подтверждаются самим платежом. Отмечаем согласие локально.
    const c = this.priceChanges()[orderId];
    if (c) this.savePriceChange(orderId, { ...c, confirmed: true });
    return this.getOrder(orderId, accessToken);
  }

  payTest(orderId: string, accessToken: string, outcome: TestPaymentOutcome, idempotencyKey: string): Observable<OrderView> {
    const id = encodeURIComponent(orderId);
    return this.get<WOrder>(`/orders/${id}`, undefined, accessToken).pipe(
      switchMap((o) => this.send<WAttempt>('POST', `/orders/${id}/payments`, {
        amount_kopecks: o.amount_kopecks, currency: 'RUB', price_hash: o.quote.price_hash, outcome: outcome === 'decline' ? 'declined' : 'success',
      }, { token: accessToken, key: idempotencyKey })),
      switchMap((attempt) => {
        // «Ответ потерян»: платёж отправлен, но клиент делает вид, что ответа нет, — итог восстанавливается по заказу.
        if (outcome === 'lost') return throwError(() => ({ code: 'network', message: 'Ответ на оплату не получен.' } satisfies ApiError));
        return this.waitAttempt(id, attempt, accessToken);
      }),
      switchMap((attempt) => {
        if (attempt.status === 'declined' || attempt.status === 'rejected') {
          // reason — машинный код (например test_declined); показываем только человекочитаемый текст.
          const reason = /[А-Яа-яЁё]/.test(attempt.reason) ? ` ${attempt.reason}` : '';
          return throwError(() => ({ code: 'declined', message: `Тестовая оплата отклонена, билет не выдан.${reason}` } satisfies ApiError));
        }
        return this.waitConfirmed(id, accessToken);
      }),
    );
  }

  /** Опрос попытки, пока она в обработке. Без ответа — «результат уточняется», а не отказ. */
  private waitAttempt(orderId: string, attempt: WAttempt, token: string, tries = 15): Observable<WAttempt> {
    if (attempt.status !== 'pending') return of(attempt);
    if (tries <= 0) return throwError(() => ({ code: 'network', message: 'Результат оплаты ещё не получен.' } satisfies ApiError));
    return timer(1000).pipe(
      switchMap(() => this.get<WAttempt>(`/orders/${orderId}/payments/${attempt.id}`, undefined, token)),
      switchMap((a) => this.waitAttempt(orderId, a, token, tries - 1)),
    );
  }

  /** Принятая оплата подтверждает заказ асинхронно — ждём статус confirmed. */
  private waitConfirmed(orderId: string, token: string, tries = 15): Observable<OrderView> {
    return this.get<WOrder>(`/orders/${orderId}`, undefined, token).pipe(switchMap((o) => {
      if (o.status !== 'held' && o.status !== 'draft') return this.view(o);
      if (tries <= 0) return throwError(() => ({ code: 'network', message: 'Подтверждение заказа ещё не получено.' } satisfies ApiError));
      return timer(1000).pipe(switchMap(() => this.waitConfirmed(orderId, token, tries - 1)));
    }));
  }

  getTicket(ticketToken: string): Observable<OrderView> {
    return this.get<WTicket>(`/tickets/${encodeURIComponent(ticketToken)}`).pipe(switchMap((t) => this.ctx(t.visit.department_id).pipe(map((c): OrderView => ({
      id: t.order_id,
      status: 'confirmed',
      banya: c.banya,
      department: c.department,
      selection: toSelection(t.visit, c),
      guests: fromWireGuests(t.visit.guests),
      buyer: { name: '', phone: '' },
      quote: toQuote(t.visit),
      paidAt: t.paid_at,
      createdAt: t.paid_at,
      ticketToken,
      history: [],
    })))));
  }

  private view(o: WOrder, status?: OrderStatus): Observable<OrderView> {
    return this.ctx(o.department_id).pipe(map((c) => toOrderView(o, c, this.priceChanges()[o.id], status)));
  }

  private priceChanges(): Record<string, PriceChange> {
    try { return JSON.parse(sessionStorage.getItem(PRICE_CHANGES) ?? '{}') as Record<string, PriceChange>; } catch { return {}; }
  }
  private savePriceChange(orderId: string, c: PriceChange): void {
    try { sessionStorage.setItem(PRICE_CHANGES, JSON.stringify({ ...this.priceChanges(), [orderId]: c })); } catch { /* хранилище недоступно */ }
  }

  /* ---------- Личный кабинет: в бекенде MVP нет ---------- */

  private noCabinet<T>(): Observable<T> {
    return throwError(() => ({ code: 'not_found', message: 'Личный кабинет пока не поддерживается сервером. Покупка доступна без входа.' } satisfies ApiError));
  }
  requestLoginCode(): Observable<LoginCodeRequest> { return this.noCabinet(); }
  verifyLoginCode(): Observable<CustomerSession> { return this.noCabinet(); }
  customerLogout(): Observable<void> { return of(undefined); }
  getProfile(): Observable<CustomerProfile> { return this.noCabinet(); }
  updateProfile(): Observable<CustomerProfile> { return this.noCabinet(); }
  deleteAccount(): Observable<void> { return this.noCabinet(); }
  myOrders(): Observable<OrderView[]> { return this.noCabinet(); }

  /* ---------- Администратор ---------- */

  adminLogin(login: string, password: string): Observable<{ token: string; name: string }> {
    return this.send<WSession>('POST', '/admin/sessions', { username: login, password }, { key: uuidv4() }).pipe(
      catchError((e: ApiError) => throwError(() => (e.code === 'unauthorized' || e.code === 'validation' ? { ...e, message: 'Неверный логин или пароль.' } : e))),
      switchMap((s) => this.get<WProfile>('/admin/me', undefined, s.access_token).pipe(
        map((p) => ({ token: s.access_token, name: p.employee.display_name || p.employee.username })),
      )),
    );
  }

  adminLogout(token: string): Observable<void> {
    return this.send<void>('DELETE', '/admin/sessions/current', undefined, { token }).pipe(catchError(() => of(undefined)));
  }

  adminOrders(token: string, filter: AdminOrderFilter): Observable<OrderView[]> {
    const params = {
      limit: 100,
      department_id: filter.departmentId,
      from: filter.from ? ekbIso(filter.from, 0) : undefined,
      until: filter.to ? ekbIso(addDays(filter.to, 1), 0) : undefined,
    };
    return this.get<WList<WOrder>>('/admin/orders', params, token).pipe(
      switchMap((r) => (r.items.length ? forkJoin(r.items.map((o) => this.view(o, adminStatus(o)))) : of([]))),
      map((list) => list.filter((o) => !filter.banyaId || o.banya.id === filter.banyaId)
        .sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt))),
    );
  }

  adminOrder(token: string, orderId: string): Observable<OrderView> {
    const id = encodeURIComponent(orderId);
    return forkJoin({
      o: this.get<WOrder>(`/admin/orders/${id}`, undefined, token),
      h: this.get<{ items: WHistoryEntry[] }>(`/admin/orders/${id}/history`, { limit: 100 }, token).pipe(catchError(() => of({ items: [] }))),
    }).pipe(switchMap(({ o, h }) => this.view(o, adminStatus(o)).pipe(map((v) => ({
      ...v,
      history: [...h.items].sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at)).map((x) => ({ at: x.created_at, text: historyText(x) })),
    })))));
  }

  private adminConfig(token: string, departmentId: string, kind: 'schedules' | 'tariffs'): Observable<WConfig> {
    return this.get<WConfig>(`/admin/${kind}/${encodeURIComponent(departmentId)}`, undefined, token);
  }

  adminWeekSchedule(token: string, departmentId: string, weekStart: string): Observable<{ days: WeekScheduleDay[]; exceptions: ScheduleException[] }> {
    return this.adminConfig(token, departmentId, 'schedules').pipe(map((cfg) => {
      const { week, exceptions } = cfg.schedule;
      const days: WeekScheduleDay[] = Array.from({ length: 7 }, (_, i) => {
        const date = addDays(weekStart, i);
        const template = week[String(weekday(date))] ?? [];
        const actual = exceptions[date];
        const actualKeys = new Set((actual ?? template).map(slotKey));
        const sessions = template.map((s) => ({ id: slotKey(s), start: s.start, end: s.end, cancelled: actual ? !actualKeys.has(slotKey(s)) : undefined }));
        for (const s of actual ?? []) if (!template.some((t) => slotKey(t) === slotKey(s))) sessions.push({ id: slotKey(s), start: s.start, end: s.end, cancelled: undefined });
        sessions.sort((a, b) => a.start.localeCompare(b.start));
        const genders = new Set((actual ?? template).map((s) => s.gender));
        const dayType = genders.size === 1 ? (genders.has('male') ? 'Мужской' : genders.has('female') ? 'Женский' : undefined) : undefined;
        return { date, dayType, sessions };
      });
      const list: ScheduleException[] = Object.entries(exceptions)
        .filter(([date]) => date >= todayEkb())
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([date, slots]) => {
          const template = week[String(weekday(date))] ?? [];
          if (!slots.length) return { date, text: 'Отделение закрыто' };
          const cancelled = template.filter((t) => !slots.some((s) => slotKey(s) === slotKey(t)));
          const added = slots.filter((s) => !template.some((t) => slotKey(t) === slotKey(s)));
          const parts = [...cancelled.map((s) => `отменён ${s.start}–${s.end}`), ...added.map((s) => `добавлен ${s.start}–${s.end}`)];
          return { date, text: parts.length ? parts.join(', ').replace(/^./, (c) => c.toUpperCase()) : 'График как обычно' };
        });
      return { days, exceptions: list };
    }));
  }

  adminCancelSession(token: string, departmentId: string, date: string, sessionId: string, idempotencyKey: string): Observable<void> {
    return this.adminConfig(token, departmentId, 'schedules').pipe(switchMap((cfg) => {
      const s = cfg.schedule;
      const base = s.exceptions[date] ?? s.week[String(weekday(date))] ?? [];
      const next = base.filter((x) => slotKey(x) !== sessionId);
      // Опубликованный график меняется целиком с проверкой версии; конфликт с заказами вернёт сервер.
      return this.send<unknown>('PUT', `/admin/schedules/${encodeURIComponent(departmentId)}`, {
        version: cfg.schedule_version,
        schedule: { ...s, exceptions: { ...s.exceptions, [date]: next } },
      }, { token, key: idempotencyKey });
    }), map(() => { this.invalidate(`cfg:${departmentId}`); this.invalidate(`ctx:${departmentId}`); }));
  }

  adminTariffs(token: string, departmentId: string): Observable<TariffRule[]> {
    return this.adminConfig(token, departmentId, 'tariffs').pipe(map((cfg) => tariffRules(cfg)));
  }

  adminSaveTariff(token: string, rule: Omit<TariffRule, 'id'>, idempotencyKey: string): Observable<TariffRule> {
    return this.adminConfig(token, rule.departmentId, 'tariffs').pipe(switchMap((cfg) => {
      const plan = planTariff(cfg, rule);
      if ('error' in plan) return throwError(() => ({ code: 'validation', message: plan.error } satisfies ApiError));
      return this.send<unknown>('PUT', `/admin/tariffs/${encodeURIComponent(rule.departmentId)}`, {
        version: cfg.tariff_version, tariffs: { rules: plan.rules },
      }, { token, key: idempotencyKey }).pipe(map((): TariffRule => {
        this.invalidate(`cfg:${rule.departmentId}`);
        this.invalidate(`ctx:${rule.departmentId}`);
        return { ...rule, id: plan.highlight };
      }));
    }));
  }

  adminSeatStates(token: string, departmentId: string, date: string, sessionId: string): Observable<{ scheme: { version: string; zones: { name: string; seats: AdminSeatState[] }[] } }> {
    return this.getSessionAvailability(departmentId, date, sessionId).pipe(map((a) => ({
      scheme: {
        version: a.scheme.version,
        zones: a.scheme.zones.map((z) => ({
          name: z.name,
          seats: z.seats.map((label): AdminSeatState => {
            const st = a.seats[label];
            return { label, state: st === 'free' ? 'free' : st === 'blocked' ? 'blocked' : 'sold' };
          }),
        })),
      },
    })));
  }

  adminSetBlock(token: string, departmentId: string, date: string, sessionId: string, seats: string[], block: boolean, reason: string, idempotencyKey: string): Observable<void> {
    const [starts_at, ends_at] = sessionId.split('|');
    return this.layout(departmentId).pipe(switchMap((layout) => {
      const ids = seats.map((l) => layout.resources.find((r) => r.label === l)?.id).filter((x): x is string => !!x);
      if (block) {
        return this.send<WBlock>('POST', '/admin/resource-blocks', {
          department_id: departmentId, resource_ids: ids, starts_at, ends_at, reason: reason || 'Закрыто администратором',
        }, { token, key: idempotencyKey }).pipe(map(() => undefined));
      }
      // Снятие: удаляем действующие блокировки этих мест, пересекающие сеанс.
      return this.get<WList<WBlock>>('/admin/resource-blocks', { department_id: departmentId, active: true, from: starts_at, until: ends_at, limit: 100 }, token).pipe(
        switchMap((r) => {
          // Сервер отдаёт время в UTC (…Z), поэтому сравниваем моменты, а не строки.
          const [s0, e0] = [Date.parse(starts_at), Date.parse(ends_at)];
          const hits = r.items.filter((b) => b.active && b.resource_ids.some((x) => ids.includes(x)) && Date.parse(b.starts_at) < e0 && s0 < Date.parse(b.ends_at));
          if (!hits.length) return throwError(() => ({ code: 'not_found', message: 'Для выбранных мест нет действующей блокировки.' } satisfies ApiError));
          return forkJoin(hits.map((b) => this.send<WBlock>('DELETE', `/admin/resource-blocks/${b.id}?version=${b.version}`, undefined, { token })));
        }),
        map(() => undefined),
      );
    }));
  }
}

/* ---------- Преобразования ---------- */

function toBanya(b: WBath): Banya {
  return { id: String(b.id), name: b.name, address: b.address, shortAddress: b.address.replace(/^[^,]*Екатеринбург,\s*/, ''), description: b.description };
}

function toDepartment(d: WDepartment, cfg?: WConfig, layout?: WLayout): Department {
  const today = todayEkb();
  const rules = (cfg?.tariffs.rules ?? []).filter((r) => activeOn(r, today));
  const prices = rules.map((r) => (d.mode === 'sauna' ? r.hourly_kopecks : r.adult_kopecks)).filter((p) => p > 0);
  const genders = new Set(Object.values(cfg?.schedule.week ?? {}).flat().map((s) => s.gender));
  return {
    id: d.id,
    banyaId: String(d.bath_id),
    name: d.name,
    mode: d.mode,
    description: d.description,
    tags: [],
    gender: genders.size === 1 && genders.has('male') ? 'male' : genders.size === 1 && genders.has('female') ? 'female' : undefined,
    priceFrom: prices.length ? Math.min(...prices) : undefined,
    baseGuests: d.mode === 'sauna' ? cfg?.base_guests ?? null : undefined,
    maxGuests: d.mode === 'sauna' ? cfg?.max_guests ?? null : undefined,
    rules: [],
    seatsTotal: layout ? layout.resources.filter((r) => r.active !== false).length : undefined,
  };
}

function scheduleRows(cfg: WConfig): { days: string; value: string }[] {
  const text = (wd: number) => {
    const slots = cfg.schedule.week[String(wd)] ?? [];
    return slots.length ? slots.map((s) => `${s.start}–${s.end}`).join(', ') : 'Выходной';
  };
  const rows: { from: number; to: number; value: string }[] = [];
  WEEK_ORDER.forEach((wd, i) => {
    const v = text(wd);
    const last = rows[rows.length - 1];
    if (last && last.value === v && last.to === i - 1) last.to = i;
    else rows.push({ from: i, to: i, value: v });
  });
  const label = (i: number) => WD_SHORT[WEEK_ORDER[i]];
  return rows.map((r) => ({ days: r.from === r.to ? label(r.from) : `${label(r.from)}–${label(r.to)}`, value: r.value }));
}

function tariffRows(cfg: WConfig): TariffLine[] {
  const today = todayEkb();
  const rules = cfg.tariffs.rules.filter((r) => activeOn(r, today));
  const cell = (f: PriceField, dt: WDayType, filter: (r: WTariff) => boolean) => {
    const list = rules.filter((r) => r.day_types.includes(dt) && filter(r)).sort((a, b) => a.start_minute - b.start_minute);
    if (!list.length) return '—';
    const uniq = new Set(list.map((r) => r[f]));
    if (uniq.size === 1) return formatMoney(list[0][f]);
    return list.map((r) => `${formatMoney(r[f])} ${windowText(r)}`.trim()).join(' · ');
  };
  const rows: TariffLine[] = [];
  if (cfg.mode === 'sauna') {
    for (const f of fieldsFor('sauna')) {
      if (!rules.some((r) => r[f] > 0)) continue;
      rows.push({ label: fieldLabel(f, cfg.base_guests), weekday: cell(f, 'weekday', () => true), weekend: cell(f, 'weekend', () => true) });
    }
    return rows;
  }
  const durations = [...new Set(rules.map((r) => r.duration_minutes))].sort((a, b) => a - b);
  for (const f of fieldsFor('session')) {
    for (const dur of durations) {
      const filter = (r: WTariff) => r.duration_minutes === dur;
      const label = durations.length > 1 && dur ? `${fieldLabel(f, 0)} · ${durationText(dur)}` : fieldLabel(f, 0);
      rows.push({ label, weekday: cell(f, 'weekday', filter), weekend: cell(f, 'weekend', filter) });
    }
  }
  return rows;
}

function toQuote(q: WQuote): Quote {
  return {
    lines: q.lines.map((l) => ({
      label: l.count > 1 ? `${l.description} × ${l.count}` : l.description,
      amount: l.denominator ? Math.round(l.numerator / l.denominator) : l.rate_kopecks * l.count,
    })),
    total: q.amount_kopecks,
    tariffName: '',
  };
}

function toSelection(q: { starts_at: string; ends_at: string; resources: { label: string }[] }, c: DeptCtx): Selection {
  const s = ekb(q.starts_at), e = ekb(q.ends_at);
  const base = { banyaId: c.banya.id, departmentId: c.dept.id, date: s.date, start: s.time, end: e.time };
  if (c.dept.mode === 'sauna') {
    return { kind: 'sauna', ...base, hours: Math.round((Date.parse(q.ends_at) - Date.parse(q.starts_at)) / 3_600_000) };
  }
  return { kind: 'session', ...base, sessionId: `${q.starts_at}|${q.ends_at}`, seats: sortSeats(q.resources.map((r) => r.label)) };
}

function toOrderView(o: WOrder, c: DeptCtx, change: PriceChange | undefined, status?: OrderStatus): OrderView {
  const visit = { starts_at: o.selection.starts_at, ends_at: o.selection.ends_at, resources: o.quote.resources };
  // Срок удержания пересчитываем по часам сервера: часы устройства могут спешить или отставать.
  const skew = Date.now() - Date.parse(o.server_time);
  const ticket = o.ticket_url ? decodeURIComponent(o.ticket_url.replace(/\/+$/, '').split('/').pop() ?? '') : undefined;
  return {
    id: o.id,
    status: status ?? publicStatus(o.status),
    banya: c.banya,
    department: c.department,
    selection: toSelection(visit, c),
    guests: fromWireGuests(o.selection.guests),
    buyer: { name: o.buyer.name ?? '', phone: o.buyer.phone ?? '' },
    quote: toQuote({ ...o.quote, amount_kopecks: o.amount_kopecks }),
    previousTotal: change && change.previous !== o.amount_kopecks ? change.previous : undefined,
    newTotalConfirmed: change?.confirmed,
    holdExpiresAt: o.expires_at ? new Date(Date.parse(o.expires_at) + skew).toISOString() : undefined,
    paidAt: o.confirmed_at,
    createdAt: o.created_at,
    ticketToken: ticket || undefined,
    history: [],
  };
}

function publicStatus(s: WOrder['status']): OrderStatus { return s; }

function adminStatus(o: WOrder): OrderStatus {
  if (o.status === 'held' && o.payment?.status === 'declined') return 'declined';
  if (o.status === 'held' && o.payment?.status === 'pending') return 'pending';
  return publicStatus(o.status);
}

function historyText(h: WHistoryEntry): string {
  const known: Record<string, string> = {
    'order.created': 'Создан черновик заказа',
    'order.updated': 'Черновик изменён',
    'order.held': 'Выбор удержан на 10 минут',
    'order.released': 'Удержание снято покупателем',
    'order.expired': 'Удержание истекло',
    'order.confirmed': 'Заказ подтверждён, билет выпущен',
    'payment.created': 'Создана попытка тестовой оплаты',
    'payment.accepted': 'Тестовая оплата принята',
    'payment.declined': 'Тестовая оплата отклонена',
  };
  return known[h.action] ?? h.action;
}

/* ---------- Админка: тарифы ---------- */

function tariffRules(cfg: WConfig): TariffRule[] {
  const res: TariffRule[] = [];
  for (const r of [...cfg.tariffs.rules].sort((a, b) => a.valid_from.localeCompare(b.valid_from) || a.start_minute - b.start_minute)) {
    for (const f of fieldsFor(cfg.mode)) {
      if (f === 'child_kopecks' && !r.child_kopecks && !r.adult_kopecks) continue;
      if (f === 'extra_hourly_kopecks' && !r.extra_hourly_kopecks && !r.hourly_kopecks) continue;
      res.push({
        id: `${r.id}|${f}`,
        departmentId: cfg.id,
        condition: conditionOf(r, f, cfg.mode, cfg.base_guests),
        days: daysText(r.day_types),
        time: windowText(r) || 'Весь день',
        from: r.valid_from,
        to: r.valid_until >= OPEN_END ? undefined : addDays(r.valid_until, -1),
        price: r[f],
        unit: cfg.mode === 'sauna' ? 'час' : 'место',
      });
    }
  }
  return res;
}

/**
 * Новое правило по образцу действующих: разрезает пересекающиеся правила той же области
 * (длительность и время суток) по периоду и дням, в новых кусках меняет одну цену.
 */
function planTariff(cfg: WConfig, input: Omit<TariffRule, 'id'>): { rules: WTariff[]; highlight: string } | { error: string } {
  const days = DAY_SETS[input.days];
  if (!days) return { error: 'Выберите дни действия правила.' };
  const from = input.from;
  const until = input.to ? addDays(input.to, 1) : OPEN_END;
  if (until <= from) return { error: 'Дата окончания раньше даты начала.' };
  if (from <= todayEkb()) return { error: 'Новое правило можно начать не раньше завтрашнего дня.' };

  let field: PriceField | null = null;
  let scope: WTariff | null = null;
  for (const r of cfg.tariffs.rules) for (const f of fieldsFor(cfg.mode)) {
    if (!field && conditionOf(r, f, cfg.mode, cfg.base_guests) === input.condition) { field = f; scope = r; }
  }
  if (!field || !scope) return { error: 'Условие не найдено в действующих правилах.' };
  const f = field, sc = scope;
  const sameScope = (r: WTariff) => r.duration_minutes === sc.duration_minutes && r.start_minute === sc.start_minute && r.end_minute === sc.end_minute;

  const out: WTariff[] = [];
  let highlight = '';
  let touched = 0;
  const nid = () => `rule-${uuidv4().slice(0, 8)}`;
  for (const r of cfg.tariffs.rules) {
    const inter = r.day_types.filter((d) => days.includes(d));
    const overlaps = sameScope(r) && inter.length > 0 && r.valid_from < until && from < r.valid_until;
    if (!overlaps) { out.push(r); continue; }
    touched++;
    const rest = r.day_types.filter((d) => !days.includes(d));
    if (rest.length) out.push({ ...r, day_types: rest });
    if (r.valid_from < from) out.push({ ...r, id: rest.length ? nid() : r.id, day_types: inter, valid_until: from });
    if (until < r.valid_until) out.push({ ...r, id: nid(), day_types: inter, valid_from: until });
    const piece: WTariff = { ...r, id: nid(), day_types: inter, valid_from: r.valid_from > from ? r.valid_from : from, valid_until: r.valid_until < until ? r.valid_until : until, [f]: input.price };
    out.push(piece);
    if (!highlight) highlight = `${piece.id}|${f}`;
  }
  if (!touched) return { error: 'В выбранный период и дни нет действующего правила с этим условием — его нечем заменить.' };
  if (out.length > 200) return { error: 'Слишком много правил у отделения (максимум 200).' };
  return { rules: out, highlight };
}
