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

## 切换要求

1. 先备份并验证旧接收器、sudo 白名单、Caddy、站点配置和部署脚本的恢复路径。新服务的密钥单独存储，不能进入仓库或日志。
2. 每次只迁移一个站点。暂停该站点的旧入口并排空在途部署，确认锁未被占用且没有遗留 rerun；不要删除或替换锁文件。新旧执行路径不得并发写入同一发布目录。
3. 改造站点脚本，使共享执行器独占任务调度和 fetch，脚本只执行该站的检查、构建及发布。保留现有构建命令、发布目录、attic、缓存与健康检查，不套用单一 rsync 模板。保留必要的 Git LFS 处理。
4. 校验一次真实 GitHub push 的签名、分支、请求 SHA、实际构建 SHA、任务最终状态、日志和线上版本；再验证失败与重复投递。HTTP 接收成功不代表发布成功。
5. 首次 HTTPS 拉取必须强制直连；失败后才使用 snirect。SSH remote 的代理能力尚未验证，应单独处理，不能依赖 HTTPS_PROXY。
6. 发布不是普遍原子操作：某些站点先写 deploy-meta 再完成资源同步。要让 meta 证明发布完成，须逐站调整写入顺序并验证；不能单独依赖旧 meta 判断成功。
7. 为回滚提供已验证的恢复方式。现有部分站点有 attic，它用于保留旧资源，不等同于完整可回滚版本。

上述站点清单来自 gx 的只读调查；实际切换前需要重新核对实时配置。
