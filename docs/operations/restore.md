# 备份恢复与演练

备份由 `deploy/backup.sh` 每天生成，文件格式为：

```text
backups/gateway-<UTC时间>.dump.enc          # AES-256-CBC + PBKDF2 加密的 pg_dump 自定义格式
backups/gateway-<UTC时间>.dump.enc.sha256   # 校验和
```

默认保留 7 天。**恢复口令（`BACKUP_PASSPHRASE`）必须与 `MASTER_KEY` 分开保存**；丢失口令等同于丢失备份。

目标：RPO 约 24 小时（每日备份），RTO 约 1 小时（单机恢复）。

## 1. 恢复前检查

```bash
cd /opt/api-gateway
ls -lh backups/
sha256sum -c backups/gateway-<时间>.dump.enc.sha256
```

确认：

- 备份文件与校验和来自同一时间点。
- 明确恢复目标：覆盖当前数据库，还是恢复到新库/新主机。
- 恢复会覆盖当前数据。除非是灾难恢复，先对当前库再做一次临时备份：

  ```bash
  BACKUP_PASSPHRASE_FILE=/etc/api-gateway/backup.passphrase \
    bash deploy/backup.sh
  ```

## 2. 解密

在服务器本地临时目录解密，完成后立即删除明文：

```bash
export BACKUP_PASSPHRASE="$(sudo cat /etc/api-gateway/backup.passphrase)"
openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 \
  -pass env:BACKUP_PASSPHRASE \
  -in backups/gateway-<时间>.dump.enc \
  -out /tmp/gateway-restore.dump
chmod 600 /tmp/gateway-restore.dump

# 只查看目录、不落库，确认文件完整
docker compose --env-file .env.production exec -T postgres \
  pg_restore --list < /tmp/gateway-restore.dump | head -20
```

能列出 `Table Data` 等条目说明解密和格式正常。

## 3. 覆盖恢复（同一台服务器）

1. 停止网关，避免恢复期间继续写入：

   ```bash
   docker compose --env-file .env.production stop gateway
   ```

2. 重建空数据库并恢复。此步骤会删除现有数据库，确认已完成第 1 节的临时备份：

   ```bash
   docker compose --env-file .env.production exec -T postgres \
     sh -c 'dropdb -U "$POSTGRES_USER" --force "$POSTGRES_DB" && createdb -U "$POSTGRES_USER" -O "$POSTGRES_USER" "$POSTGRES_DB"'

   docker compose --env-file .env.production exec -T postgres \
     sh -c 'pg_restore --no-owner --no-privileges \
       -U "$POSTGRES_USER" -d "$POSTGRES_DB"' \
     < /tmp/gateway-restore.dump
   ```

   `pg_dump` 使用 `--no-owner --no-privileges`，因此角色和授权不在备份内，必须在恢复后重新授予。

3. 重新创建角色并授权（若角色已存在，只更新密码和授权）。将密码替换为 `.env.production` 中 `DATABASE_URL` / `AUDIT_MAINTENANCE_DATABASE_URL` 使用的实际密码：

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
   GRANT USAGE, CREATE ON SCHEMA public TO gateway_app;

   GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO gateway_app;
   GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO gateway_app;

   REVOKE ALL PRIVILEGES ON audit_logs FROM gateway_app;
   GRANT SELECT, INSERT ON audit_logs TO gateway_app;
   GRANT USAGE, SELECT ON SEQUENCE audit_logs_id_seq TO gateway_app;

   GRANT SELECT (event_time) ON audit_logs TO gateway_maint;
   GRANT DELETE ON audit_logs TO gateway_maint;
   SQL
   ```

4. 启动并验证：

   ```bash
   docker compose --env-file .env.production up -d
   docker compose --env-file .env.production exec gateway \
     wget -qO- http://127.0.0.1:8080/readyz
   ```

5. 数据核对：

   ```bash
   docker compose --env-file .env.production exec -T postgres \
     sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
   SELECT count(*) AS employees FROM employees;
   SELECT count(*) AS tokens FROM tokens;
   SELECT count(*) AS audit_rows, max(event_time) AS latest_audit FROM audit_logs;
   SELECT count(*) AS admins FROM admin_users;
   SQL
   ```

   然后登录后台，检查员工、Token、审计和 Dashboard；用测试 Token 发一次非流式请求，确认 200 且审计新增。

6. 删除明文与临时变量：

   ```bash
   rm -f /tmp/gateway-restore.dump
   unset BACKUP_PASSPHRASE
   ```

## 4. 新主机灾难恢复

适用场景：服务器丢失或磁盘损坏。新主机已安装 Docker、代码和 `.env.production`，DNS 仍指向该主机或已切换到新 IP。

```bash
cd /opt/api-gateway

# 1) 只启动数据库，先不要启动 gateway（否则会在空库上执行迁移和 bootstrap）
docker compose --env-file .env.production up -d postgres

# 2) 解密、恢复（步骤同第 2、3 节）
#    - 确认 POSTGRES_USER/DB 与新库一致
#    - 恢复时使用新主机的超级用户

# 3) 重新创建角色、授权、启动全部服务
docker compose --env-file .env.production up -d
```

注意：

- 不要在恢复前启动 `gateway`，否则空库会先被迁移并创建管理员；虽然覆盖恢复可以修正，但会增加风险。
- `MASTER_KEY` 必须与备份生成时一致，否则上游 Key、Prompt/Response、SMTP 密码和 spool 文件都无法解密。若刚轮换过主密钥，把旧密钥放入 `MASTER_KEY_PREVIOUS`。
- 证书保存在 `caddy_data` 卷；新主机将重新签发。DNS 必须先就绪。
- 若 `POSTGRES_USER` 不是 `gateway`，上文 SQL 中的数据库名需同步替换。

## 5. 恢复演练（每月）

演练恢复到独立数据库，不影响生产：

```bash
cd /opt/api-gateway
export BACKUP_PASSPHRASE="$(sudo cat /etc/api-gateway/backup.passphrase)"
openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 \
  -pass env:BACKUP_PASSPHRASE \
  -in backups/gateway-<时间>.dump.enc -out /tmp/drill.dump

docker compose --env-file .env.production exec -T postgres \
  sh -c 'createdb -U "$POSTGRES_USER" gateway_restore_drill'

docker compose --env-file .env.production exec -T postgres \
  sh -c 'pg_restore --no-owner --no-privileges \
    -U "$POSTGRES_USER" -d gateway_restore_drill' \
  < /tmp/drill.dump

docker compose --env-file .env.production exec -T postgres \
  sh -c 'psql -U "$POSTGRES_USER" -d gateway_restore_drill -c "
    SELECT count(*) AS employees FROM employees;
    SELECT count(*) AS audit_rows, max(event_time) AS latest_audit FROM audit_logs;"'

docker compose --env-file .env.production exec -T postgres \
  sh -c 'dropdb -U "$POSTGRES_USER" gateway_restore_drill'

rm -f /tmp/drill.dump
unset BACKUP_PASSPHRASE
```

记录演练日期、备份文件、恢复耗时、核对结果和发现的问题。演练失败时按真实故障处理。

## 6. 常见问题

| 现象 | 处理 |
|---|---|
| `bad decrypt` / `digital envelope routines::bad decrypt` | 口令错误或备份损坏；尝试另一份备份，确认 `BACKUP_PASSPHRASE_FILE` 未被修改 |
| `pg_restore: error: input file appears to be a text format dump` | 解密不完整或文件不是 `-Fc` 格式；重新解密并检查 `pg_restore --list` |
| `permission denied for table audit_logs`（应用运行时） | 重新执行授权 SQL；确认应用连接的是 `gateway_app` 而不是 `gateway_maint` |
| 登录后上游调用报“无法解密”类错误 | `MASTER_KEY` 与备份不匹配；配置原密钥或 `MASTER_KEY_PREVIOUS` 后重启 |
| 后台无审计数据 | 检查审计 spool 与 `gateway` 日志；确认恢复的数据库包含 `audit_logs` |
