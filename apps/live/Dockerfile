FROM node:24-bookworm-slim AS build
RUN corepack enable && corepack prepare pnpm@10.24.0 --activate
WORKDIR /src
COPY package.json pnpm-workspace.yaml pnpm-lock.yaml ./
COPY apps/web/package.json apps/web/package.json
COPY apps/live/package.json apps/live/package.json
COPY packages/editor-schema/package.json packages/editor-schema/package.json
RUN pnpm install --frozen-lockfile
COPY packages/editor-schema/ packages/editor-schema/
COPY apps/live/ apps/live/
RUN pnpm --filter @myjira/editor-schema build && pnpm --filter @myjira/live build

FROM node:24-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends chromium fonts-noto-cjk ca-certificates \
 && rm -r /var/lib/apt/lists/*
RUN apt-get -o Acquire::http::Timeout=30 update && apt-get -o Acquire::http::Timeout=30 install -y --no-install-recommends fonts-noto-color-emoji \
 && rm -r /var/lib/apt/lists/*
COPY --from=build --chown=node:node /src /app
WORKDIR /app/apps/live
ENV NODE_ENV=production CHROMIUM_PATH=/usr/bin/chromium LIVE_ADDR=0.0.0.0 LIVE_PORT=3101
USER node
EXPOSE 3101
CMD ["node", "dist/server.js"]
