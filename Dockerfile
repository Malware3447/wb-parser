# Сборка программы
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -o /wb-parser ./cmd

# В образе CloakBrowser уже лежат сам браузер, Node.js, шрифты и виртуальный экран
FROM cloakhq/cloakbrowser
WORKDIR /app
COPY browser/package.json browser/package-lock.json ./browser/
RUN cd browser && npm ci --omit=dev && npm cache clean --force
COPY browser/fetch.mjs ./browser/
COPY --from=build /wb-parser /usr/local/bin/wb-parser
VOLUME /app/data
EXPOSE 8080
CMD ["wb-parser", "-addr", "0.0.0.0:8080"]
