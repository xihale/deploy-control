# deploy-control

自托管多站点部署控制台：Go 后端（GitHub Webhook 验签、SQLite 持久队列、并发调度）+ Next.js 静态导出前端，单二进制同源托管，无外部依赖。

- **触发与去重**：支持 GitHub `push`（HMAC-SHA256 验签、delivery 去重）与 API/网页手动触发。
- **调度与持久化**：同站串行、全局有限并发；任务状态与日志落盘，重启不假装成功。
- **统一拉取**：首次强制直连，HTTPS 重试走配置代理，SSH 始终直连；`checked_sha` 记录实际检出版本。
- **同源托管**：通过 `web_dir` 挂载前端静态产物，留空则纯 API 模式；支持自举部署。

## 快速开始

需要 Go、Git 及可执行的站点部署脚本（构建前端另需 Node / pnpm）。

1. **准备配置与 Token**

   复制 [config.example.yaml](config.example.yaml) 为 `config.yaml`（建议 `chmod 600 config.yaml`），生成随机 token 并将 SHA-256 摘要填入 `users[].token_hash`：

   ```sh
   openssl rand -hex 32
   printf '%s' '<你的明文 token>' | sha256sum
   ```

2. **构建前端（可选，纯 API 模式可跳过）**

   ```sh
   cd web && pnpm install && pnpm build   # 生成 web/out
   ```

3. **编译并启动**

   ```sh
   go build -o deployd ./cmd/deployd
   ./deployd -config config.yaml
   curl http://127.0.0.1:8080/healthz
   ```

## 文档

- [HTTP API 与 Webhook](docs/api.md)：认证、接口列表、任务状态与 Webhook 验签。
- [前端开发与说明](docs/frontend.md)：静态导出、`DEV_API_ORIGIN` 本地代理联调与页面说明。
- [站点脚本契约与生产部署](docs/deploy.md)：脚本环境变量、Git 拉取策略、`run_as` 隔离与 systemd 配置。
- [ops/gx/](ops/gx/)：生产环境配置快照（Caddy、systemd、自举配方 [deploy-control](ops/gx/scripts/sites.d/deploy-control)），详见 [迁移记录](ops/gx/MIGRATION.md) 与 [配方格式](ops/gx/scripts/sites.d/README.md)。

## License

[MIT](LICENSE)
