FROM node:24-alpine AS build
RUN corepack enable && corepack prepare pnpm@10.24.0 --activate
WORKDIR /src
COPY package.json pnpm-workspace.yaml pnpm-lock.yaml ./
COPY apps/web/package.json apps/web/package.json
COPY apps/live/package.json apps/live/package.json
COPY packages/editor-schema/package.json packages/editor-schema/package.json
RUN pnpm install --frozen-lockfile
COPY packages/editor-schema/ packages/editor-schema/
COPY apps/web/ apps/web/
RUN pnpm --filter @myjira/editor-schema build && pnpm --filter @my-jira/web build

FROM caddy:2-alpine
COPY infra/Caddyfile /etc/caddy/Caddyfile
COPY --from=build /src/apps/web/dist/ /srv/
EXPOSE 80
