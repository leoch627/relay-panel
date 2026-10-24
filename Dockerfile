# ---------- 前端：React + shadcn/ui ----------
FROM node:20-alpine AS fe
WORKDIR /build/panel/frontend
COPY panel/frontend/package.json panel/frontend/package-lock.json* panel/frontend/.npmrc ./
RUN npm ci --no-audit --no-fund || npm install --no-audit --no-fund
COPY panel/frontend/ ./
# vite outDir=../web → /build/panel/web
RUN npm run build

# ---------- 后端：Go 静态二进制（embed 前端产物） ----------
FROM golang:1.25-alpine AS be
WORKDIR /src
COPY panel/go.mod panel/go.sum ./panel/
RUN cd panel && go mod download
COPY . .
COPY --from=fe /build/panel/web ./panel/web
RUN cd panel && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/relay-panel .

# ---------- 运行镜像 ----------
# 单端口复用：页面 + 面板 API + 节点轮询 API 全部走 PANEL_ADDR（默认 :8081）
# 反代/Cloudflare：默认 PANEL_TLS=0 由反代终止 TLS；裸跑直连改 PANEL_TLS=1（自动自签）
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=be /out/relay-panel /usr/local/bin/relay-panel
VOLUME /etc/relay-panel
EXPOSE 8081
ENV PANEL_ADDR=:8081 PANEL_TLS=0
ENTRYPOINT ["relay-panel"]
