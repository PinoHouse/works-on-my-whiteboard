# 批处理数据流水线

## 表面题目

设计批处理数据流水线，表面是调度一组任务读取分区、执行转换并把结果写到目标目录。真正的状态变化是一次 run 固定输入快照、代码、配置和依赖，产生完整 output manifest，经质量门后把可见数据集指针从旧版本原子切到新版本。成功不是所有 task 都返回零，也不是目录里出现文件，而是任一 published version 都能追到统一 lineage，失败重试不会把不同 attempt 或输入版本混成一个结果。批处理的“进展”由版本化运行与发布边界定义，不是流式 offset。

## 反问与边界

先问是每日全量、分区增量还是历史回填，输入是否有可固定 snapshot，产出 SLA、允许陈旧多久、质量检查和 schema 契约由谁批准；再问失败能否重跑、下游是否支持版本读取、回滚保留多久。容量按输入总字节、扫描、shuffle 比例、中间物化、最大 partition、worker 配额和 backfill 范围表达，不能只用行数或 task 数。还需定义敏感数据权限、区域驻留、临时产物删除与多租户资源公平。

排序域只在 input file/partition 或显式业务 sort key 内成立，批完成不创造全局 event order。重放边界是仍保留的 input manifests、代码/依赖 artifact 和运行配置；只保存 SQL 文本不足以重建。背压在 scheduler 并发配额、集群资源队列、shuffle spill、目标写带宽与发布 gate。state owner 是 catalog 中的 run/output manifest 和各 task attempt 私有路径；scheduler 仅触发，不是数据事实 owner。重复来自 task retry 或不同 attempt 追加同目录，丢失来自非快照输入、临时产物提前删除和部分发布。

## 客观模型

最小对象为 `InputManifest(id,files,schema,watermarks)`、`Run(id,code_digest,config,dependencies,input_manifest,status)`、`TaskAttempt(run,task,attempt,outputs)` 与 `OutputManifest(version,files,quality,lineage)`。接口包括 `StartRun(input_manifest,code_digest,config)`、`CommitTaskAttempt`、`ValidateCandidate` 和 `Publish(expected_current,new_manifest)`。task 写私有 staging，run coordinator 聚合 content-addressed files，catalog 条件切换 `current`。

不变量是：published dataset 的所有文件来自同一 output manifest；manifest 可追到固定输入、代码、配置、依赖和质量结果；失败 run 不改变 current；旧 attempt 不能覆盖新 attempt；回填产生新版本而非无痕改写。输入字节 `D`、shuffle 比例 `f`、复制或中间写放大 `A` 时，一次运行 I/O 近似 `D×(1+f×A)`。最大 partition 为 `D_max`、平均为 `D/n` 时，倾斜比 `D_max/(D/n)` 限制有效并行度，增加 worker 不能拆开单 reducer。

批处理可对 task 至少一次执行，只要输出以 attempt 私有路径或内容摘要去重，读者仍只见一次完整 manifest。input snapshot 的文件顺序不是业务时间顺序；需要排序的产出必须显式声明 key 和范围。下游读取 current 指针获得稳定版本，不能在一次查询中自动混读两代。

## 必然约束

[DEDUCED:batch-data-pipeline-replayability-requires-versioned-lineage] “某天跑过一段 SQL”没有固定输入文件、代码摘要、配置、依赖和运行时语义，无法唯一决定结果。最小反例是同一 SQL 在 M1 与补录后的 M2 上行数不同，依赖 UDF 升级又改变舍入；仅按日期重跑无法解释差异。因此 lineage 必须联合版本化这些输入。若源本身不可快照，只能声明 best-effort 时间范围，不能承诺精确重放。

[DEDUCED:batch-data-pipeline-atomic-publication-is-not-atomic-computation] 计算可以分成一百个 task 并各自重试，但读者需要知道这百分之百文件是否属于同一次候选。反例是 attempt A 写到 60/100 文件后失败，B 以修复输入追加 100 个文件；按目录 glob 读到 160 个新旧混合文件。每个 attempt 应写私有 staging，完整检查后单次切换 output manifest。原子 publication 不要求整个计算成为一个事务，只要求可见性有一个仲裁点。

[DEDUCED:batch-data-pipeline-backfill-is-a-new-versioned-decision] 回填历史输入会改变受影响分区、聚合和下游派生，无法从“任务成功”判断所有消费者是否接受新口径。最小反例是修复一日汇率后，月报与账单累计同时变化；原地覆盖只通知一个表会造成跨数据集不一致。回填必须声明范围、生成新版本、比较差异并保留回滚。只有没有下游审计且结果可随时覆盖的临时数据，普通补跑才足够。

## 从简单方案演进

基线是单机读取固定输入、写一个临时文件，完成后 rename 为结果；对小数据简单且 publication 清楚。输入超过单机资源后拆 task 并引入 shuffle、attempt 私有路径与 output manifest；它解决并行和重试，却新增倾斜、spill、straggler 与清单协调。当最大 reducer 时长超过中位数三倍，或 shuffle spill 超过本地磁盘预算 70%，先分析 key skew，采用 salting、预聚合或热点 key 独立处理，单加 executor 不会线性扩展。

历史修复出现后，引入不可变 input snapshot、完整 lineage、候选版本比较和 current pointer。当 backfill 影响分区超过全表 10%，或质量失败使发布延迟超过下游 SLA 的 50%，分批产生版本化候选、冻结受影响消费者并要求数据契约审批，不暴露失败 run 的部分文件。频繁全量扫描超过 SLA 后，才考虑增量 materialization，同时保留可全量重建路径和变更捕获边界。

三倍、70%、10% 和 50% 是待基准测试、回填演练与下游风险校准的初始参数。提前拆热点或冻结下游增加成本，延后则扩大 straggler 与错误发布风险。反选“原地覆盖最新目录”仅在输入可丢、无审计回滚且存储真正支持事务目录替换时重新变优；分析、计费和监管数据需要不可变版本。

## 设计决定

本设计由 catalog 固定 input manifest、代码 digest、配置和依赖后创建 run。每个 task attempt 写私有 content-addressed 输出并提交摘要；coordinator 只接纳当前 attempt generation。所有 task 完成后构建 candidate output manifest，执行 schema、完整性、业务阈值与旧版本差异检查；通过后用 `expected_current` 条件更新一次指针。读者始终固定一个 manifest 读取。

重试 task 不直接 append 公共目录；相同内容可复用，不同 attempt 不互相覆盖。scheduler 与集群队列提供 tenant 并发、CPU、内存、shuffle 和目标写信用，形成批式背压；发布 gate 过载时候选等待，不让半成品泄露。失败 run 保留诊断 lineage，按保留期清理 staging。排序仅对声明 key 执行，不能以 run 完成时间替代 event time。

未选择跨所有 task 的分布式事务，因为长运行会锁资源且任一 worker 故障使协调脆弱；私有输出加原子 manifest 能把事务边界缩到一次元数据切换。低数据量单文件任务仍可使用临时文件 rename，不强迫引入完整分布式执行。

## 运行与演进

SLI 包括 run 排队/执行/发布延迟、task retry、straggler 比、shuffle spill、input/output 字节、质量失败、candidate 与 current 差异、backfill 范围和下游版本陈旧。看板按 dataset、run 与 tenant 拆分，不能用 task 成功率掩盖 publication 未完成。临时路径、manifest 和 current 都需授权，敏感列变换与导出保留 lineage。

故障时间线：0 min，scheduler 固定 M1、代码 C7 和 run R1；20 min，task 43 重试但输出在 attempt 私有路径；40 min，R1 质量检查失败，current 仍指 V19；60 min，以 M2/C8 建 R2，产出 V20 candidate 并对 V19 比较；80 min，条件 pointer swap 发布 V20，V19 仍可回滚。若 swap ACK 丢失，读取 current 判断结果，不重复创建混合目录。

schema 升级先发布兼容 reader，再让候选双写旧新字段，验证下游后切 manifest；回滚指向 V19 而非重新计算。input retention 缩短前审计哪些版本仍需重放。跨区域复制按完整 manifest 验证文件可达，单独复制 current 指针而缺文件会产生假发布。

## 面试考察本质

给定“每个已发布数据集版本都能从固定输入、代码和检查结果重放解释”的不变量，因为单个 task 成功或目录存在无法证明所有文件来自同一 run，候选人应推导出不可变 input/output manifest、attempt 隔离、质量 gate 和原子 pointer publication。主导取舍是全量重算与回滚能力、物化 I/O、发布延迟和回填协调，不是流式 offset 或 exactly-once。

优秀信号包括写出 I/O 放大和 skew 比、区分计算重试与读者可见性、让 scheduler 不冒充数据 owner、保留 failed run lineage，并把 backfill 当新版本决策。常见误区是直接写最终目录、以 task 数量估算并行度、覆盖旧分区不通知下游，或只保存 SQL 文本就声称可重现。

二十分钟回答应完成 snapshot、run、task 和 manifest；四十分钟加入 shuffle 倾斜、attempt 重试、质量 gate 与 pointer swap；六十分钟再讨论增量化、回填审批、schema 迁移、区域复制和成本。追问可用 60 个旧文件加 100 个新文件的反例，要求说明排序域、重放边界、背压、state owner、丢失与重复边界。
