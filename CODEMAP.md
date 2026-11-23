# deploy-control Codemap（Cold-Start 速查）

> **检索策略**：语义查询先调 `csearch` MCP `search_code`；精确符号用 `rg -n` 后再区间 `read_file`（<= 120 行）。已配 `.rgignore` / `.csearchignore` 过滤 `deployd` 二进制、`web/out/`、`.next/`、`node_modules/` 与锁文件。

## 1. Go 后端 (`cmd/deployd/` & `internal/`)

| 模块 / 路径 | 核心职责 |
|---|---|
| `cmd/deployd/main.go` | 服务入口（`-config`）、SQLite 文件锁（`flock`）、路由挂载（`/webhook/github/`、`/api/`、`/healthz`、`/`） |
| `internal/config/config.go` | YAML 配置解析与校验（对照模板 `config.example.yaml`） |
| `internal/store/store.go` | SQLite（`modernc.org/sqlite`）任务/delivery 持久化与恢复 |
| `internal/analytics/{parser,store,ingestor}.go` | Caddy JSON 访问日志增量采集、隐私去重 DAU/WAU/MAU 聚合（`analytics.db`）与维度查询 |
| `internal/dispatch/dispatch.go` | 同站串行、全局有限并发调度队列 |
| `internal/fetcher/{fetcher,git}.go` | 5 次统一拉取策略（首次强直连，HTTPS 重试走代理，SSH 永直连） |
| `internal/runner/{runner,exec}.go` | 站点脚本执行（注入 `DEPLOY_*` 环境变量、可选 `run_as` `sudo -n -H -u` 与日志落盘） |
| `internal/webhook/github.go` | GitHub `push` / `release` Webhook HMAC-SHA256 验签与 delivery 去重 |
| `internal/api/api.go` & `internal/webui/webui.go` | Token 鉴权 REST API（含 `/api/analytics/*`）与 `web_dir` 静态产物同源托管 |

## 2. Next.js 静态前端 (`web/`) 与运维 (`ops/gx/`)

| 目录 / 文件 | 核心内容 |
|---|---|
| 页面与路由 (`web/app/`) | `layout.tsx` · `globals.css` · `page.tsx`（总览）· `analytics/page.tsx`（访问统计）· `site/page.tsx`（单站）· `deploy/page.tsx`（任务日志） |
| 组件 (`web/components/`) | `app-shell` · `auth-provider` · `overview` · `analytics-view` · `analytics-charts` · `site-view` · `deploy-view` · `log-viewer` · `manual-deploy-dialog` · `status-badge` · `ui/{button,dialog,input,label}` |
| 客户端库 (`web/lib/`, `hooks/`) | `api.ts` · `types.ts` · `status.ts` · `format.ts` · `mock.ts` & `mock-analytics.ts`（`demo` token 离线演示）· `hooks/use-api.ts` |
| 运维配方 (`ops/gx/`) | `deployd.service` · `caddy-deploy.xeed.ink` · `scripts/run-site` · `scripts/lib/common.sh` · `scripts/sites.d/*` |

## 3. Token Guard 与语义约束 (`web/app/globals.css` + `docs/`)

- **样式令牌**：Tailwind v4 `@theme inline` + oklch CSS 变量（`:root` 与 `.dark` custom-variant）；复用 `web/components/ui/`（`@base-ui/react`）与 `web/lib/status.ts`、`web/lib/format.ts`。
- **契约边界**：`succeeded` 仅代表站点脚本退出码 `0`；"重新部署"创建新任务而非回滚；站点脚本禁止自行 `fetch`/`checkout`（见 `docs/deploy.md`）。

## 4. 常用命令

| 场景 | 命令 | 说明 |
|---|---|---|
| Go 单测与编译 | `go test ./...` · `go build -o deployd ./cmd/deployd` | 仓库根执行；测试覆全所有 `internal/*` 包 |
| 本地启动服务 | `./deployd -config config.yaml` | 健康检查 `GET /healthz` |
| 前端类型检查与导出 | `cd web && pnpm typecheck && pnpm build` | `tsc --noEmit` + Next 静态导出到 `web/out/` |
| 前端联调开发 | `cd web && DEV_API_ORIGIN=http://127.0.0.1:8790 pnpm dev` | 代理 `/api/*` 与 `/healthz` 到后端 |
