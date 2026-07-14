# works-on-my-whiteboard 项目设计

- 状态：已批准
- 日期：2026-07-14
- 仓库：`PinoHouse/works-on-my-whiteboard`
- 目标读者：资深 / Staff 后端工程师与架构师候选人
- 文档语言：简体中文，保留准确的英文术语
- 实验语言：Go
- 许可：Apache-2.0

## 1. 摘要

`works-on-my-whiteboard` 是一个系统设计面试知识库与可运行实验室。名字来自 “works on my machine”：架构在白板上看似成立并不构成证据，关键结论必须能够从约束推导，或通过实现、测试、基准和故障实验验证。

项目不把系统设计面试理解为背诵组件清单。它关注候选人能否：

1. 把模糊题目转化为显式问题模型；
2. 找到不可违反的不变量和主导约束；
3. 从规模、偏斜、故障、成本和组织边界推导复杂度；
4. 比较多个可行方案，并说明决策切换条件；
5. 区分假设、推导、实测和外部事实；
6. 设计系统的运行、降级、迁移和演进路径。

项目采用“双索引证据图谱”：读者既可以从经典面试题进入，也可以从稳定原理进入；题目、原理、实验和证据通过稳定 ID 建立多对多关系。

## 2. 第一性原理立场

每道题都遵循同一条推导链：

```text
surface prompt
  -> explicit assumptions and non-goals
  -> contracts, state, invariants and SLOs
  -> workload, skew, cost and failure model
  -> unavoidable constraints and lower bounds
  -> simplest correct baseline
  -> observed or deduced bottleneck
  -> alternatives and trade-offs
  -> decision and switching conditions
  -> implementation and evidence
  -> operations, migration and interview rubric
```

任何组件名都不能作为推导起点。Redis、Kafka、PostgreSQL 等成熟组件只在原理模型完成后，通过统一 adapter 接口参与对照实验。

## 3. 目标与非目标

### 3.1 目标

- 覆盖常见、经典和重点系统设计面试题；
- 总结每道题客观考察的能力，而不是复制品牌化标准答案；
- 为每道题提供完整题目档案和至少一个可运行 scenario lab；
- 为每个 required principle 提供至少一个可复用 primitive lab；
- 让关键结论可以追溯到推导、仓库实测或直接来源；
- 覆盖 Staff 级的多地域、演进、运维、安全、成本和组织边界；
- 从 clean checkout 通过 `make verify` 完成严格验证。

### 3.2 非目标

- 不为 75 道题分别复制一套生产级微服务；
- 不开发与原理验证无关的业务 UI；
- 不把项目变成 Redis、Kafka 或数据库的完整重实现；
- 不引入图数据库维护知识关系；
- 不声称小型本地实验可以替代真实生产容量测试；
- 不把 skipped、flaky 或缺少环境的实验包装为通过；
- 首版不开发自定义文档网站，GitHub Markdown 是阅读入口。

## 4. 完整范围契约

`scope.yaml` 将是 v1.0 范围的机器可读契约。以下 10 个家族和 75 个基线条目全部是 required。题目可以引用多个家族，但在范围契约中只有一个 primary family，以避免重复计数。

本项目中“v1.0 全覆盖”的可执行定义是：`scope.yaml.cases` 中精确枚举的 ID 集合，与 `cases/*/case.yaml` 中的 complete required ID 集合完全相等。validator 比较集合相等性，不以数量相等代替；75 只是当前基线的可读摘要。品牌别名、同构题和明确不纳入的变体记录在 `scope.yaml.exclusions`，每项必须给出 canonical case ID 和 rationale。新增公开目录中的独立经典题必须先通过 ADR 更新范围契约，才能继续发布审计。

10 个稳定 family ID 如下：

| Family ID | 中文名称 | Required cases |
| --- | --- | ---: |
| `addressing-traffic` | 寻址、分片与流量治理 | 7 |
| `distributed-storage` | 通用分布式存储 | 7 |
| `feed-social-ranking` | Feed、社交内容与排名 | 8 |
| `realtime-collaboration` | 实时通信与协作状态 | 8 |
| `search-crawl-geo` | 搜索、抓取、地理与发现 | 8 |
| `media-delivery` | 媒体与内容分发 | 7 |
| `transactions-contention` | 强事务与资源争抢 | 8 |
| `streaming-analytics-observability` | 流处理、分析与可观测性 | 7 |
| `scheduling-control-plane` | 调度、工作流与平台控制面 | 8 |
| `ai-vector` | AI / ML 与向量检索 | 7 |

每个 case 的 `primary_family` 必须是该集合中的一个 ID，且 scope 中只能出现一次；跨家族关系通过 `secondary_families` 表达，不参与覆盖计数。

### 4.1 寻址、分片与流量治理（7）

- `url-shortener`：短链接
- `pastebin`：文本分享
- `distributed-id`：分布式唯一 ID
- `dns-service-discovery`：DNS 与服务发现
- `load-balancer-api-gateway`：负载均衡与 API Gateway
- `distributed-rate-limiter`：分布式限流器
- `consistent-hash-router`：一致性哈希与分片路由

### 4.2 通用分布式存储（7）

- `key-value-store`：分布式 KV Store
- `distributed-cache`：分布式缓存
- `distributed-sql`：分布式关系数据库
- `wide-column-document-store`：宽列 / 文档存储
- `object-storage`：S3 / Blob Store
- `cloud-file-sync`：Drive / Dropbox 文件同步
- `time-series-database`：时序数据库

### 4.3 Feed、社交内容与排名（8）

- `social-news-feed`：社交信息流
- `photo-sharing`：图片社交平台
- `qa-news-aggregation`：问答与新闻聚合
- `top-k-heavy-hitters`：Top-K 与热点统计
- `leaderboard`：排行榜
- `comments-reactions`：评论与反应系统
- `recommendation-system`：推荐系统
- `ad-serving-ranking`：广告投放与排序

### 4.4 实时通信与协作状态（8）

- `chat-messenger`：即时聊天
- `notification-delivery`：通知系统
- `distributed-email-service`：分布式邮件服务
- `webhook-delivery`：Webhook 投递
- `presence-service`：在线状态
- `collaborative-editor`：协同文档编辑
- `live-comments`：直播评论 / 弹幕
- `video-conferencing`：视频会议

### 4.5 搜索、抓取、地理与发现（8）

- `web-crawler`：Web Crawler
- `autocomplete`：搜索补全
- `full-text-search`：全文检索
- `social-graph`：社交图与好友推荐
- `nearby-places`：附近服务
- `maps-navigation`：地图与导航
- `ride-hailing-dispatch`：网约车匹配与调度
- `food-delivery-dispatch`：外卖匹配与调度

### 4.6 媒体与内容分发（7）

- `video-on-demand`：点播视频平台
- `live-streaming`：视频直播
- `image-service`：图片处理与分发
- `cdn`：内容分发网络
- `large-file-transfer`：大文件分片上传与下载
- `transcoding-pipeline`：媒体转码流水线
- `music-podcast-streaming`：音乐 / 播客流媒体

### 4.7 强事务与资源争抢（8）

- `ticketing`：票务系统
- `appointment-booking`：预约系统
- `online-auction`：在线拍卖
- `payment-system`：支付系统
- `double-entry-ledger-wallet`：复式账本与钱包
- `trading-brokerage`：证券交易与经纪系统
- `ecommerce-order-inventory`：电商订单与库存
- `bank-transfer`：银行转账

### 4.8 流处理、分析与可观测性（7）

- `distributed-log-message-queue`：分布式日志与消息队列
- `pubsub`：发布订阅系统
- `ad-click-aggregation`：广告点击聚合
- `metrics-monitoring-alerting`：指标、监控与告警
- `centralized-logging`：集中式日志平台
- `stream-processing`：流处理引擎
- `batch-data-pipeline`：批处理数据流水线

### 4.9 调度、工作流与平台控制面（8）

- `job-scheduler`：分布式任务调度器
- `dag-workflow`：DAG 工作流引擎
- `ci-runner`：CI / GitHub Actions Runner
- `deployment-system`：部署与发布系统
- `container-orchestrator`：容器调度与编排
- `configuration-feature-flags`：配置中心与 Feature Flag
- `multi-tenant-cloud-control-plane`：多租户云控制面
- `identity-authorization-service`：身份与授权服务

### 4.10 AI / ML 与向量检索（7）

- `llm-chat-serving`：ChatGPT 类对话与推理服务
- `rag-assistant`：RAG 客服 / 知识助手
- `code-assistant`：代码助手
- `vector-database`：向量数据库
- `embedding-index-pipeline`：Embedding 与索引构建流水线
- `inference-gateway`：模型推理网关
- `gpu-scheduler`：GPU 调度器

新增 required 条目必须通过 ADR 修改 `scope.yaml`；删除或合并条目也必须留下兼容映射，避免静默缩减“全覆盖”范围。

## 5. 客观考察维度

每个 case manifest 必须标注以下 12 个维度中哪些适用。未采用的维度必须在题目档案中说明不适用原因。

1. **题设澄清与 SLO**：用户、主流程、成功语义、延迟、可用性、持久性、RPO/RTO 和非目标；
2. **容量、成本与偏斜**：QPS、并发、存储、带宽、峰谷、热点租户和单位经济性；
3. **契约、数据模型与不变量**：API、状态机、所有权、唯一性、守恒关系和非法状态；
4. **放置、路由与分片**：分片键、数据局部性、迁移、热点与再平衡；
5. **一致性、顺序与时间**：可见性、因果、线性一致、时钟、版本和冲突；
6. **并发、事务与幂等**：竞争、隔离、锁 / 租约、fencing、去重、账本和补偿；
7. **缓存、索引与读写放大**：命中率、失效、分页、索引刷新和 fan-out；
8. **异步、背压与公平性**：队列、重试、优先级、配额、调度和过载；
9. **故障隔离、恢复与灾备**：超时、重试风暴、级联故障、region 故障和降级顺序；
10. **可观测、发布与零停机演进**：SLI、告警、灰度、回滚、Schema / 分片迁移和 backfill；
11. **安全、隐私与多租户**：认证、授权、审计、滥用、数据驻留、删除和隔离；
12. **测试、基准、故障注入与证据**：如何证伪主张，以及结果适用边界。

以下 Staff 级挑战横切全部家族：多地域与灾备、过载与故障遏制、静默数据损坏、控制面 / 数据面、多租户配额、成本边界、事件与 Schema 演进、跨团队契约和分阶段交付。

## 6. 仓库架构

仓库采用单 Go module。文档和 manifest 是手写事实源，生成目录不得成为第二份事实源。

```text
works-on-my-whiteboard/
├── README.md
├── LICENSE
├── go.mod
├── scope.yaml
├── sources.yaml
├── aliases.yaml
├── cases/
│   └── social-news-feed/
│       ├── README.md
│       └── case.yaml
├── principles/
│   └── consistency/
│       ├── README.md
│       └── principle.yaml
├── labs/
│   ├── primitives/
│   ├── scenarios/
│   ├── adapters/
│   └── harness/
├── evidence/
│   ├── runs/
│   └── releases/
├── deployments/
├── cmd/whiteboard/
├── internal/
│   ├── catalog/
│   ├── validator/
│   └── report/
├── docs/
│   ├── methodology/
│   ├── glossary/
│   ├── decisions/
│   └── superpowers/specs/
└── generated/
```

### 6.1 组件边界

- `cases/` 只描述题目场景、推导和面试表达，引用而不复制原理与实验；
- `principles/` 只描述稳定知识，可以脱离任何品牌题独立理解；
- `labs/primitives/` 用最小实现验证一个底层机制；
- `labs/scenarios/` 组合 primitives，验证一道题的主导取舍；
- `labs/adapters/` 用同一接口和工作负载对照成熟组件；
- `labs/harness/` 负责工作负载、故障、断言、采样和生命周期；
- `evidence/` 保存机器可读结果、环境元数据和诊断；
- `sources.yaml` 用稳定 ID 保存来源标题、直接 URL、访问日期和许可备注；
- `aliases.yaml` 保存 case、principle、lab 和 source 的旧 ID 到 canonical ID 映射；映射不得成环，多个旧 ID 可以指向同一 canonical ID；
- `cmd/whiteboard/` 提供 `validate`、`run`、`report` 和 `coverage`；
- `generated/` 保存由 manifest 生成的题目索引、原理索引和覆盖矩阵。

## 7. 图谱数据模型

稳定 ID 必须匹配 `^[a-z][a-z0-9-]*$`。重命名必须在 `aliases.yaml` 保留映射；canonical ID 不得同时作为 alias，解析后必须唯一且无环。case、principle 和 lab manifest 中的 `sources` 字段只能引用 `sources.yaml` 中存在的稳定 ID。

### 7.1 Case manifest

```yaml
schema_version: 1
id: social-news-feed
title: 社交信息流
primary_family: feed-social-ranking
secondary_families:
  - streaming-analytics-observability
required: true
status: complete
dimensions:
  - capacity-cost-skew
  - cache-index-amplification
  - async-backpressure-fairness
  - evidence-validation
principles:
  - fanout
  - hot-key-mitigation
  - cursor-pagination
claims:
  - id: social-news-feed-hot-publisher-amplification
    statement: Hot publishers make unconditional fan-out-on-write exceed the write budget.
labs:
  - fanout-hot-celebrity
evidence_requirements:
  - claim: social-news-feed-hot-publisher-amplification
    lab: fanout-hot-celebrity
sources:
  - source-system-design-catalog
```

### 7.2 Principle manifest

```yaml
schema_version: 1
id: fanout
title: Fan-out 与读写放大
required: true
status: complete
dimensions:
  - cache-index-amplification
  - async-backpressure-fairness
claims:
  - id: fanout-write-amplifies-hot-publisher-cost
    statement: Fan-out-on-write trades read latency for write amplification and stored copies.
labs:
  - fanout-amplification
evidence_requirements:
  - claim: fanout-write-amplifies-hot-publisher-cost
    lab: fanout-amplification
sources:
  - source-system-design-catalog
```

### 7.3 Lab manifest

```yaml
schema_version: 1
id: fanout-hot-celebrity
kind: scenario
required: true
status: complete
implementations:
  - fanout-on-write
  - fanout-on-read
case_bindings:
  - id: social-news-feed-celebrity
    case_id: social-news-feed
    claim: social-news-feed-hot-publisher-amplification
    workload: celebrity-skew
    assertions:
      - no-missing-feed-entry
      - stable-cursor-order
required_runs:
  - id: celebrity-skew-with-consumer-delay
    binding: social-news-feed-celebrity
    baseline: fanout-on-write
    variants:
      - fanout-on-read
    workload: celebrity-skew
    faults:
      - consumer-delay
    adapters:
      - id: redis-sorted-set
        required: true
metrics:
  - write-amplification
  - feed-read-p99
sources:
  - source-system-design-catalog
```

任何被 required case 引用的 principle 自动成为 required；principle manifest 也可以显式设置 `required: true`。case、principle、lab 和 adapter 的内容生命周期只有 `draft` 与 `complete`；运行结果状态另行使用 `passed`、`failed`、`skipped`、`flaky` 和 `inconclusive`，两者不得混用。

schema 对 complete 条目执行条件化非空约束：引用、主张、实验和证据要求必须满足各类型契约，不适用字段可以省略，但不能用空数组伪装完成。claim ID 在全仓库唯一，并由所属 case 或 principle manifest 定义。

对于 `kind: scenario` 的 lab，`case_bindings` 必填。每个 required case 必须至少定义一个自己的 claim，并双向引用一个 complete scenario lab；该 lab 必须以相同 case ID 和 claim ID 建立 binding，并提供非空的 case-specific workload 与 assertions。每个 required run 的 `binding` 是单个标量，只能指向一个 case binding；每个 binding 必须被至少一个 required run 引用。一个 scenario 可以共享实现代码并服务多个 case，但每个 case 都必须有独立 binding 和独立 required run。

`kind: primitive` 的 lab 不使用 `case_bindings`，而是用同形的 `principle_bindings` 引用一个 principle ID 和它拥有的 claim ID；primitive required run 同样只能绑定一个 principle binding。validator 要求任何 required run 恰好解析到一个 binding 和一个 claim。

### 7.4 Adapter manifest

adapter 位于 `labs/adapters/<id>/adapter.yaml`，描述成熟组件如何实现统一实验接口：

```yaml
schema_version: 1
id: redis-sorted-set
title: Redis sorted-set adapter
status: complete
interface: ranked-feed-store
runtime: docker
sources:
  - source-redis-sorted-set
```

adapter 是否阻断发布由 lab 的 `required_runs[].adapters[].required` 决定，而不是由 adapter 全局决定。required adapter 必须存在 complete manifest，并为对应 required run 产生通过证据；optional adapter 可以缺环境并记录为 skipped。

### 7.5 Source record

`sources.yaml` 保存可解析来源实体：

```yaml
schema_version: 1
sources:
  - id: source-system-design-catalog
    title: ByteByteGo System Design Interview
    url: https://bytebytego.com/courses/system-design-interview
    accessed_at: 2026-07-14
    kind: catalog
    license_note: Used only to verify topic coverage.
```

validator 要求 source ID 唯一、URL 为直接 HTTPS 链接、访问日期有效，并拒绝任何悬空 source 引用。实际 `sources.yaml` 必须保存第 16 节列出的真实直接链接。

### 7.6 Evidence record

每份不可变 evidence record 位于 `evidence/runs/<id>.json`，至少包含：`schema_version`、全局唯一 `id`、`lab_id`、`required_run_id`、`binding_id`、`claim_id`、`implementation_id`、可选 `adapter_id`、`status`、`source_commit`、`input_digest`、Go 版本、OS / arch、CPU、seed、时间、参数、测量值、断言结果和诊断。文件名等于 evidence ID；内容摘要写入 record，validator 复算摘要。失败运行以新的 ID 保存，不覆盖任何历史结果。

`input_digest` 是对当前版本控制输入的 canonical SHA-256：包含代码、文档、manifests、schemas、部署配置、Makefile 和 Go module 文件，排除 `.git/`、`evidence/` 与可重新生成的 `generated/`。这样 evidence 可以在后续 commit 中保存，而不会形成“记录自身 commit hash”的循环；`source_commit` 提供运行时提交的可读追踪，`input_digest` 才是发布门禁的事实身份。

### 7.7 Release evidence snapshot

`evidence/releases/<input-digest>/manifest.yaml` 是由 `make evidence` 生成的 release snapshot，列出每个 required lab run、binding、claim、baseline、variant 和 required adapter 对应的 evidence ID 与内容摘要。snapshot 中的有效记录必须同时满足：

- evidence schema 有效且状态为 `passed`；
- evidence `input_digest` 与当前 release input digest 完全相同；
- required assertions 全部通过；
- workload、fault、implementation 和 adapter 与 required run matrix 完全匹配；
- 内容摘要与不可变 evidence file 一致。

历史 `failed`、`skipped`、`flaky` 或 `inconclusive` 记录继续保留，但不直接阻断未来发布；发布门禁只读取当前 input digest 的 release snapshot。snapshot 对每个 required matrix cell 必须恰好选择一个有效 passed record，空集合和重复选择都失败。

## 8. 题目档案契约

每个 `cases/<id>/README.md` 固定包含八个部分：

1. **表面题目**：原始 prompt、角色、主流程和歧义；
2. **反问与边界**：SLO、规模、成本、威胁、地域、合规和非目标；
3. **客观模型**：API、数据模型、状态机、不变量、容量区间和偏斜；
4. **必然约束**：信息下界、排队、放大效应和不可兼得的目标；
5. **从简单方案演进**：基线、瓶颈、候选方案、取舍和触发条件；
6. **设计决定**：关键路径、故障语义、降级和未选方案；
7. **运行与演进**：观测、发布、迁移、灾备、安全、多租户和组织边界；
8. **面试考察本质**：优秀信号、常见误区、追问树和 20 / 40 / 60 分钟表达路径。

`whiteboard validate --content` 使用 Markdown AST 判定机械完整性：上述八个二级标题必须按顺序各出现一次；每节至少包含一个非空 paragraph、list、table 或 code block，并达到 schema 中按节定义的最小非代码字符数；不得出现配置中的未完成标记；文档中出现的 claim ID 必须由 case manifest 定义。机械规则防止空壳，release audit 仍必须对推导质量做人工审阅。

每个非平凡主张使用以下标签之一：

- `ASSUMED`：题目未提供，为推进分析而显式选择；
- `DEDUCED`：由约束、不变量或数学模型推导；
- `MEASURED`：由仓库实验得到，并链接环境和原始结果；
- `SOURCED`：来自外部论文或官方文档，并附直接链接。

`DEDUCED`、`MEASURED` 和 `SOURCED` 段落必须携带 claim ID。`MEASURED` claim 必须出现在 `evidence_requirements` 并由 release snapshot 解析到通过证据；`SOURCED` claim 必须解析到 `sources.yaml`；`ASSUMED` 必须说明采用该假设的原因和它改变时的影响。

## 9. 实验契约

可独立记录和判定的“实验单元”是 required run，而不是可复用的 lab package。每个 required run 恰好绑定一个 binding 和一个 claim，只验证一个主要主张，并包含：

1. 可证伪 hypothesis；
2. 最简单正确 baseline；
3. 一个或多个 variant；
4. 对所有实现一致的接口和 workload；
5. 显式负载分布、热点、持续时间和 seed；
6. fault schedule；
7. correctness assertions；
8. latency、throughput、resource、loss、ordering 和 recovery 指标中适用的子集；
9. 支持、否定或缩小假设的 conclusion；
10. 实验无法代表的生产边界。

scenario lab 可以是对共享 harness、primitive 和 adapter 的声明式组合，不要求拥有独立业务服务代码。它的最小独立内容是：由该 case 定义的 claim、case-specific workload 参数与分布、case-specific assertions、一个能暴露主导取舍的 baseline / variant 对比，以及至少一个相关 fault。复用代码不等于复用证据：两个 case 只有在各自存在独立 `case_binding`、required run 和 evidence record 时才能共享同一个 scenario lab。

required case 只有在其至少一个 complete scenario binding 已被 release snapshot 中的 passed evidence 覆盖时，才算“具有可运行实现”。required principle 只有在其 complete primitive lab 的 required run 被同一 snapshot 覆盖时，才算“具有有效证据”。

标准库原理实现先完成；成熟组件 adapter 后完成。二者必须复用接口、工作负载和断言，避免比较不同问题。

数据流如下：

```text
case/principle manifests
  -> whiteboard validate
  -> graph and coverage checks
  -> whiteboard run
  -> baseline / variant / adapter under the same harness
  -> assertions and measurements
  -> immutable evidence record
  -> whiteboard report
  -> generated indexes and case-linked conclusions
```

## 10. 失败与错误语义

- manifest schema、稳定 ID、引用或覆盖关系无效时，`validate` 非零退出；
- 不变量被破坏、结果丢失、超时或 harness 失控时，`run` 非零退出；
- 缺少 Docker 或外部组件时，optional adapter 标记 `skipped` 并说明原因；
- required adapter 在 release audit 中不得 `skipped`；
- `skipped`、`flaky` 和 `inconclusive` 均不得计为 `passed`；
- 所有运行都有 `context.Context` deadline，并在取消时清理 goroutine、端口、容器和临时目录；
- seed、端口分配、故障时刻和负载参数被记录以支持复现；
- 错误向调用方返回，CLI 统一决定诊断和退出码；
- 失败运行写入独立诊断记录，不覆盖最近一次有效证据；
- 外部组件不可用不得阻断标准库核心实验，但会阻断对应 required adapter 的发布门禁。

## 11. 测试与质量门禁

Make targets 是唯一对外验证契约：

- `make verify-fast` 执行第 11.1 节的 PR 门禁；
- `make verify-deep` 执行第 11.2 节的完整负载、故障和 adapter 矩阵；
- `make evidence` 计算当前 input digest，执行 required matrix，并把不可变 records 与 snapshot 写入 `evidence/`，供单独 commit；
- `make verify` 顺序执行 fast 与 deep，重新计算 input digest，验证仓库中已提交的 snapshot，再运行 `whiteboard validate --release <input-digest>`。任何子步骤失败、required matrix cell 缺失、live replay 不通过或 snapshot 非唯一映射都非零退出。

发布流程先在 source commit 上运行 `make evidence` 并提交生成物，再从包含 evidence 的 clean checkout 运行 `make verify`；因为 input digest 排除 evidence 与 generated 文件，两步引用同一输入身份。因此 `make verify` 是 clean checkout 和 v1.0 发布审计的单一严格命令；它要求 Docker 和所有 required adapter 环境可用。日常 PR 使用较快的 `make verify-fast`，不能替代发布门禁。

### 11.1 Pull request 快速门禁

- `gofmt`、`go vet`、unit tests 和 race detector；
- table-driven、property 和短时 fuzz tests；
- manifest schema、稳定 ID、引用、内部链接和覆盖校验；
- 小负载 lab smoke tests 与不变量断言；
- 中文文档结构、术语、主张标签和来源 lint；
- 生成目录与源 manifest 一致性校验。

### 11.2 Scheduled / manual 深度门禁

- 完整负载矩阵、热点和长稳实验；
- 崩溃、超时、网络分区、乱序、重复和恢复注入；
- Docker adapters 的同接口对照；
- 同机同轮相对比较，不设置脆弱的跨机器绝对性能阈值；
- 保存原始结果、环境、seed、commit 和诊断；
- 对证据变化生成 diff，由人工判断是否代表回归或环境变化。

## 12. v1.0 发布契约

仓库从创建起保持 Public，但在以下条件全部满足前不创建正式 v1.0 release：

- `scope.yaml.cases` 的精确 ID 集合与 complete required case manifest 集合相等；当前基线为 75 个，并且每个 family ID 的成员集合与 scope 相等；
- 所有题目档案通过 `whiteboard validate --content`，无未完成标记、空壳段落或未登记 claim；
- 每个 required case 的 scenario lab 双向 binding、case-specific required run 和 evidence requirement 完整；
- 从 required cases 传递闭包得到的所有 required principles、primitive labs、scenario labs 和 required adapters 均为 complete；
- 当前 input digest 的 release snapshot 对每个 required run matrix cell 恰好包含一个同 digest 的 passed evidence record，required adapter 也不例外；
- 历史失败记录可以保留，但当前 input digest 的 snapshot 中不得出现 `failed`、`skipped`、`flaky` 或 `inconclusive`；
- 无悬空 ID、失效内部链接、alias 环、source 缺失或未分类的关键主张；
- clean checkout 可以用 `make verify` 完成构建、执行、已提交 snapshot 验证与严格审计；
- 来源、威胁模型、复现说明和 Apache-2.0 许可通过人工审阅。

## 13. 实施分解

项目范围过大，不使用单一实施计划。每个工作包都有独立 spec、plan、verification 和 review，但全部属于同一个 v1.0 范围。

### W0：Foundation

实现 `scope.yaml`、schemas、catalog loader、`whiteboard` CLI、lab harness，并以 `distributed-rate-limiter` 建立第一个完整垂直样板。

### W1：Principles

建立稳定原理文档、主张账本和 primitive labs。required principle 集合由 75 个 required case manifests 的传递引用闭包产生，并固化在生成的 dependency graph；优先覆盖分片、缓存、复制、时钟、租约、事务、消息、背压、索引、故障隔离和迁移。

### W2：10 Case Packs

按 10 个题型家族逐包完成 75 个 case dossiers 和 scenario labs。每个 case pack 子规格在进入实现前必须声明 `case -> principles -> primitive labs` 和 `case -> scenario lab -> required runs` 的依赖边；多个 case 复用已有 primitives，不复制机制实现。

### W3：Comparisons

加入 Redis、Kafka、PostgreSQL 等成熟组件 adapters，执行同接口对照并生成 evidence reports。

### W4：Release Audit

执行覆盖、复现、来源、安全、许可和 clean-checkout 审计，满足发布契约后创建 v1.0。

W0 完成后，W1 与 W2 可以在 draft 状态并行，但 complete 状态按 dependency graph 逐片门禁：case 不能 complete，直到其 required principles 和 primitive labs 已 complete；W1 不能整体 complete，直到 75 个 cases 的 required principle 闭包全部 complete。validator 拒绝依赖环和缺失边。W3 依赖对应 complete principle、scenario lab 和 required run；W4 依赖全部工作包完成。

## 14. 关键设计决定

| 决定 | 选择 | 未选方案与原因 |
| --- | --- | --- |
| 知识组织 | 双索引证据图谱 | 纯题目百科重复原理；纯原理教材不利于按题进入 |
| 阅读入口 | GitHub Markdown | 自定义 Web UI 增加无关建设和维护成本 |
| 图谱存储 | YAML manifest + 稳定 ID | 图数据库对当前规模没有必要 |
| Go 组织 | 单 module | 多仓库或每题独立 module 会阻碍复用和统一验证 |
| 实现深度 | 原理验证级 | 生产原型会把范围变成 75 个产品工程 |
| 组件策略 | 标准库优先，再用 adapter 对照 | 只造轮子失去工程现实；只调组件无法暴露原理 |
| 正式发布 | 公开开发，v1.0 全范围门禁 | 隐藏开发不利于持续审阅；过早 release 会稀释“全覆盖”承诺 |
| 许可 | Apache-2.0 | 单一许可比代码 / 文档双许可更易维护，并提供明确专利条款 |

## 15. 风险与缓解

- **范围爆炸**：用固定 scope contract、共享 primitives 和独立工作包控制；
- **文档浅而多**：complete 状态要求必填推导链、主张标签和实验映射；
- **实验结论被过度外推**：记录环境和边界，禁止把本地指标包装为生产容量；
- **性能测试不稳定**：CI 只做 smoke 和正确性，深度性能采用同机相对比较；
- **外部组件拖垮开发体验**：标准库核心实验独立运行，adapter 使用独立门禁；
- **来源随时间过期**：记录访问日期，优先论文和官方文档，定期审阅 SOURCED 主张；
- **生成内容漂移**：PR 校验生成结果与 manifest 一致；
- **安全或隐私风险**：实验只使用合成数据和本地资源，不提交凭据或真实用户数据。

## 16. 公开来源基线

题型范围在 2026-07-14 交叉核对以下公开目录：

- [ByteByteGo — System Design Interview](https://bytebytego.com/courses/system-design-interview)
- [Educative — Grokking Modern System Design Interview](https://www.educative.io/courses/grokking-the-system-design-interview)
- [Hello Interview — System Design Problem Breakdowns](https://www.hellointerview.com/learn/system-design/in-a-hurry/problem-breakdowns)
- [System Design Primer](https://github.com/donnemartin/system-design-primer)
- [Google SRE Book — Table of Contents](https://sre.google/sre-book/table-of-contents/)
- [AWS Well-Architected — The Six Pillars](https://docs.aws.amazon.com/wellarchitected/latest/framework/the-pillars-of-the-framework.html)

这些来源用于核对题型覆盖和工程维度，不作为可复制内容。仓库文档必须重新推导，并遵守各来源许可和引用边界。

## 17. 设计完成状态

开始 W0 前没有未决的项目级设计问题。W0 和后续工作包中的局部 API、schema 细节、具体原理清单和实验参数，应在各自子规格中明确并单独审阅，不得绕过本设计的范围与发布契约。
