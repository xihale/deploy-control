# 站点脚本契约与生产部署

## 站点脚本契约

`deployd` 独占仓库拉取（clone/fetch/checkout）与并发队列调度。站点配置的 `script` 必须为绝对路径的可执行文件，仅负责**检查、构建与发布**，禁止自行 `fetch`、`checkout`、抢锁或重入队。

服务以该站工作树为当前目录执行脚本，并注入以下环境变量：

| 变量 | 说明 |
| --- | --- |
| `DEPLOY_SITE` | 站点名 |
| `DEPLOY_BRANCH` | 目标分支 |
| `DEPLOY_COMMIT` | 实际检出的完整提交 SHA |
| `DEPLOY_JOB_ID` | 当前任务 ID |
| `DEPLOY_FETCH_PROXY` | 配置的 `fetch.proxy` 地址（可能为空） |

- 脚本退出码 `0` 记为 `succeeded`，非零或超时（`deploy_timeout_sec`）记为 `failed`。
- 脚本标准输出与标准错误统一写入任务日志，请勿在输出中打印密钥。

## Git 拉取策略

`deployd` 对所有受管仓库执行统一拉取策略（最多 5 次尝试）：

- **第 1 次**：强制直连（清除代理环境变量并通过 `-c http.proxy= -c https.proxy=` 覆盖 Git 配置）。
- **第 2–5 次**：HTTPS 仓库走 `fetch.proxy`（设为 `"none"` 则全直连）；SSH 仓库始终直连，不走代理。
- **超时**：单次 `fetch` 超时 60 秒，`clone` 超时 10 分钟。
- **注意事项**：部署前请排查目标用户的 Git 配置，移除与统一策略冲突的按 URL 代理设置（如 `http.<url>.proxy`）。

## 生产部署与安全

- **网络暴露**：默认仅监听 `127.0.0.1`，公网入口必须套 HTTPS 反向代理，建议对管理 API 与 Webhook 入口做访问隔离。
- **文件与权限**：`config.yaml` 含敏感密钥应设为 `chmod 600`；SQLite 数据库加有独占文件锁，单库仅允许一个 `deployd` 实例运行；备份时注意连同 WAL 文件一起备份。
- **进程隔离（`run_as`）**：配置 `run_as` 后，Git 操作与站点脚本均通过 `sudo -n -H -u <user>` 执行。需提前配置受限 CI 用户、工作树权限、Git 凭据与最小化 `sudo` 规则。
- **systemd 配置**：建议使用 `KillMode=control-group`，确保服务强杀或重启时一并清理残留构建子进程；服务重启后，原 `running` 任务会恢复为 `failed`，`queued` 任务继续调度。
