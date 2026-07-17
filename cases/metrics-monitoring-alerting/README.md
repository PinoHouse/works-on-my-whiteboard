# 指标、监控与告警

## 表面题目

设计指标、监控与告警系统，表面流程是采集样本、查询图表、阈值超限时通知。真正的状态包括由完整 labelset 标识的时间序列、规则版本、固定求值时间网格，以及每个 alert fingerprint 的 `PENDING/FIRING/RESOLVED` 状态和通知去重。成功不只是“写入了数值”，还要在基数预算内保持可查询，并让重启、缺样和通知超时不会把同一事故拆成无限页面或错误恢复。指标适合受控低维聚合，不是任意请求字段的廉价索引。

## 反问与边界

先问采集是 pull 还是 push、采样周期和允许乱序、长期保留分辨率、查询 SLO、租户基数上限，以及告警 `for`、抑制、分组、恢复和升级路径。目标要区分样本接受率、查询新鲜度、检测延迟、通知接受和人工响应；通知 2xx 不证明值班人看见。容量必须按 active series、series churn、每样本字节、规则匹配 fingerprint 数和查询扫描范围表达，不能只看 metric name 数。

排序域是单 series 的样本时间与规则求值网格；不同 series 没有全局顺序。重放边界是样本 retention、规则版本和持久 alert state；历史样本重算可能生成新 alert generation，不能冒充当时已通知。背压位于 agent buffer、ingest 配额、索引写、压缩、查询和 evaluator；owner 分别是 TSDB shard 与 alert state store。丢失来自 agent 缓冲溢出或拒绝写入，重复来自样本重试和通知 ACK 未知，缺样则必须保留 unknown 语义。

## 客观模型

最小接口为 `WriteSamples(tenant,series,samples)`、`Query(expr,time_range)`、`PutRule(version,expr,for,labels)` 与 `Evaluate(grid_time)`。series identity 是 `(tenant,metric,canonical_labelset)`；alert instance identity 是规则版本与结果标签形成的 fingerprint。TSDB shard 拥有样本接受、乱序和保留状态；evaluator/state store 拥有 `for_timer`、抑制、恢复与 notification key；面板只是派生视图，通知渠道不拥有告警真相。

不变量是不同 labelset 不能错误合并；同一 fingerprint 在同一 incident generation 只进入一次 firing；重启恢复后 `for` 计时不归零也不凭缺样推进；样本 stale 与数值零分开；租户不能通过标签查询越权。若 n 个独立标签的值域分别为 `L_i`，潜在 series 基数近似 `S=∏|L_i|`，而不是标签数之和。规则每 `e` 秒求值、匹配 `K` 个 series、需读取 `H` 个样本时，读取与状态工作至少随 `K×H/e` 增长。

高 churn 即使 active series 暂时稳定，也持续制造索引、块和规则 fingerprint。热点不是单个写 key，而可能是扫描百万 series 的聚合规则。样本重试可按 series 与 timestamp 去重或覆盖，但通知端点收到请求后响应丢失会产生未知，必须用稳定 notification key；人工是否看见仍是外部信息。

## 必然约束

[DEDUCED:metrics-monitoring-alerting-cardinality-is-a-state-budget] 每个新 labelset 都创建独立 series identity、索引项、head state 和可能的 alert fingerprint。最小反例是原有 `service,code` 只有数十组合，加入每秒一万个新值的 `request_id` 后，一小时产生三千六百万 series；再快的查询优化也无法消除要标识和保留这些实体的状态。结论是标签必须有值域预算，高基数请求 ID 应进入日志或 trace。只有值域固定且受控的维度才适合指标切片。

[DEDUCED:metrics-monitoring-alerting-alert-state-is-not-a-stateless-threshold] `for=5m` 要知道条件从何时连续满足，抑制和恢复要知道 incident 是否已打开，通知重试要复用同一 key。最小反例是规则在第 4 分钟 evaluator 重启；若只看当前超限就立即通知会提前，若重新计时则延迟，若每次求值都通知会页面风暴。因此 alert fingerprint 状态必须持久恢复，并与规则版本和求值网格绑定。

[DEDUCED:metrics-monitoring-alerting-scrape-gap-is-not-zero] 某分钟没有样本可能是目标正常无事件、采集器断网、标签改变、写入拒绝或查询延迟，现有信息不能唯一推出数值为零。反例是事故中采集网关失联，错误率查询把 missing 填零，面板反而显示恢复。正确设计保留 stale/unknown，并用独立 target-missing 规则处理。只有契约明确“无上报即零”的事件计数才可补零。

## 从简单方案演进

基线是单节点按 metric name 存样本并即时阈值判断，适合少量固定 series。当 active series 超过内存或查询 `p99` 逼近 SLO，按 tenant 与 series hash 分片、分块压缩并下采样；它解决容量，却引入跨 shard 聚合、乱序和长期精度差异。当每租户 active series 超过配额的 80%，或十五分钟 churn 超过其基线两倍，拒绝新高基数 labelset、要求预聚合或受控 bucket，而不是随机丢旧 series。

告警从无状态查询演进为 fingerprint 状态机与持久 `for_timer`，再加入分组、抑制和稳定 notification key。当单规则 firing instances 超过通道处理量的 70%，或五分钟同根因页面超过 100 个，按服务和区域分组、保留根因与安全告警，抑制派生症状；恢复后发送摘要，不逐条重放。高成本规则可记录 rule evaluation lag 并按优先级隔离，不能让探索查询饿死核心告警。

80%、两倍、70% 和 100 个都是待负载测试、事故演练和 on-call 容量校准的策略参数。降低它们会更早拒绝维度或分组，可能损失诊断细度；提高则增加存储耗尽和告警风暴风险。反选“所有调试字段都做 label”仅在字段值域固定且很小、必须稳定多维聚合时成立，请求与用户 ID 应走日志或 trace。

## 设计决定

本设计在 agent 侧校验 metric schema、label 白名单和租户额度，样本以 series hash 写入 TSDB。每 series 明确乱序窗口与重复 timestamp 规则，超界样本进入质量计数而非静默改写。evaluator 在固定 grid time 读取带 stale 标记的数据，按规则版本更新 fingerprint 状态；`PENDING→FIRING` 与 notification key 在同一 state transaction 中提交，通知重试沿同一 key。

缺样默认 unknown，由规则显式选择保持状态、转 stale 或触发 missing 告警。通知成功只表示渠道接受，升级策略还需确认人工 ACK；二者分别观测。过载先限制探索查询和高基数新 series，再延迟非关键 recording rules，核心 SLO 与安全规则保留独立资源池。样本重放可重建面板，但不能自动补发历史页面，除非创建审计 replay generation。

未选择每次求值直接无状态发送通知，因为无法实现 `for`、恢复和去重。低价值一次性阈值 webhook、无需抑制与恢复时该方案才更简单。也不把 missing 当零，除非具体 metric 契约证明这就是业务含义。

## 运行与演进

SLI 包括样本接受/拒绝、active series、churn、每租户 head bytes、查询扫描量与 `p99`、rule evaluation lag、pending/firing 实例数、notification retry 和 target missing。告警系统必须监控自身写入与求值空洞，且使用隔离路径避免完全依赖已故障组件。成本按 series-hour、样本和查询 CPU 分摊；标签值可能含隐私，schema 层禁止用户 ID 等无界敏感值。

故障时间线：0 min，规则 R 对 fingerprint F 超限并持久为 `PENDING(start=0)`；2 min evaluator 重启，从 state store 恢复；5 min 在固定网格确认连续满足，原子转 `FIRING(key=K)`；5m+1s 通知请求超时，以 K 重试而不创建新 incident；10 min scrape gateway 失联，样本变 unknown，独立 missing 规则触发。演练验证重启不重置计时、通知不风暴、缺样不伪装恢复。

标签 schema 变更先影子计算基数上界和查询差异，小租户灰度后放量；回滚停止新 series，但旧块必须保留可读直到 retention 结束。规则升级创建新版本并迁移可兼容 fingerprint；语义改变时显式关闭旧 generation。区域灾备要同时恢复样本与 alert state，否则图表存在但页面状态丢失。

## 面试考察本质

给定“受控 labelset 的告警必须拥有可恢复的触发与恢复状态”这一不变量，因为系统不能从缺样推断零，也无法承受无限实体，候选人应推导出 series 基数预算、unknown 语义、规则状态机与通知去重。主导取舍是诊断维度、存储与求值成本、检测延迟和告警疲劳，不是简单阈值查询。

优秀信号包括写出标签值域乘积、区分 active series 与 churn、把 `for` 和恢复建模为状态、按 fingerprint 分组、说明通知接受不等于人已看见。常见误区是按 metric name 估容、把 request ID 作为 label、把缺样补零，或 evaluator 重启后无条件重发所有告警。

二十分钟回答应完成 series、采集、TSDB 和基本规则；四十分钟加入基数预算、状态机、缺样与通知重试；六十分钟再讨论查询隔离、下采样、区域恢复、租户成本和规则迁移。追问可用每秒一万个 request ID 与第 4 分钟重启，要求明确排序域、重放边界、背压位置、state owner 和丢失/重复语义。
