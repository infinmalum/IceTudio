# 文档清理记录

2026-11-01，仅处理文档，不删除代码、数据或测试。

| 操作 | 文档 | 原因与替代 |
|---|---|---|
| DELETE | docs/scenarios.md | 四套通用模板已删除，正文仍指导 templates/--profile；当前范围由 P0 契约和根计划承接，音乐创作路线见本目录 |
| DELETE | docs/profile-drafts.md | Profile 草案/分享命令已经失效，Skill 部分与新契约不一致；当前 Skill 规范由 ../p4-skill-evolution.md 承接，共享 improver/证据/权限继续保留 |
| KEEP | docs/frontend-product-audit.md | 包含历史缺陷和验证范围，不是单纯操作指南；保留其历史标记，不再作为现状待办 |
| KEEP | docs/history/* | 含旧实现和限定验证证据；时间早不等于没有价值。历史链接中已删除指南改指本文，避免继续引导运行失效命令 |
| KEEP | docs/p0–p4、docs/validation、许可证、实验 | 当前契约与验收证据不可因新规划删除 |
| KEEP | desktop/fixed-workflows/materials/mcp-and-skills | 混合当前保留能力与旧术语；新入口索引显式标记历史使用限制，后续对应功能文档修订时按实际接口重写，不能整删共享说明 |
| UPDATE | README、IMPLEMENTATION_PLAN、architecture、AGENTS | 链接新路线，明确扩展目标与当前实现、旧首版范围的关系 |

历史文件不得覆盖本次目标设计，但新目标也不代表旧验证证据可丢弃。本轮删除两份失效产品指南，不把保留的历史审计说成已清理完所有旧术语。
