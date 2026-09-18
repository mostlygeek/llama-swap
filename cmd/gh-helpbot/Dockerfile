# gh-helpbot: answers @help questions on GitHub with llama-swap's Help agent.
#
# The build context is the repository root, because the bot shares the Help
# agent's code with the UI:
#
#   docker build -f cmd/gh-helpbot/Dockerfile -t gh-helpbot .
#
# See cmd/gh-helpbot/README.md for how to run it.

FROM node:24-alpine AS build
WORKDIR /src
COPY ui/src/lib ui/src/lib
COPY ui/src/cli/fetchBase.ts ui/src/cli/headless.ts ui/src/cli/
COPY cmd/gh-helpbot cmd/gh-helpbot
WORKDIR /src/cmd/gh-helpbot
RUN npm ci --no-audit --no-fund \
    && npm run check \
    && npm test \
    && npm run build

FROM node:24-alpine
# /data holds the poll state file. The image ships uid 1000 as "node"; alpine
# has no useradd, so that account is used rather than creating one.
RUN mkdir -p /app /data && chown node:node /data
COPY --from=build /src/cmd/gh-helpbot/dist/gh-helpbot.js /app/gh-helpbot.js
USER node
WORKDIR /app
ENV NODE_ENV=production \
    HELPBOT_STATE_FILE=/data/state.json \
    HELPBOT_LISTEN=0.0.0.0:8085
VOLUME /data
EXPOSE 8085
# Only meaningful for `serve`; pass --no-healthcheck to `docker run` for `poll`.
HEALTHCHECK --interval=30s --timeout=3s --start-period=20s \
    CMD wget -qO- http://127.0.0.1:8085/healthz >/dev/null || exit 1
ENTRYPOINT ["node", "/app/gh-helpbot.js"]
CMD ["serve"]
