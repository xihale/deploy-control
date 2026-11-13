# gx 生产迁移记录

## 迁移状态（2026-11-06）

9 个站点已全部切至 `deployd` + 统一配方体系（`/var/lib/deployd/scripts/run-site` + `sites.d/<site>`），Webhook 统一指向 `https://deploy.xeed.ink/webhook/github/<site>`，旧 `*/hooks/*` 入口已下线。

| 站点 | CI 用户 | 分支 | 构建与发布形态 |
| --- | --- | --- | --- |
| `blog` | `blog-ci` | `astro` | Astro + `_astro` attic |
| `zig-playground` | `zig-ci` | `master` | 静态产物 + assets attic + 工具链自助编译 |
| `poetry` | `poetry-ci` | `main` | Nuxt 产物 + 多组 attic |
| `schedmate` | `sched-ci` | `main` | 静态产物 + assets attic |
| `ziglings-web` | `ziglings-ci` | `main` | 静态产物全量发布 |
| `foyer` | `foyer-ci` | `main` | Go 二进制 + systemd user service + 健康检查 |
| `me` | `me-ci` | `main` | SSH remote + Astro + attic |
| `light-cathedral` | `lc-ci` | `main` | 源码树 rsync |
| `sonny-boy` | `sonny-boy-ci` | `main` | SSH remote + Git LFS + 源码树 rsync |

## 迁移契约约束

旧脚本依赖 `.deploy.lock` / `.deploy-rerun`（锁冲突即写标志并返回 `0`）及各站独立的 `*_PUSH_SHA` 变量。迁移后统一由 `deployd` 独占调度与 Git 拉取，站点脚本仅负责检查、构建与发布：

- `runner` 会拦截含 `.deploy.lock`、`.deploy-rerun` 或旧 `*_PUSH_SHA` 标记的脚本（最终合规性仍由管理员审核）。
- 站点脚本接收统一环境变量（`DEPLOY_SITE`、`DEPLOY_BRANCH`、`DEPLOY_COMMIT`、`DEPLOY_JOB_ID`、`DEPLOY_FETCH_PROXY`）。

## 验证结果与已知风险

**已验证**：
- 9 站签名 `ping`、真实 `push` 空提交（job 17–26 全部 `succeeded`，`checked_sha` 匹配）及手动部署均通过，首页返回 `200`。
- 实测 gx→GitHub 直连吞吐约 27 KB/s 且 TLS 握手易超时（60s），代理尝试通常 <1s；因此 `clone` 超时独立设为 10 分钟（`fetch` 保持 60s），且失败不保留半成品目录。

**未验证与风险**：
- `poetry`（58 MB）工作树由旧链播种，仅实测了增量 `fetch` 路径，未测全新 `clone`。
- `sonny-boy` 使用 SSH + Git LFS（代理不覆盖 SSH），SSH 不可达时会按单次 10 分钟慢失败，由 `deploy_timeout_sec` 兜底；脚本内含 `git lfs pull` 重试与指针校验。
- Caddy reload 重启窗口曾导致两条 GitHub 投递返回 `502`（GitHub 不自动重投），需通过面板或 API 手动补发。
- 部分站点存在 `attic` 保留旧资源，但并非完整原子回滚方案。
