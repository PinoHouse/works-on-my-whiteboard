# 容器编排器

## 表面题目

设计容器编排器，表面上是接收工作负载声明，把容器放到节点，并在节点或进程失败后重启。真正的状态变化是版本化 desired workload 经调度与多个控制器，持续收敛为 observed pods、placements、网络身份和存储绑定。成功不是“创建请求返回”，而是相应 resourceVersion 的副本达到可服务条件，旧节点或旧控制器不能继续代表当前 workload 写外部状态。

本题与部署系统的差别是：部署决定哪个版本及暴露节奏，编排器解决资源约束、节点不可靠、对象生命周期和持续 reconcile。范围包含 API 对象、调度、节点 lease、pod epoch、驱逐与多租户隔离；镜像构建和应用内部主从协议不由编排器神奇解决。

## 反问与边界

先确认目标规模：节点、pod、每秒对象更新、最大单租户、资源类型和故障域。语义上问副本是可替换无状态实例还是绑定卷/身份；节点失联多久可重建；允许重复运行窗口吗；优先级、抢占、亲和/反亲和和拓扑约束谁更重要。SLO 分 API 持久化、调度等待、启动、节点故障检测和副本恢复。

期望状态由 API store 中带 generation/resourceVersion 的 workload、pod 和绑定对象表示；观测状态来自节点心跳、容器状态、资源使用和外部 provisioner 回执。workload controller 创建 pod，scheduler 唯一写 placement binding，node agent 执行当前 pod spec；各 reconciler 独立重试但只能条件写自己拥有字段。节点 lease 表示控制面是否信任其报告，不证明节点上的进程已停止。

安全上 API 授权、节点身份、镜像来源、secret 和租户网络隔离都必须显式。非目标是节点分区后同时保证立即重建与绝无双运行；对有状态写者需要应用/存储层 fence。资源 request 是调度承诺，limit 和真实使用影响过载，但不能把可压缩 CPU 与不可压缩内存同等超售。

## 客观模型

实体为 `Workload(spec_generation, replicas)`、`Pod(uid, workload_epoch, phase)`、`Binding(pod_uid,node,placement_epoch)`、`Node(allocatable, lease_epoch)` 和 `VolumeAttachment(fence)`。基本循环是 `observe -> diff(desired, observed) -> act -> persist observation`。API 条件更新 resourceVersion 是对象意图仲裁；binding 创建是 placement 线性化点；node agent 只接受当前 pod UID/epoch。

不变量包括一个 pod UID 最多一个有效 placement；workload controller 不丢并发用户更新；已删除或替换 pod 的旧 agent 不能回写 Ready；有状态外部写受 workload/volume fence。若资源维度为 `d`，节点 j 的可行条件是所有 `Σ request_i,k ≤ capacity_j,k`，总量足够不代表存在装箱解。例如两节点各剩 `{CPU:4,Mem:1}`，一个需 `{2,2}` 的 pod 无处放置。

控制面写放大约为 `pods * status_update_rate * replicas`，节点同时失联会把恢复、调度、镜像拉取和服务发现同时放大。节点故障判定窗口 `F` 越短，恢复快但短分区导致重复 pod 越多；越长则可用容量恢复慢。这是物理信息边界，不是多加 controller 能消除。

## 必然约束

[DEDUCED:container-orchestrator-convergence-requires-versioned-desired-state] 控制器必须以带 resourceVersion 的期望状态反复协调观测状态，否则并发更新和重试会丢失用户意图或制造重复对象。用户把副本从 3 改 5，同时 controller 基于旧值 3 写“已满足”；若无条件覆盖，用户更新消失。声明式条件写和由 spec 派生的稳定 child identity 使重试幂等，事件只加快收敛。

[DEDUCED:container-orchestrator-node-lease-does-not-fence-external-effects] 节点租约过期只使控制面停止相信该节点，不能立即停止分区节点上的容器；有状态外部写必须另有 workload epoch fencing。节点 A 分区后数据库主 pod 仍运行，控制面在 B 重建；两者都可能写。存储 attach generation、数据库 leader term 或下游 fence 必须拒绝 A，单纯把 API phase 改 `Unknown` 没有物理停止能力。

[DEDUCED:container-orchestrator-placement-is-constrained-allocation] 副本放置同时受资源、故障域、亲和性、端口和租户配额约束，局部贪心在碎片化后可能有空闲总量却无可行位置。调度器需过滤硬约束再评分，并用预留、重排或抢占处理碎片。追求最高平均利用率会降低故障余量，不能同时免费获得完美装箱和即时调度。

## 从简单方案演进

最简单基线是单 API store、单 controller 和 scheduler，节点 agent 轮询 desired pod。稳定 UID 和条件 binding 已可恢复。第一个待压测指标是 API watch lag 超过十秒或 reconcile queue `p99` 超过恢复 SLO 的四分之一，且写热点来自 status；此时按对象类型拆 controller、合并状态更新并分片 watch，新增缓存陈旧、工作队列去重和分片迁移。

第二个待业务校准指标是 unschedulable pod 超过总量百分之一且集群仍有百分之二十空闲资源，或调度 `p99` 超过五秒；此时加入拓扑感知评分、预留与有限回溯。1%、20%、5 秒需按批处理或在线负载校准。节点失联若同时超过百分之五，进入故障域模式，限制重建速率，避免镜像和存储 attach 风暴。

未选择每个 pod 都走全局最优求解器，因为约束变化快、求解尾延迟高；当批任务可等待数分钟且资源昂贵时，离线求解会重新变优。未选择故障一秒即重建，因为短网络抖动会产生大量双运行；当硬件能提供强 fence 且业务要求极短恢复时可缩短。

## 设计决定

API store 保存规范化对象和 resourceVersion，watch 丢失时 controller 从列表重建。workload controller 以稳定 owner UID 创建/删除 pod；scheduler 对未绑定 pod 读取快照、过滤与评分，再用条件 create binding。节点 agent 通过 node lease 报告观察，并按 pod UID、spec generation 启停。外部存储 attach 与有状态服务携带单调 workload epoch。

节点失租约后控制面先标 unknown，超过 policy window 才创建 replacement，并递增有状态 fence。旧节点返回时可上传带旧 epoch 的诊断状态，但 Ready、binding、volume attach 和外部写全部拒绝；agent 必须终止不再属于当前 desired set 的 pod。若 attach 调用响应丢失，reconciler查询 provider 事实而非盲目创建第二卷。

反选是命令式“把容器启动在 node7”，简单且适合静态小集群；当节点固定、无自动恢复且人工运维可接受时重新变优。动态故障下，声明式事实和可重入协调更可靠。

## 运行与演进

SLI 包括 API 提交延迟、watch/reconcile lag、desired/available gap、schedule latency、unschedulable reason、node lease age、duplicate workload epoch 拒绝、驱逐率、资源碎片和每租户占用。过载先降低非关键 status 频率、暂停低优先级批任务和自动扩缩，再保护删除、fence 与健康恢复；不能丢弃 desired spec 更新。

演练：T0 节点 A lease epoch 18 运行有状态 pod P/epoch 9；T1 A 与控制面分区但仍能访问存储；T2 判失联，在 B 创建 P2/epoch 10并把卷 fence 提至 10；T3 A 恢复，以 epoch 9 写存储和上报 Ready。预期存储拒绝写，API 拒绝旧 UID 状态，agent 停 P。另在 binding 成功响应丢失时重启 scheduler，唯一 binding 防止 P 同时调度两节点。

待演练指标一是 node failure 后 available gap 超过两分钟或镜像仓库利用率超过百分之七十时调整分批重建与预热；二是 reconcile 重试中相同错误超过一千次/分钟时进入 per-object backoff 和隔离队列。升级 API 采用版本转换与旧字段保留，controller 灰度必须可读新旧对象。租户配额覆盖对象数、请求、CPU、内存与高成本扩展资源。

## 面试考察本质

给定“版本化 desired workload 必须在节点会失联、资源有多维约束时持续收敛，且旧节点不能继续代表新世代写状态”这一不变量，因为控制面无法立即知道分区节点是否仍运行，候选人应推导出声明式对象、可重入 reconcile、placement 条件写和应用级 fencing，并按恢复速度交换双运行风险与容量余量。

优秀信号是明确 desired、observed 和各 controller 写权限，区分 node lease 与进程停止，给出资源碎片反例，并处理旧节点恢复。常见误区是把心跳过期当物理 kill、让多个 controller 无条件写整个对象、只按 CPU 总量调度，或认为重启容器等于恢复有状态服务。

二十分钟覆盖对象、scheduler 与 agent；四十分钟加入 watch 恢复、节点分区、volume fence 和配额；六十分钟讨论控制面分片、抢占、升级与故障域风暴。本题独特本质是把不断变化的多维资源现实，收敛为有版本的声明，同时承认观测永远落后于物理世界。
