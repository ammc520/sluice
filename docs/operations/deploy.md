# 生产部署手册

本文面向在腾讯云（或任意 Linux 云主机）上首次部署、升级和切换上游 Key 的运维人员。整套服务由 `docker-compose.yml` 编排：

| 服务 | 作用 | 是否对公网开放 |
|---|---|---|
| `caddy` | 自动 HTTPS、静态管理后台与员工门户、反代 `/v1`、`/admin/api`、`/portal/api` | 仅 80/443 |
| `gateway` | Go 网关，SQL 迁移、DLP、配额、审计、管理 API | 否 |
| `postgres` | PostgreSQL 16 数据 | 否 |
| `redis` | 限流与会话缓存（可丢） | 否 |

`postgres`、`redis` 位于 `internal` 网络，没有发布端口；`gateway` 同时接入公网边缘网络和内部网络。数据库和 Redis 不应出现在云安全组或主机防火墙的放行列表里。

## 1. 前置条件

- Ubuntu 22.04/24.04 LTS，2 vCPU / 4 GB 内存起步，系统盘之外建议挂载独立数据盘。
- 已安装 Docker Engine 24+ 与 Compose v2.24+（`docker compose version`；`env_file.required` 需要该版本）。
- 一个域名（例如 `api.example.com`）的 A/AAAA 记录已指向服务器公网 IP。
- 云安全组与主机防火墙只放行：SSH（限制来源 IP）、TCP 80、TCP 443、UDP 443。
- 仓库代码（`git clone` 或发布包）位于例如 `/opt/api-gateway`。

> 证书由 Caddy 通过 ACME 自动申请和续期。80 端口必须保持可达，用于 HTTP→HTTPS 跳转和证书校验；`caddy_data` 卷保存证书，不能随意删除。

## 2. 准备配置

```bash
cd /opt/api-gateway
cp .env.production.example .env.production
chmod 600 .env.production
```

编辑 `.env.production`，至少完成以下项：

```bash
# 32 字节主密钥，用于加密上游 Key、Prompt/Response、SMTP 密码和 spool
openssl rand -base64 32          # 填入 MASTER_KEY

# 数据库超级用户密码（用于首次迁移与运维）
openssl rand -hex 24             # 填入 POSTGRES_PASSWORD

# 初始管理员密码（至少 12 位）
# 填入 BOOTSTRAP_ADMIN_PASSWORD；首次登录后立即修改并删除该变量
```

需要核对的变量：

| 变量 | 说明 |
|---|---|
| `DOMAIN` | 对外域名，Caddy 用来自动签发证书 |
| `PUBLIC_BASE_URL` | `https://<DOMAIN>`，用于生成绝对链接 |
| `DATABASE_URL` | 首次部署先用 `POSTGRES_USER` 超级用户；创建应用角色后改为 `gateway_app`（见第 5 节） |
| `MASTER_KEY` | base64 编码的 32 字节密钥。丢失将无法解密历史审计原文与上游 Key |
| `MASTER_KEY_PREVIOUS` | 仅在轮换主密钥的过渡期设置 |
| `COOKIE_SECURE` | 生产必须为 `true` |
| `TRUSTED_PROXIES` | 默认 `172.16.0.0/12`，覆盖 Docker 网桥；不要配置为任意公网段 |
| `BOOTSTRAP_ADMIN_PASSWORD` | 仅在不存在管理员时用于创建初始账号 |
| `AUDIT_RETENTION_DAYS` | 审计保留天数，后台设置可覆盖 |
| `ALLOW_PRIVATE_UPSTREAMS` | 仅当确有自建模型部署在私网地址时设为 `true` |

`MASTER_KEY` 应保存在密码管理器或密钥管理服务中，不要只留在服务器本地。`BACKUP_PASSPHRASE` 是另一个独立密钥，必须与 `MASTER_KEY` 不同。

`TRUSTED_PROXIES` 必须覆盖 Caddy 所在容器的网段。启动后可执行 `docker network inspect api-gateway_edge` 查看 `Subnet`；如果与默认的 `172.16.0.0/12` 不重叠，按实际网段修改。配置错误会导致员工来源 IP 被识别为 Caddy 容器地址，影响 IP 白名单和审计。

## 3. 首次启动

```bash
cd /opt/api-gateway
docker compose --env-file .env.production build
docker compose --env-file .env.production up -d
```

启动顺序由健康检查保证：`postgres`/`redis` 健康 → `gateway`（自动执行嵌入的 goose 迁移并创建 bootstrap 管理员）健康 → `caddy` 启动并申请证书。

检查状态：

```bash
docker compose --env-file .env.production ps
docker compose --env-file .env.production logs --tail=100 gateway
docker compose --env-file .env.production exec gateway \
  wget -qO- http://127.0.0.1:8080/readyz
curl -fsS -o /dev/null -w '%{http_code}\n' https://api.example.com/
curl -sS -o /dev/null -w '%{http_code}\n' https://api.example.com/v1/models
```

预期：`/readyz` 返回 `{"status":"ready"}`；首页返回 200；未携带 Token 的 `/v1/models` 返回 401。`/healthz`、`/readyz`、`/metrics` 对公网返回 404，只能从容器内部访问。

首次登录管理后台 `https://api.example.com/admin/login`（员工门户入口为 `/portal/login`）：

1. 使用 `BOOTSTRAP_ADMIN_USERNAME` / `BOOTSTRAP_ADMIN_PASSWORD` 登录。
2. 进入「管理员账号」，为超级管理员重置强密码（会使当前会话失效，需要重新登录）。
3. 从 `.env.production` 删除 `BOOTSTRAP_ADMIN_PASSWORD`。
4. `docker compose --env-file .env.production up -d gateway` 重建容器使变量生效。

## 4. 数据库角色与最小权限

网关正常写入审计只需要 `INSERT`，后台查询需要 `SELECT`；**应用角色不得拥有 `audit_logs` 的 `DELETE`**。保留任务使用独立连接（`AUDIT_MAINTENANCE_DATABASE_URL`）删除过期记录。

以下 SQL 在首次迁移完成后、以 PostgreSQL 超级用户执行。把两个密码替换为密码管理器生成的值：

```bash
docker compose --env-file .env.production exec -T postgres \
  sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
DO $$ BEGIN
  CREATE ROLE gateway_app LOGIN;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
DO $$ BEGIN
  CREATE ROLE gateway_maint LOGIN;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER ROLE gateway_app PASSWORD 'REPLACE_APP_PASSWORD';
ALTER ROLE gateway_maint PASSWORD 'REPLACE_MAINT_PASSWORD';

GRANT CONNECT ON DATABASE gateway TO gateway_app, gateway_maint;
GRANT USAGE ON SCHEMA public TO gateway_maint;
-- CREATE 只为让 goose 在启动时确认版本表存在；业务表仍归超级用户所有。
GRANT USAGE, CREATE ON SCHEMA public TO gateway_app;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO gateway_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO gateway_app;

-- 审计表只允许追加和查询。
REVOKE ALL PRIVILEGES ON audit_logs FROM gateway_app;
GRANT SELECT, INSERT ON audit_logs TO gateway_app;
GRANT USAGE, SELECT ON SEQUENCE audit_logs_id_seq TO gateway_app;

-- 维护角色只用于保留任务：读取 event_time 过滤并删除过期审计。
GRANT SELECT (event_time) ON audit_logs TO gateway_maint;
GRANT DELETE ON audit_logs TO gateway_maint;
SQL
```

然后修改 `.env.production`：

```bash
DATABASE_URL=postgres://gateway_app:<URL编码后的密码>@postgres:5432/gateway?sslmode=disable
AUDIT_MAINTENANCE_DATABASE_URL=postgres://gateway_maint:<URL编码后的密码>@postgres:5432/gateway?sslmode=disable
```

重启网关并验证：

```bash
docker compose --env-file .env.production up -d gateway
docker compose --env-file .env.production exec gateway \
  wget -qO- http://127.0.0.1:8080/readyz

# 应用角色不能删除审计（应报 permission denied）
docker compose --env-file .env.production exec -T postgres \
  psql -U gateway_app -d gateway -c 'DELETE FROM audit_logs WHERE false;'
```

如果未设置 `AUDIT_MAINTENANCE_DATABASE_URL`，保留任务会禁用，审计数据不会自动清理。

## 5. 发布升级与迁移

网关启动时会自动执行嵌入的 goose 迁移。启用最小权限后，`gateway_app` 不能修改超级用户拥有的表，因此**每次升级必须在启动新版本前，用专用迁移命令执行待迁移脚本**，否则应用会因 DDL 权限不足而启动失败：

```bash
cd /opt/api-gateway

# 1) 先备份
BACKUP_PASSPHRASE_FILE=/etc/api-gateway/backup.passphrase \
  bash deploy/backup.sh

# 2) 构建新镜像
docker compose --env-file .env.production build

# 3) 用超级用户跑迁移（只在这一步使用超级用户连接）
#    镜像内的 /app/migrate 只执行嵌入的 goose 迁移，不启动 HTTP 服务。
docker compose --env-file .env.production run --rm --no-deps \
  -e MIGRATE_DATABASE_URL="postgres://gateway:<超级用户密码>@postgres:5432/gateway?sslmode=disable" \
  --entrypoint /app/migrate gateway

# 4) 重新授予新表/新序列权限（迁移会创建新对象）
docker compose --env-file .env.production exec -T postgres \
  sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO gateway_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO gateway_app;
REVOKE ALL PRIVILEGES ON audit_logs FROM gateway_app;
GRANT SELECT, INSERT ON audit_logs TO gateway_app;
GRANT USAGE, SELECT ON SEQUENCE audit_logs_id_seq TO gateway_app;
GRANT SELECT (event_time) ON audit_logs TO gateway_maint;
GRANT DELETE ON audit_logs TO gateway_maint;
SQL

# 5) 启动新版本
docker compose --env-file .env.production up -d
```

迁移执行前必须备份。迁移是向前兼容的，不要依赖自动回滚；需要回退结构时按 `restore.md` 恢复备份。

镜像回退：升级前给当前镜像打标签，出问题时恢复标签并重启网关：

```bash
docker tag api-gateway:latest api-gateway:rollback
# 升级后如需回退
docker tag api-gateway:rollback api-gateway:latest
docker compose --env-file .env.production up -d --no-build gateway
```

若新版本已经执行了不兼容迁移，必须同时恢复数据库。

## 6. 日志与监控

- 网关日志为 JSON，写入 stdout，由 Docker `json-file` 驱动轮转（50 MB × 5）。查看：

  ```bash
  docker compose --env-file .env.production logs -f --tail=200 gateway
  ```

- 审计数据只写入 PostgreSQL，不写 stdout；排查时使用管理后台「审计日志」。
- `/metrics` 不在 Caddy 路由中，公网不可达。从内网采集：

  ```bash
  docker compose --env-file .env.production exec gateway \
    wget -qO- http://127.0.0.1:8080/metrics
  ```

  如需 Prometheus 采集，可在同一 Docker 网络部署采集器，或通过 SSH 隧道/VPN 访问，不要把 `/metrics` 暴露到公网。
- 建议告警：网关容器不健康、5xx 比例、磁盘使用率 > 80%、审计 spool 文件持续增长、PostgreSQL 连接失败、证书续期失败。
- `docker compose ps` 中 `gateway`/`caddy` 应为 `healthy`。

## 7. 每日备份

1. 在服务器上创建独立口令文件（不要与 `MASTER_KEY` 相同）：

   ```bash
   sudo install -d -m 700 /etc/api-gateway
   openssl rand -base64 48 | sudo tee /etc/api-gateway/backup.passphrase >/dev/null
   sudo chmod 600 /etc/api-gateway/backup.passphrase
   ```

2. 手动执行一次并确认输出：

   ```bash
   BACKUP_PASSPHRASE_FILE=/etc/api-gateway/backup.passphrase \
     bash deploy/backup.sh
   sha256sum -c backups/gateway-*.dump.enc.sha256
   ```

3. 配置 cron（每天 03:17，按实际路径调整）：

   ```cron
   17 3 * * * cd /opt/api-gateway && BACKUP_PASSPHRASE_FILE=/etc/api-gateway/backup.passphrase RCLONE_REMOTE=cos:company-backups/api-gateway /usr/bin/bash deploy/backup.sh >> /var/log/api-gateway-backup.log 2>&1
   ```

   `backup.sh` 使用 `pg_dump -Fc`，在落盘前用 AES-256（PBKDF2）加密，保留本地 7 天；设置了 `RCLONE_REMOTE` 时上传到 COS/S3。远端建议再配置生命周期规则。任何一步失败脚本都会非零退出，cron 会记录错误。

4. 每月按 `restore.md` 做一次恢复演练，并记录结果。

## 8. 上线切换：撤销旧上游 Key

在旧 Key 被自由分发的情况下，只要旧 Key 仍有效，员工就可以绕过网关直连上游，DLP 和审计将失效。切换必须按顺序执行：

1. 在后台录入 Provider（上游 Key 只保存在服务端，AES-256-GCM 加密），创建模型路由，并执行连通性测试。
2. 创建员工、Token、额度和 IP 白名单；通过安全渠道把公司 Token 一次性交付给员工。
3. 端到端验证：OpenAI、Claude、DeepSeek 三个上游各完成一次**流式**和一次**非流式**请求；确认审计记录、Token 用量、额度扣减、DLP Mask/Block/Audit 均符合预期。
4. 通知员工更新 `base_url` 和 API Key（模板见 `employee-onboarding.md`），并给出切换截止时间。
5. 验证通过后再到上游控制台**撤销/轮换所有此前自由分发的旧 Key**。不要在验证前撤销，否则切换窗口内无人可用。
6. 保留 1–2 周观察期：对比上游账号侧调用量与网关统计。若上游调用量明显高于网关，说明仍有直连绕过，继续排查并收紧旧 Key。
7. 如业务上确实不能立即撤销旧 Key，必须书面记录这是临时状态、责任人和截止日期；不能长期双轨运行。

撤销完成后，抽测一次直连旧 Key 应失败、经网关请求应成功。

## 9. 日常检查清单

- [ ] `docker compose ps` 全部 healthy，最近 24 小时无重启。
- [ ] `https://<DOMAIN>` 可登录，证书有效期正常。
- [ ] 当日备份存在、校验通过、已上传 COS。
- [ ] 磁盘使用率正常；`backups/`、Docker 日志、`audit-spool` 未异常增长。
- [ ] 审计 spool 未堆积告警。
- [ ] 上游账号用量与网关统计趋势一致。
