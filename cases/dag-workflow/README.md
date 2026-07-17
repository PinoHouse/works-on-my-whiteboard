# DAG 工作流

## 表面题目

设计 DAG 工作流系统，表面上是把任务节点和依赖边画成无环图，根节点完成后逐步调度下游。真正的成功语义是：某个不可变图版本的一次 workflow run 中，每个下游只消费本版本依赖的权威输出，重试、补跑和编辑不能混出一条从未存在过的执行路径。一个进程退出码为零，只能说明 attempt 结束，不能直接证明输出已经对下游可见。

本题区别于普通作业调度：核心不只是“何时触发”，而是依赖满足、关键路径、attempt 与 output commit。设计覆盖图发布、运行实例、动态重试、制品提交和取消；任务内部业务逻辑与大文件存储不在范围，但其提交身份必须参与协议。

## 反问与边界

先问图是否在运行中可编辑，补跑单节点时下游是否自动失效，失败策略是 fail-fast、继续独立分支还是允许人工跳过。节点输出是小状态、表分区还是对象集合；下游要原子看到全部输出还是允许流式读取。还要确认最大节点数、扇出扇入、运行并发、关键路径 SLO、单任务时长、资源类型和租户公平性。

期望状态是图版本、run 参数及每个 task instance 应达到的终态；观测状态包括依赖 commit、attempt epoch、worker lease、临时输出和资源使用。workflow reconciler 拥有 task readiness 与重试决策，artifact commit 记录拥有可见输出，worker 只拥有当前 attempt 的临时执行权。若图编辑改变语义，必须产生新 graph version，而不是原地改边后继续解释旧 run。

非目标是跨任意外部系统提供一个大事务。我们承诺状态机和输出引用一致；外部副作用需稳定 task identity、fencing 或补偿。SLO 分为 workflow 完成时间、关键路径排队、ready-to-start、失败确定时间和补跑成本，不能用全部节点平均时长掩盖一个慢扇入。

## 客观模型

实体为 `GraphVersion(nodes, edges)`、`WorkflowRun(graph_version, run_epoch)`、`TaskInstance(task_id, dependency_versions, state)`、`Attempt(attempt_epoch, lease)` 和 `OutputCommit(manifest_digest, producer_epoch)`。状态为 `BLOCKED -> READY -> RUNNING(epoch) -> COMMITTED|FAILED|CANCELLED`。worker 先写以 `{run, task, epoch}` 命名的临时输出，再由条件 `CommitOutput` 原子发布 manifest 并把 task 标记完成；对象路径本身不是完成证据。

依赖计数由权威 output commit 驱动，事件可重复消费。对节点 `v`，最早完成下界是 `finish(v) ≥ max(finish(parent))+queue(v)+run(v)`；全图时间至少是最长路径权重。若每节点失败概率为 `q`、关键路径有 `k` 节点，不考虑重试时一次全过概率为 `(1-q)^k`。把非关键节点并行度从 10 加到 100 不改变串行关键路径，反而可能争抢其资源。

不变量是同一 task instance 最多一个 output generation 成为权威；READY 必须对应固定 graph version 的全部依赖；下游输入 manifest 必须能追溯到具体父 task commit；旧 attempt 不能覆盖新 attempt 或删除其制品。旧 lease worker 返回时，临时对象保留到垃圾回收，状态和 manifest commit 因 epoch 不匹配而拒绝。

## 必然约束

[DEDUCED:dag-workflow-readiness-must-bind-graph-version] 任务就绪必须绑定不可变图版本及全部依赖的权威完成版本，否则编辑、补跑与迟到事件会让下游消费不相容输出。反例是 v1 中 C 依赖 A、B；A 完成后用户发布删除 B 依赖的 v2，迟到的 A 事件让旧 C 被判 READY，再与 v2 参数运行。结果不属于 v1 或 v2。固定 run 的 graph version，并以父 output commit 集合判就绪才能复现。

[DEDUCED:dag-workflow-attempt-output-needs-an-authoritative-commit] attempt 写出临时文件不等于任务完成，只有当前 task epoch 的输出提交记录才能把一组制品原子暴露给下游。W1 写了 9/10 个分片后失租约，W2 重算全部十个；若下游按目录存在性扫描，会拼接 W1 与 W2。以 manifest digest 作为单一发布点且校验 producer epoch，可在对象存储弱列表语义下仍得到一组确定输入。

[DEDUCED:dag-workflow-critical-path-bounds-completion] 增加非关键分支并行度不能缩短关键路径下界，扇出只会在共享资源不足时进一步放大排队与重试。A→B→C 各需十分钟时下界三十分钟，即使另有一千个一分钟叶子。若一千叶子占满 worker 令 B 等五分钟，总时长反而三十五分钟。因此调度必须识别关键路径或优先级，而非只追求全局利用率。

## 从简单方案演进

基线是不可变邻接表、每 run 一行 task 状态、事务条件更新依赖计数和单 worker 队列。事件只作唤醒，reconciler 可重扫事实恢复丢通知。第一个待压测指标是 ready 扫描 `p99` 超过完成 SLO 的百分之十，或单 run 节点超过一万导致状态事务超过存储安全大小；此时按 run/task 分片并用幂等依赖事件增量维护 readiness，新增重复事件、计数修复和热点扇入。

第二个待业务校准指标是关键路径节点 `queue/run` 比值连续十五分钟超过 0.3，或普通扇出消耗超过百分之七十资源而关键路径仍排队；此时引入资源池、关键路径优先与租户配额。0.3 和 70% 是需演练参数：过低会牺牲公平和吞吐，过高会错过完成 SLO。大扇入的万级父事件通过层级 barrier 或位图摘要合并，但最终仍核对父 commit 集。

未选择允许运行中的图原地编辑，因为无法稳定解释已发生输出；若系统仅做无副作用交互式草稿且不要求复现，原地编辑可重新变优。未选择把目录 rename 当原子提交，因为跨对象存储并不总有此语义；单机文件系统且单 attempt 时它更简单。

## 设计决定

图发布做环检测并生成 content-addressed graph version。创建 workflow run 时固定版本与参数摘要。workflow reconciler 从权威 task/output 状态计算 desired readiness；调度器授予 task epoch 和 lease。worker 的所有输出写入 attempt namespace，完成时提交含输入版本、输出摘要和 producer epoch 的 manifest。只有当前 epoch 可将 task 转为 `COMMITTED`，随后幂等唤醒子节点。

worker 失租约后，新 worker 获更高 epoch。旧 worker 返回的完成和 checkpoint 被拒绝，临时输出按引用计数延迟回收；若旧 worker 已调用外部系统，使用 `{workflow_run, task, semantic_operation}` 稳定键查询或补偿，不能因 task 状态 FAILED 就断言副作用不存在。取消提高 run epoch，阻止新 commit；已提交父输出仍可审计，不向尚未开始节点发 claim。

反选是中心编排器内存维护整个 DAG，它的延迟低且代码简单；当图小、执行短、单进程重启可接受整图重跑时会重新变优。在长流程和人工介入场景，持久事实加可重算 reconcile 更符合恢复要求。

## 运行与演进

SLI 包括按 graph/run 的 ready lag、关键路径 queue time、attempt 重试率、失租约数、旧 epoch commit 拒绝数、依赖计数漂移、临时输出字节、output commit 年龄与 workflow terminal latency。过载先延迟非关键低优先级节点和历史补跑，再限制新 workflow；不应让已运行任务无法提交 manifest，否则计算成本白费且加剧重试。

故障演练：T0 W1 获 task X epoch 4；T1 写出九个分片后网络隔离；T2 lease 过期，W2 获 epoch 5 并提交 manifest M5；T3 子任务 Y 因 M5 READY；T4 W1 恢复写第十片并提交 M4。预期 M4 被拒绝，Y 的输入只含 M5，W1 对象最终清理。另一轮在父 commit 后、子唤醒前杀死 reconciler，重扫必须再次发现 READY 而不重复创建 task。

待演练指标一是 output commit 后子节点超过两分钟仍未 READY 的数量超过十条时触发全量依赖修复；二是临时输出超过权威输出的百分之二十或最老超过一天时加速 GC。阈值受对象成本和补跑诊断需求校准。图格式迁移先双读旧新版本；运行中的图永远按原版本解释。审计保存输入、参数、代码和输出摘要以支持复现。

## 面试考察本质

给定“下游只能消费固定图版本中全部依赖的一个权威输出世代”这一不变量，因为任务完成通知会重复或丢失、worker 会失租约、对象写入与状态提交不原子，候选人应推导出不可变 graph version、attempt epoch、manifest commit 和可重算 readiness，并在并行利用率、关键路径延迟与重算成本间取舍。

优秀信号是区分 process success、task commit 与 workflow completion，算出关键路径下界，明确旧 attempt 文件如何处理，并把外部副作用放入端到端身份。常见误区包括仅靠消息计数判断依赖完成、允许运行中直接改边、目录里有文件就启动下游，以及认为增加 worker 必然缩短 DAG。

二十分钟回答图版本、状态机和依赖；四十分钟加入 attempt/output commit、重试与关键路径；六十分钟再讨论动态映射、补跑失效、资源公平和制品 GC。最有区分度的追问是 W1 与 W2 都写出输出时谁能发布，以及这个决定如何被下游复现。
