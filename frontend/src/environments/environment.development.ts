/**
 * Локальная разработка: бекенд из backend/ в Docker (docker compose up), шлюз на :8080.
 * Запросы /api проксирует dev-сервер (proxy.conf.json), поэтому CORS и порт фронтенда не важны.
 */
export const environment = {
  api: 'http' as 'http' | 'demo',
  apiBaseUrl: '/api/v1',
};
