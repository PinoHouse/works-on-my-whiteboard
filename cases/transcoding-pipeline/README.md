# 转码流水线

## 表面题目

设计转码流水线，是把一个不可变媒体源经过探测、解码、切片、多个编码档、音轨/字幕处理、质检和封装，最终发布成一组可播放产物。成功不是某个 worker 返回 0，而是当前 DAG revision 的所有必需节点都有可验证产物，质量策略通过，并由一个 release manifest 原子暴露。通知、计费和回调是 release 之后可重试且可对账的外部效果，不得由 worker 直接执行并据一次超时猜结果。队列重试、推测执行和 worker 迟到都不能让旧结果污染新发布。

题目重点不在播放分发，而在依赖感知的计算、资源异构、attempt fencing 和部分完成隔离。源 generation、配方/DAG revision 与处理器版本共同决定输出身份；源换版或配方升级产生新运行。可选字幕失败可按策略降级，主视频缺档是否阻塞发布必须在 job 契约中声明。

## 反问与边界

先问日输入时长、分辨率/编码分布、输出梯度、交付 deadline、实时倍速、GPU/CPU 类型、失败率、优先级和成本。是上传后分钟级点播、直播实时转码还是离线归档会决定调度；本文聚焦上传后异步。还要问哪些产物必需、质检是自动还是人工、重复编码能否接受、源或 recipe 更新如何取消旧 job、是否允许先发布低清再扩展 manifest。

若输入时长 `H`，每个 stage 的处理速率为 `speed_i` 倍实时、并行份数 `k_i`，理想计算时长约关键路径上的 `Σ H/(speed_i×k_i)`，实际还加排队和数据搬运。输出存储为 `H×Σri/8`，中间产物若不流式复用可能再放大一至数倍。十个 rendition 不是十倍 wall time 的必然值，但若各自完整解码，则解码 I/O 和计算会重复，DAG 共享父产物可减小放大。

## 客观模型

实体为 `SourceGeneration`、`PipelineDefinition(revision)`、`Job(job_id,input_digest,revision)`、`TaskNode(dependencies,resource_class)`、`Attempt(attempt_no,fence_token,lease)`、`Artifact(digest,producer_attempt)`、`ReleaseManifest` 和持久 `ReleaseEffectOutbox`。DAG revision 冻结节点、边、参数、必需/可选标志和处理器镜像。节点只有所有当前父 artifact 成功且摘要可读时进入 runnable；调度器发 attempt 租约与单调 fencing token。

attempt 输出先写不可变临时 artifact，再向任务状态机提交 token、输入父摘要与输出摘要。只有当前 attempt token 可 CAS 任务为 succeeded；旧 attempt 的字节可清理但不能成为下游输入。Job 在必需叶子完成后进入 VERIFYING，校验时间轴、音视频同步、关键帧和 manifest 引用，最后在一个事务中提交 release 并插入所需 outbox 行。每行 effect key 固定为 `(job_id, revision, effect_type)`；dispatcher 在提交后才发送通知、计费或回调，接收端以该键幂等，或发送端通过查询状态与对账闭合未知结果。worker 无权直接触发这些外部副作用。下游 key 包含源 digest、DAG revision、父 artifact digest 和处理器版本，可安全复用真正相同结果。

队列年龄而非队列长度更接近 deadline 风险；一段八小时 8K 视频与一百个短音频不能等价计数。资源模型同时跟踪预计 GPU 秒、CPU、显存、临时存储和读写带宽，按租户与 deadline 公平准入。未知运行时可从保守估计开始，但不能声称为已测数据。

## 必然约束

[DEDUCED:transcoding-pipeline-dag-dependencies-define-runnable-work] 封装节点依赖视频片、音轨和时间轴元数据。若调度只看消息到达，视频完成消息先到便运行封装，而音轨仍缺失，产物可能结构合法却无声。父节点状态与 digest 集合才定义可运行条件，队列顺序不表达因果。可选依赖可以策略化省略，但必须在 revision 中明确，不能由 worker 超时自行猜测。

[DEDUCED:transcoding-pipeline-attempt-fencing-must-reject-late-workers] attempt 1 超时后 attempt 2 用新编码器完成并提交；若 attempt 1 稍后恢复且能覆盖任务路径，下游会读到与已验证摘要不同的字节。仅“最后写胜”甚至可能让更旧工作胜出。每次重派提升 fence token，状态机只接受当前 token，artifact 路径不可变且带 attempt 身份，迟到结果只能作为垃圾回收对象。

[DEDUCED:transcoding-pipeline-release-commit-must-atomically-record-external-effects] 五个 rendition 完成四个时不能发布；必需项完整并质检通过后，若先提交 release、进程再崩在写“计费/通知待办”之前，媒体已经可见却永久漏掉副作用。反过来先通知再提交，release 失败会产生幽灵账单。故 release commit 必须与持久 outbox 同事务，effect key 为 `(job_id,revision,effect_type)`；外部成功响应丢失只能记 UNKNOWN，以幂等接收、查状态或对账闭合，不能当失败重复制造效果。

## 从简单方案演进

最简单正确基线是单 worker 串行执行固定脚本，最后移动完整目录。低量时故障面最小。当交付 `p95` 超过 deadline 的 50%，且不同 rendition 可独立运行时拆成 DAG 并并行；代价是依赖状态、数据搬运和部分失败。若同一解码被三档以上重复且解码占计算 30%，共享中间父产物；中间存储和版本兼容随之增加。这些比例须由真实 profile 校准。

队列年龄超过最短 deadline 的 25% 或某资源利用率持续 70% 时，加入资源分类、预测与按 deadline/租户调度；若失败重试浪费 GPU 秒超过 5%，先修故障分类和 checkpoint，再考虑推测执行。推测执行只用于长尾且必须 fencing，否则优化尾延迟会破坏正确性。第二组待压测指标是 artifact 远程搬运何时超过编码时间，以及 chunk 粒度如何影响恢复工作量与调度开销。

未选择每个 stage 通过无状态队列随意消费，因为依赖、取消和版本难以表达；只有完全线性、每步幂等且顺序固定时重新变优。未选择始终预留整套峰值 GPU，若交付价值极高且负载稳定，它可换取可预测 deadline；当前按 admission、优先级和弹性容量折中。

## 设计决定

提交 Job 时冻结源 digest 与 DAG revision。协调器持久化任务状态，依赖满足才发带 token 的租约；worker 续租、写 attempt 专属不可变 artifact、校验后提交。失败分为确定性输入/配方错误、可重试基础设施错误和资源不足，只有后两类退避重试；取消新 revision 会提升 job generation，使旧 worker 提交被拒。release verifier 从权威任务状态读取摘要，写 manifest 临时对象，并在同一事务中提交 release 与按 effect type 去重的 outbox 行。

独立 dispatcher 锁定 outbox 行并携带 `(job_id,revision,effect_type)` 调用接收端；worker 不直接通知、计费或回调。明确 4xx 业务拒绝可终止并告警，传输超时或成功响应丢失则记 UNKNOWN：接收端支持 effect key 幂等时可用同键重试，否则发送端先查远端状态并由周期对账决定补发。只有证实未发生才标记失败，单次 ACK 丢失不构成这项证据。

过载从低可见到高可见：暂停低价值预计算与历史重转码；延后可选字幕/预览；减少最高码率；优先完成接近发布的 job；按租户排队新 job；最后拒绝低优先级输入。不得跳过摘要、依赖或 attempt 校验。反选让 worker 自行发现后继并直接触发，因为重复和权限扩散难控；在很小的纯函数 DAG 且状态可由对象存在性完全推导时，它才更简洁。

## 运行与演进

观察 job 交付时延、各 stage queue age/运行时、关键路径、GPU/CPU 利用、租约超时、fence 拒绝、重试浪费、artifact cache 命中、部分完成年龄、质检失败、发布原子性、outbox age、UNKNOWN 外部效果数、effect-key 去重命中和对账不一致。待校准两组门槛：队列年龄/预计剩余时间达到 deadline 多少比例触发扩容或拒绝；outbox UNKNOWN 多久转人工/对账，以及接收端去重保留期至少覆盖多长重试窗口。

故障时间线：0 分钟 job J revision 8；10 分钟 encode attempt 1 租约丢失；11 分钟 attempt 2 token 22 开始；15 分钟 2 成功并使下游 runnable；16 分钟 1 token 21 迟交，被 fence，不能覆盖。随后四个必需叶子中一个缺失，release 保持不可见；补齐、质检通过后，事务同时提交 manifest R8 与 callback/billing outbox。dispatcher 发出 callback，接收端已成功持久化但成功响应丢失，发送端把效果记 UNKNOWN 而非失败；它用同一 effect key 重试或先查询状态，接收端不重复回调/计费，对账最终把该行闭合为 delivered。

处理器升级先固定一组源双跑 revision 8/9，比对时长、帧率、音画同步、视觉指标和计算成本；再影子生产、少量标题发布。回滚切 release 指针回 R8，但保留 R9 审计。worker 在沙箱中读取最小权限源、写 attempt 前缀；租户配额按预估 GPU 秒和临时存储而非 job 数，防止超长 8K 任务垄断。

## 面试考察本质

本题本质是：给定“只有当前 DAG revision 的完整、经验证依赖闭包才能发布，发布后的外部效果又必须与该事实一致”，因为 worker 是否仍在运行、长尾何时结束、异构资源何时可用以及远端是否已执行都不可立即获知，候选人应推导依赖状态机、租约加 fencing、不可变 artifact、release+outbox 原子提交与 UNKNOWN 对账，并在并行、复用、deadline 和重复计算间选择。

优秀回答会画 DAG 而不是组件列表，算关键路径与 `H×Σri`，构造 attempt 1 迟交覆盖 attempt 2 和外部成功响应丢失的反例，并区分 task success、release success 与 effect outcome。常见误区是把消息到达当依赖、让 worker 直接计费/回调、把 ACK 丢失当失败、用队列长度代表工作量、少一档也直接改清单，或只谈扩 GPU 不做 admission。

二十分钟完成 job/task/attempt 模型、容量和发布；四十分钟加入资源调度、缓存复用、故障与降级；六十分钟讨论推测执行、版本迁移、质量验证和多租户。考察点不在知道某工作流引擎，而在能否证明哪个 attempt 有写权限、哪些父产物属于同一 revision，以及为何半套“都成功了大部分”的输出仍不是产品成功。
