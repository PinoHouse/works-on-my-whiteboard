# 流处理

## 表面题目

设计流处理系统，表面是持续读取事件，经 map、join、window 或聚合后写出结果。真正的状态变化是 source offset、按 key 的事件时间状态、timer 与输出 epoch 被一个完整 checkpoint 共同固定，并在故障后从一致边界恢复。成功不只指每秒处理多少事件，还包括乱序输入按声明的 event time 解释、下游变慢时状态仍有界，以及恢复不会把不同 checkpoint 的输入位置和算子状态拼接。外部 sink 是否只产生一次副作用是独立问题，不能由“框架有 checkpoint”自动推出。

## 反问与边界

先问计算是无状态映射、窗口聚合、双流 join 还是会话；业务时间由谁产生，允许多大乱序和迟到，窗口何时 provisional/final；再问 source 是否可重放、sink 是否支持幂等或事务、状态大小、恢复时间与新鲜度 SLO。容量按每 key 事件率、窗口与 lateness、state 字节、shuffle、timer、checkpoint 大小和热点 key 估算，不能只看总输入吞吐。

排序域是每 input partition 与同 key 的算子序列，跨 key 只有显式 window/watermark 关系。重放边界是最后完整 checkpoint 保存的 source offsets 之后；未完成 checkpoint 不可与新状态混用。背压从 sink 经 operator、shuffle、source credit 逆向传播。state owner 是 keyed state backend、source split owner 与 checkpoint coordinator；外部 sink 仍拥有其副作用。丢失来自无可重放 source 或明确 late/drop 策略，重复来自 checkpoint 后的输入重放和 sink ACK 未知。

## 客观模型

最小模型包含 `SourceSplit(offset,watermark)`、`Operator(key,state,timers)`、`Checkpoint(epoch,source_offsets,state_handles)` 与 `SinkCommit(epoch)`. coordinator 发 barrier，所有 source 位置和 operator state 完整后才把 epoch 标为完成；worker lease 与 key-group assignment 决定当前 state 写 owner。watermark 是各活跃 source 进展的保守函数，idle source、时钟异常和 allowed lateness 都是其政策组成。

不变量是：每个 key 的窗口结果只按声明 event-time、lateness 与逻辑版本解释；恢复使用同一个完整 epoch 的 offsets 与 state；旧 assignment 不能提交新 checkpoint；state 只有在 watermark 越过清理边界后释放；sink 输出携带 epoch 或 event ID。对 key k 的事件率 `λ_k`、窗口 `W`、lateness `L`、每事件状态 `s`，状态下界约为 `Σ_k λ_k×(W+L)×s`。checkpoint 大小 `C`、有效写带宽 `β` 时，基础时长至少 `C/β`。

热点 key 形成单 owner 串行瓶颈，增加 worker 不能自动拆开不可结合的状态。若 checkpoint duration 接近 interval，barrier 持续堆积并放大恢复点距离。处理时间只能说明事件被机器看见的时刻，不能替代 event time；watermark 也只是运行时选择的进展界，不证明现实世界绝不再产生旧事件。

## 必然约束

[DEDUCED:stream-processing-event-time-progress-requires-a-watermark-policy] 无界流没有最后一条记录，乱序 source 也不能从当前处理时间推出某窗口已完整。最小反例是 P1 已到 10:10、P2 暂停在 10:00；若按 P1 清理 10:00 state，P2 后到的 10:01 合法事件被遗漏，若无限等 P2，所有 key 状态增长。系统必须声明 watermark 合并、source idle、allowed lateness 和 late side output。更晚关窗提高覆盖但增加状态与新鲜度延迟。

[DEDUCED:stream-processing-checkpoint-consistency-is-separate-from-external-effect-exactly-once] checkpoint 能原子关联 source offset 与 operator state，却不能回滚普通 HTTP、邮件或未参与协议的数据库写。最小反例是 sink 已写 epoch 42，ACK 丢失后 worker 崩溃，从 42 恢复并再写一次。只有 sink 按 checkpoint epoch 幂等提交，或参与两阶段事务，外部结果才可去重；否则应声明至少一次。框架内部的一致恢复不是端到端 exactly-once。

[DEDUCED:stream-processing-backpressure-preserves-bounded-state-by-slowing-admission] 当 sink 率 `μ` 小于 source 率 `λ` 时，若入口继续接受，队列在 `Δt` 增长 `(λ-μ)×Δt`，无界时间最终耗尽任何有限内存或磁盘。背压必须降低 source credit 或使用业务明确的丢弃、采样与溢出存储。简单“多加 buffer”只延后故障。对不可暂停 source，入口日志 retention 本身就是有限重放预算，越界必须显式处理。

## 从简单方案演进

基线是无状态逐条处理、失败后从 source offset 重放；简单但对有状态窗口会重复或丢失。当需要窗口和 join，加入 keyed state 与周期 checkpoint；它解决一致恢复，却增加状态上传、barrier 对齐和恢复时间。当 checkpoint `p99` 超过 interval 的 60%，或 alignment time 超过端到端延迟预算的 30%，减少状态、改增量或 unaligned checkpoint、重分配热点 key，而不是盲目缩短 interval。

乱序导致窗口错误后，引入 event-time watermark、idle source 与 late side output。若 watermark lag `p99` 超过新鲜度目标的 50%，或 late-event 比率超过 1%，先检查 source idle、时钟和分区倾斜，再调整 lateness 或修正流。sink 变慢则让 credit 反向传播，非关键输出可采样，关键事实保留可重放；不能继续无界摄入并期待扩容及时到达。

60%、30%、50% 和 1% 是待回放压测、故障注入与产品风险校准的初始策略参数。较低阈值增加 checkpoint I/O 和状态保留，较高阈值扩大恢复与迟到风险。反选“按处理时间微批”在 event time 不重要、乱序很小、只做近实时近似监控时重新变优；归因、会话和合规窗口不能借此宣称正确。

## 设计决定

本设计按业务 key 分配 key-group，source 使用可重放 offset，operator state 与 timer 存入版本化 backend。checkpoint barrier 完整覆盖所有活跃 split 后才提交；恢复只使用最后完整 epoch。watermark 基于各 source 进展并有显式 idle timeout，late event 进入可观察 side output 或版本化 correction。sink 优先采用 epoch 幂等提交，普通外部调用沿稳定 event ID 至少一次重试。

背压从 sink 写队列经 operator mailbox 和 shuffle credit 传播到 source；租户和作业有独立 state、checkpoint 带宽与恢复优先级。超时的 checkpoint 被废弃而不部分发布；worker 失联后旧 lease 被 fence。过载先暂停历史 replay、降低非关键采样输出，再限制 source admission；关键窗口 state 不能随机淘汰。

未选择所有算子共享全局锁和总序，因为无关 key 会被一个协调点串行化；只有所有事件确需统一顺序且吞吐低时才适合。也未默认两阶段 sink 事务，因为长事务和外部系统可用性成本高；支持稳定幂等键时通常更稳健。

## 运行与演进

SLI 包括 input/output rate、每算子 backpressure ratio、mailbox 与 shuffle 队列、watermark lag、late/drop 比率、keyed state 字节、热点 key、checkpoint duration/alignment/failure、恢复时间与 sink duplicate。端到端新鲜度要按 event time 测量，不能用处理器 CPU 空闲代替。多租户按 state、shuffle、checkpoint 带宽隔离，敏感 state 加密并按 retention 删除。

故障时间线：0 s，checkpoint 42 barrier 到 source A/B；10 s，A 已快照 offset 700 和 state，B 因慢 sink 被背压；20 s，B 完成，coordinator 原子完成 42；25 s，sink 提交 epoch 42；30 s，worker 崩溃，从 42 的 offsets 与 keyed state 恢复。若 sink ACK 丢失，以 epoch 42 查询或幂等重试；普通 HTTP 则明确可能重发。未完成的 43 不参与恢复。

state schema 升级先让新代码双读旧新版本，在 savepoint 上影子恢复并比对，再小流量切换；回滚保留旧 serializer。改 key 分区需要停止或版本化路由，保证同一 key 不被两个 owner 并写。区域灾备必须复制 checkpoint handles 和 source 重放位置，单有代码并不能恢复状态。

## 面试考察本质

给定“每个 key 的结果必须按声明事件时间并从一致 checkpoint 恢复”的不变量，因为运行时不知道慢 source 是否仍会产生旧事件，也不能把内部 snapshot 自动扩展到外部副作用，候选人应推导出 watermark、state 生命周期、背压传播与 sink 提交边界。主导取舍是结果新鲜度、状态大小、恢复时间与迟到正确性，而不是笼统声称 exactly-once。

优秀信号包括写出 `Σλ_k(W+L)s` 和 `C/β`、区分 event time 与 processing time、说明 idle source、只从完整 epoch 恢复，并定位 sink ACK 丢失重复。常见误区是 watermark 等同真实时间、checkpoint 越频繁越免费、buffer 能吸收永久过载，或跨 key 默认全局有序。

二十分钟回答应完成 source、operator、state 和 checkpoint；四十分钟加入 watermark、late policy、backpressure 与幂等 sink；六十分钟再讨论热点 key、state 迁移、区域恢复和 schema 演进。追问可沿 P2 停在 10:00 与 epoch 42 ACK 丢失展开，要求说明排序域、重放边界、state owner、背压位置和丢失/重复。
