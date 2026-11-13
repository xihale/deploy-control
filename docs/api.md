# HTTP API 与 Webhook

## 认证与约定

- 除 `GET /healthz` 和 `POST /webhook/github/{site}` 外，所有请求需携带 `Authorization: Bearer <token>`。
- 服务端仅保存 token 的 SHA-256 摘要（`users[].token_hash`），支持两种角色：`viewer`（只读）和 `admin`（可触发部署）。
- 鉴权失败返回 `401`，权限不足返回 `403`。成功响应为 JSON（日志接口除外）；错误响应通常为 `text/plain`。

## 接口一览

| 方法与路径 | 权限 | 说明 |
| --- | --- | --- |
| `GET /healthz` | 公开 | 健康检查，返回 `{"status":"ok","queued":0,"running":0}` |
| `GET /api/me` | `viewer` | 当前用户 `{"name":"alice","role":"admin"}` |
| `GET /api/sites` | `viewer` | 已配置站点列表（含 `has_webhook_secret`、`run_as`） |
| `GET /api/deploys` | `viewer` | 部署列表，支持 `?site=&status=&limit=20&offset=0` |
| `POST /api/deploys` | `admin` | 创建手动部署，入队返回 `202` 与任务对象 |
| `GET /api/deploys/{id}` | `viewer` | 单条任务详情 |
| `GET /api/deploys/{id}/log` | `viewer` | 纯文本日志尾部，`?bytes=16384`（默认 16 KiB，最大 1 MiB） |
| `POST /webhook/github/{site}` | HMAC 验签 | GitHub push Webhook |

## 手动部署

`POST /api/deploys`：

```json
{"site":"blog","branch":"main","sha":"0123456789abcdef0123456789abcdef01234567"}
```

- `branch` 和 `sha` 均可选：省略时分别取站点默认分支和 fetch 后的分支最新提交。
- 指定旧 `sha` 只是基于该提交新建一条部署任务，不等同于原子回滚。

## 任务对象与状态

```json
{
  "id": 1,
  "site": "blog",
  "trigger_type": "push",
  "branch": "main",
  "sha": "0123456789abcdef0123456789abcdef01234567",
  "checked_sha": "0123456789abcdef0123456789abcdef01234567",
  "delivery_id": "github-delivery-id",
  "status": "succeeded",
  "attempts": 1,
  "created_at": "2026-11-06T00:00:00Z",
  "started_at": "2026-11-06T00:00:01Z",
  "finished_at": "2026-11-06T00:01:00Z",
  "exit_code": 0,
  "log_path": "/var/lib/deploy-control/logs/job-1.log"
}
```

- **字段说明**：时间均为 UTC；无数据的可选字段省略。`sha` 为请求提交，`checked_sha` 为实际检出提交。`exit_code` 为 `-1` 表示异常终止（如超时或中断）。
- **状态说明**：
  - `queued`：排队中（同一站点串行执行，受 `global_concurrency` 限制）
  - `running`：执行中
  - `succeeded`：脚本退出码为 `0`（不独立证明外部线上流量已原子切换）
  - `failed`：执行失败、超时或服务异常重启中断
  - `superseded`：同站点同分支排队任务被后续新 `push` 替代

## GitHub Webhook

`POST /webhook/github/{site}` 校验站点 `webhook_secret` 与请求头：

- `X-Hub-Signature-256`：HMAC-SHA256 验签，失败返回 `403`。
- `X-GitHub-Event`：仅处理 `push`，其余事件（如 `ping`）返回 `200` 并忽略。
- `X-GitHub-Delivery`：按 `delivery_id` 去重，重复投递返回 `200` 且不重复入队。
- 仅目标分支的普通推送会入队并返回 `202`；删除分支等无效提交直接返回 `200` 忽略。
