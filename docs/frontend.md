# 前端开发与说明

前端位于 `web/`，基于 Next.js 静态导出构建，并由 `deployd` 在同源 `/` 托管（无跨域、无需 CORS）。

## 构建与本地调试

```sh
# 生产静态导出（输出到 web/out）
cd web && pnpm install && pnpm build

# 本地开发（将 /api/* 与 /healthz 代理到后端，支持热更新）
cd web && DEV_API_ORIGIN=http://127.0.0.1:8790 pnpm dev
```

- 在 `config.yaml` 设置 `web_dir: web/out`（相对路径基于配置文件目录；留空则仅运行纯 API 模式）。
- `/api/*`、`/healthz` 与 `/webhook/github/*` 路由始终优先于静态资源。
- 修改前端后需重新运行 `pnpm build` 并重启 `deployd`。

## 页面与交互

- `/`：站点总览、最近部署状态分布与近期任务列表。
- `/site?name=<site>`：单站详情、状态筛选分页历史、接入信息与管理员手动部署入口。
- `/deploy?id=<id>`：任务时间线、请求/检出提交对比、日志尾部（运行中每 3 秒轮询刷新）与管理员重新部署。

## 登录与语义边界

- 凭 token 登录（校验 `GET /api/sites`），默认存于 `sessionStorage`，勾选保持登录时存于 `localStorage`。
- 登录页支持使用固定 token `demo` 查看内置演示数据，不请求后端。
- 界面展示遵循后端语义：`succeeded` 仅代表脚本退出 `0`；“重新部署”会发起全新任务，而非回滚操作。
