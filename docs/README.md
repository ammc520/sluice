# 项目文档

访问入口（单域名）：

| 入口 | 路径 |
|---|---|
| 管理后台 | `https://<域名>/admin/login` |
| 员工门户 | `https://<域名>/portal/login` |
| 模型网关 | `https://<域名>/v1` |

- [设计文档](design.md)：目标、范围、架构、数据模型、接口与安全设计。
- [实施计划](implementation-plan.md)：按任务拆分的开发步骤、测试要求与验收标准。
- [生产部署](operations/deploy.md)：Compose、Caddy、数据库角色、发布与旧 Key 切换。
- [备份恢复](operations/restore.md)：加密备份、恢复步骤与每月演练。
- [员工额度与自助 Key](employee-quota.md)：按员工共享额度、自助 Key 生命周期、提额审批、数据模型与迁移方案。
- [员工自助门户](portal.md)：门户启用、账号开通、Key 管理与提额申请、安全与排障。
- [员工接入](operations/employee-onboarding.md)：OpenAI / Anthropic / DeepSeek SDK 配置与常见错误。
- [真实上游验收](operations/live-verification.md)：上线后三家上游、审计、额度、DLP 与旧 Key 撤销检查。

文档按“设计 → 计划 → 实现 → 运维”组织。
