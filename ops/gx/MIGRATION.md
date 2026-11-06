# gx 迁移约束

本项目不会自动接管 gx 上的生产部署。切换之前必须先在隔离环境验证，并逐站迁移；旧链路在新链路验证完成前保持可用。

## 当前现状

九站都使用各自 CI 用户及各自仓库中的 `scripts/server/deploy.sh`。所有脚本使用 `/home/<ci>/.deploy.lock` 与 `.deploy-rerun`，锁竞争时写 rerun 标志并返回 0；因此单靠旧脚本的退出码不能证明这次任务已经发布。旧脚本自身还会 fetch、checkout，部分会 re-exec 和重跑。新调度器不能简单包裹旧脚本后声称已实现精确的提交控制、统一 fetch 或可靠的任务状态。

现有共享 runner 导出 `ZP_PUSH_SHA`，而 blog、poetry、schedmate、ziglings 分别读取 `BLOG_PUSH_SHA`、`POETRY_PUSH_SHA`、`SC_PUSH_SHA`、`ZL_PUSH_SHA`。blog 和 poetry 因此未收到推送 SHA。迁移时须统一契约，不能把字面量 `unknown` 当作有效提交。

| 站点 | CI 用户 | 分支 | 特殊事项 |
| --- | --- | --- | --- |
| blog | blog-ci | astro | Astro、`_astro` attic；当前脚本独有 HTTPS 代理重试 |
| zig-playground | zig-ci | master | 静态产物与 assets attic |
| poetry | poetry-ci | main | Nuxt 产物及多组 attic |
| schedmate | sched-ci | main | 静态产物与 assets attic |
| ziglings-web | ziglings-ci | main | 静态产物全量发布；旧脚本不自更新 |
| foyer | foyer-ci | main | Go 二进制、systemd user service、健康检查与回滚 |
| me | me-ci | main | SSH remote；Astro 与 attic |
| light-cathedral | lc-ci | main | 源码树 rsync |
| sonny-boy | sonny-boy-ci | main | SSH remote、Git LFS、源码树 rsync |

## 迁移状态（2026-11-06）

九站已全部切到新链路（deployd + `/var/lib/deployd/scripts/*.sh`，各站 CI 用户与 per-site webhook secret）。GitHub 每个仓库的 webhook 指向 `https://deploy.xeed.ink/webhook/github/<site>`，旧链路口径（`*/hooks/*`）已不再被引用。

验证证据（均为真实执行结果，非推断）：

- 签名 ping 全部 200；每个仓库真实推送一个**空提交**（内容不变）触发 push 部署，job 17–26 全部 `succeeded`，`checked_sha` 等于推送的提交，九站首页 200。
- 分站手动部署（`POST /api/deploys`）同样全部成功，日志含 clone/fetch 每次 attempt、脚本输出与退出码。
- 传输实测：gx→GitHub 吞吐约 27 KB/s，`TLS 握手`经常被黑洞（直连 attempt 60 s 超时），代理 attempt 通常 <1 s 成功；因此 clone 的 attempt 超时独立为 10 分钟（fetch 保持 60 s），clone 失败不留下半成品目录，且每次 attempt 前重置 staging。
- poetry（58 MB）未走首次 clone：从旧链 clone 播种工作树后只验证了 fetch 增量路径；其余八站的 clone 路径均为真实执行。
- sonny-boy 仍走 SSH（私有库、Git LFS），代理不覆盖 SSH；`git lfs pull` 在脚本内重试，pointer 检查拦住未 smudge 的指针文件。

未验证 / 风险（不得省略）：

- 切链当日 13:58 Caddy 一次 reload 失败导致重启，窗口内两条 GitHub 投递拿到 502；GitHub 不会自动重投，本次用 API 手动 redeliver 恢复。这类丢投递不会造成错误发布，但会漏一次自动部署，需要面板手动重新部署兜底。
- poetry 的 clone 路径（新工作树）未经实测，仅播种后的 fetch/checkout/script 路径实测。
- SSH 偶发不可达时 sonny-boy 会以 10 分钟/attempt × 次数的方式慢失败，受 `deploy_timeout_sec` 兜底。
- 本轮为验证 webhook 向九仓库各推入一个空提交（`chore: 触发部署验证`）；sonny-boy 首次推送因浅克隆空索引生成了空树提交，已 force-push 恢复到 `f6423837` 并复部署，站点内容 5.7 MB 恢复，无内容损失。

## 切换要求

1. 先备份并验证旧接收器、sudo 白名单、Caddy、站点配置和部署脚本的恢复路径。新服务的密钥单独存储，不能进入仓库或日志。
2. 每次只迁移一个站点。暂停该站点的旧入口并排空在途部署，确认锁未被占用且没有遗留 rerun；不要删除或替换锁文件。新旧执行路径不得并发写入同一发布目录。
3. 改造站点脚本，使共享执行器独占任务调度和 fetch，脚本只执行该站的检查、构建及发布。保留现有构建命令、发布目录、attic、缓存与健康检查，不套用单一 rsync 模板。保留必要的 Git LFS 处理。
4. 校验一次真实 GitHub push 的签名、分支、请求 SHA、实际构建 SHA、任务最终状态、日志和线上版本；再验证失败与重复投递。HTTP 接收成功不代表发布成功。
5. 首次 HTTPS 拉取必须强制直连；失败后才使用 snirect。SSH remote 的代理能力尚未验证，应单独处理，不能依赖 HTTPS_PROXY。
6. 发布不是普遍原子操作：某些站点先写 deploy-meta 再完成资源同步。要让 meta 证明发布完成，须逐站调整写入顺序并验证；不能单独依赖旧 meta 判断成功。
7. 为回滚提供已验证的恢复方式。现有部分站点有 attic，它用于保留旧资源，不等同于完整可回滚版本。

上述站点清单来自 gx 的只读调查；实际切换前需要重新核对实时配置。
