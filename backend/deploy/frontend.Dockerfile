# syntax=docker/dockerfile:1
# Build context: repository root.
FROM node:24.13.1-alpine AS build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY frontend/ ./
ENV NG_BUILD_MAX_WORKERS=2 NODE_OPTIONS=--max-old-space-size=2048
RUN npm run build -- --configuration production

FROM nginx:1.28.0-alpine
COPY backend/deploy/nginx/frontend.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist/frontend/browser/ /usr/share/nginx/html/
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
