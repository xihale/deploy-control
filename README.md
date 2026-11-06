# deploy-control

Go 部署控制服务：GitHub push 验签、SQLite 持久队列、每站串行/全局限并发、统一 Git 拉取、执行站点部署脚本和查询日志。前端接口见 [FRONTEND.md](FRONTEND.md)，gx 切换前必读 [MIGRATION.md](MIGRATION.md)。

## 启动

需要 Go、Git 和可执行的站点脚本。复制 `config.example.yaml` 为 `config.yaml`，修改仓库、分支、脚本绝对路径与密钥。配置含 webhook secret，应仅授权服务用户读取（例如 `chmod 600 config.yaml`），不要提交到版本库。生成足够长的随机 API token，配置其 SHA-256 摘要：

```sh
openssl rand -hex 32
printf '%s' '将上面生成的 token 放在这里' | sha256sum
```

将输出摘要填入用户的 `token_hash`，实际 token 保存到安全位置供客户端使用。单机开发时不填 `run_as`，脚本以服务当前用户运行。

```sh
go build -o deployd ./cmd/deployd
./deployd -config config.yaml
curl http://127.0.0.1:8080/healthz
```

默认仅绑定回环地址。外部访问需要经过受保护的 HTTPS 入口；不要将无 TLS 的服务直接暴露到公网。GitHub webhook 需要可达的 HTTPS 地址，其访问规则须与管理 API 分开配置。配置文件、数据库及日志可能包含敏感信息，按部署环境限制目录访问并备份 SQLite 数据（含 WAL）。

## 前端

控制台前端在 `web/`（Next.js 静态导出），由本服务同源提供，因此浏览器不需要 CORS，token 也不跨源。

```sh
cd web && pnpm install && pnpm build   # 输出到 web/out
```

在 `config.yaml` 中设置 `web_dir: web/out`（相对路径以配置文件所在目录为准；留空则只提供 API）。服务启动时校验 `web/out/index.html` 是否存在，缺失会直接报错退出。`/api/*`、`/healthz` 和 `/webhook/github/*` 始终优先于静态文件。

前端改动后需要重新执行 `pnpm build` 并重启服务；静态文件在启动时挂载，不做热更新。

开发调试时不要走静态导出，用 dev server 转发（同源，无需 CORS）：

```sh
cd web
DEV_API_ORIGIN=http://127.0.0.1:8790 pnpm dev   # http://localhost:3000
```

设置 `DEV_API_ORIGIN` 后，dev server 把 `/api/*` 和 `/healthz` 转发到该地址，页面照常热更新；token 直接填后端配置里的。不设置该变量时保持静态导出模式，`pnpm build` 产物不受影响。

## 站点脚本契约

服务负责获取仓库和检出提交。每个站点的 `script` 必须由管理员审核，只负责检查、构建和发布，退出 0 表示发布步骤成功；不应自行 fetch、checkout、重新入队或抢旧锁。服务会以该站工作树作为当前目录，并传入：

- `DEPLOY_SITE`：站点名。
- `DEPLOY_BRANCH`：目标分支。
- `DEPLOY_COMMIT`：已检出的完整提交。
- `DEPLOY_JOB_ID`：任务 ID。
- `DEPLOY_FETCH_PROXY`：配置的 fetch 代理地址，可能为空。

脚本可以执行工作树中的项目代码，因此应按不可信代码对待。输出写入任务日志；避免输出密钥。非零退出会标记失败，但失败后线上内容是否发生了部分变更，取决于各站实际发布过程。


正式部署应使用 systemd 的 `KillMode=control-group`，确保服务被强杀后其构建子进程也被清理，再自动重启服务；只恢复数据库中的任务状态并不能清理遗留进程。同一数据库只能由一个 deployd 实例使用。
配置 `run_as` 时，Git 操作和脚本通过 `sudo -n -H -u <用户>` 执行。必须预先建立受限制的服务身份、工作目录访问权限、目标用户的 Git 凭据和可用的 sudo 授权；不要将本服务作为任意命令的 root 代理。服务会为每站创建独立工作树并使用该用户权限。具体 sudo 规则必须按安装路径和实际调用审查验证后再配置。

## 网络策略与状态

HTTPS 仓库首次尝试直连，后续最多四次使用配置的代理；SSH 使用直连重试。其他脚本自行发起的网络请求不受此策略约束。任务在服务重启时不会假装成功：此前运行中的任务标记为失败，已排队任务仍可被调度。后台记录的 `checked_sha` 是实际检出版本；发布是否真正成功仍依赖受审查的站点脚本。

Git 支持按 URL 指定代理，其优先级可能高于全局 `http.proxy`。部署前须检查各 CI 用户和工作树的 Git 配置，移除与统一策略冲突的按 URL 代理项；否则无法保证连接经过预期路径。

当前 gx 的九个旧脚本还未迁移，不应直接把它们配置进新服务：它们有自己的锁、fetch、重跑和 SHA 处理，冲突时可能返回 0。迁移需要逐站排空旧部署并改造脚本，详见迁移文档。
