import { ApplicationConfig, LOCALE_ID, provideBrowserGlobalErrorListeners } from '@angular/core';
import { provideRouter, withComponentInputBinding, withInMemoryScrolling } from '@angular/router';
import { registerLocaleData } from '@angular/common';
import { provideHttpClient, withFetch } from '@angular/common/http';
import localeRu from '@angular/common/locales/ru';

import { routes } from './app.routes';
import { BookingApi } from './core/api/booking-api';
import { DemoBookingApi } from './core/api/demo-booking-api';
import { HttpBookingApi } from './core/api/http-booking-api';
import { environment } from '../environments/environment';

registerLocaleData(localeRu);

export const appConfig: ApplicationConfig = {
  providers: [
    provideBrowserGlobalErrorListeners(),
    provideRouter(
      routes,
      withComponentInputBinding(),
      withInMemoryScrolling({ anchorScrolling: 'enabled', scrollPositionRestoration: 'top' }),
    ),
    { provide: LOCALE_ID, useValue: 'ru' },
    provideHttpClient(withFetch()),
    // Источник данных выбирается в src/environments: бекенд MVP или демо-данные в браузере.
    { provide: BookingApi, useClass: environment.api === 'demo' ? DemoBookingApi : HttpBookingApi },
  ],
};
