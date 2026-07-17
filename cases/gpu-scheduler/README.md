# GPU 调度器

## 表面题目

设计 GPU 调度器：接收训练、批推理或分布式推理作业，按照 GPU 型号、显存、互联拓扑、gang 数量、优先级和租户配额选择节点，管理启动、租约、抢占、checkpoint 与故障恢复。成功不是“找到几张空卡”，而是整组设备在可接受等待内同时可用，旧任务失去分配权后不能继续访问复用设备，抢占带来的恢复成本没有反过来摧毁吞吐。

它与推理网关的区别是分配粒度和状态所有者：网关调度请求 token 与 batch slot，本题调度分钟到数天的物理设备 allocation。题目也不能退化成普通 CPU 容器调度，因为 GPU 显存不可随意超卖、跨卡带宽差异巨大、分布式任务部分启动通常毫无产出。

## 反问与边界

先问工作负载是单卡推理、数据并行训练、张量/流水并行还是混合，所需 GPU 数、型号、显存、NVLink/PCIe/机架拓扑、CPU/RAM/网络和本地缓存。SLO 包括排队 `p95/p99`、gang 启动时间、作业完成时间、故障恢复 RTO；效率看 GPU 计算/显存利用、拓扑降速、碎片化；成本看设备小时、checkpoint I/O 与抢占损失。

公平要明确租户配额、优先级、借用与回收、最大饿死时间；抢占对象是否可 checkpoint、周期和恢复兼容版本。租约到期由谁在节点/设备侧执行，孤儿进程、MIG、驱动重置和故障域如何处理。安全还包括容器、GPU 内存清零、peer-to-peer、模型/数据缓存和侧信道隔离。模型、数据、训练代码、容器、驱动/runtime、checkpoint 格式与随机状态需要谱系，但即使调度确定，训练或推理的并行数值结果仍可能非确定。

## 客观模型

接口为 `Submit(job_spec, priority, tenant, idempotency_key)`、`Acquire(allocation_id, expected_epoch)`、`Heartbeat(allocation_id, epoch)`、`Checkpoint(job_id, state_digest)` 与 `Release(allocation_id, epoch)`。job_spec 包含不可变的 GPU count/type、每卡显存、允许拓扑、gang 语义、预计时长与 checkpoint 能力。设备目录显式建模 `physical_gpu → partition/MIG_instance → allocation`：父卡保存健康、P2P/NVLink 拓扑和 physical_fence_generation，每个子分区保存 profile、显存和 partition_fence_generation，allocation 再保存 lease_epoch 与 owner。

设节点总显存空闲和为 `F_total`，但一个请求需单卡连续显存 `m`；可分配容量不是 `F_total/m`。例如四张 80GB 卡各碎片化剩 20GB，总空闲 80GB，却无法放置一个需单卡 40GB 的任务。分布式作业每步时间近似 `Tstep=max(Tcompute_i)+Tcollective(topology,bytes)+Tstraggler`；把八卡 gang 跨低带宽机架可能虽然“放下”却使训练时间翻倍。排队还受 gang size 的装箱与 head-of-line blocking 影响。

调度器拥有期望 allocation，节点 agent/设备插件拥有父卡与 partition 的实际启动/fence，checkpoint 存储拥有可恢复状态。关键不变量：gang 要么全部设备在同一 epoch 获得并通过启动 barrier，要么全部回滚；allocation 必须同时匹配父卡、分区和自身三个当前 epoch；父卡 reset/故障会让所有 sibling slice 一起失效；P2P 与拓扑能力只按 physical GPU 关系声明。租户借用不能越过可回收边界。

## 必然约束

[DEDUCED:gpu-scheduler-placement-must-respect-topology-and-gang-atomicity] 分布式训练或张量并行任务只有在整组设备同时满足互联与显存约束时才有用，逐卡独立分配会形成昂贵空等。最小反例是四卡 job，调度器先占三张，第四张长期不可用；前三张既不能产生训练 step，又阻止三个单卡任务运行。即使凑足四张，若两张跨慢速链路，collective 成为瓶颈。故候选选择要同时满足 gang、显存与拓扑，并以 reservation+commit barrier 原子启动。

[DEDUCED:gpu-scheduler-lease-expiry-needs-device-level-fencing] 控制面租约过期只表达调度器的判断，必须由设备侧世代栅栏阻止旧任务继续使用 GPU，才能安全复用。对 MIG 不能只 fence 子 allocation：同一父卡上的 slice A reset 或父卡 ECC 故障会影响 sibling slice B。节点 agent 必须同时校验 physical、partition 与 allocation generation；一旦需要父卡 reset，就隔离父卡及全部子分区，终止/重建 sibling，健康确认后才重新准入。不能确认时宁可隔离而非超卖。

[DEDUCED:gpu-scheduler-preemption-trades-recovery-cost-for-fairness] 抢占能缩短高优先级等待，却把代价转化为 checkpoint、恢复和已完成计算损失，是否抢占必须比较剩余工作与恢复成本。一个还剩 2 分钟的低优任务若 checkpoint+恢复需 8 分钟，为让高优任务提前 2 分钟而抢占会增加系统总完成时间；另一个剩 10 小时且 30 秒可恢复的任务则适合抢占。优先级不能单独决定，需估计 `benefit_wait_saved > checkpoint+restore+lost_work+fragmentation`。

## 从简单方案演进

基线是同构集群、单卡不可抢占任务的 FIFO；节点有足够显存就放置。它语义简单。当队首大 gang 阻塞使空闲 GPU 比例连续十分钟高于百分之二十，或租户排队 `p95` 超过 SLO，才引入 backfill：小任务可借用未来 reservation 前的空窗，但必须在承诺时间前结束或可抢占。阈值需真实时长误差与压测校准。

第二步加入 topology-aware gang scheduling，先预留整组设备，再让节点 agent 提高 fence generation 并 barrier commit。若拓扑不匹配造成 step time 比同型基线慢百分之十五以上，或 gang 启动失败超过千分之一，就收紧放置域/健康检查；收紧会增加排队，需离线基准和故障演练选择。若物理空闲显存与可分配显存差距超过百分之二十五，则通过整卡优先、MIG 规格整形或 drain/重排降低碎片。

第三步做配额、借用与 checkpoint-aware preemption。高优作业等待接近 SLO 的百分之八十才评估候选，只有预计等待收益超过恢复总成本才抢占。百分比是策略起点，调低增加抖动和 I/O，调高伤害紧急作业。长期批作业可周期 checkpoint，但周期按故障率和 checkpoint 成本计算，而非越频繁越好。

未选择全局最紧密装箱，因为可能把所有高速互联卡填成小碎片，未来 gang 无法启动；单卡占绝大多数且无大 gang 时紧密装箱重新变优。也未选择所有高优任务立即抢占，因为不可 checkpoint 作业损失巨大；安全紧急任务且业务明确愿意牺牲吞吐时它才重新变优。

## 设计决定

调度循环从版本化设备快照生成候选，先在 physical GPU 图上检查 P2P/NVLink/故障域，再选择其 partition/MIG instance，以拓扑代价、碎片增量、公平 debt 和预计完成时间评分。gang 进入 `RESERVING`，所有节点 agent 对 physical、partition、allocation 三层 generation 做条件确认并 ACK；全部成功后才 `RUNNING`。子分区不能虚构父卡没有的 P2P 能力。

心跳丢失先冻结新 checkpoint 提交，租约到期触发节点侧停止并推进 allocation/partition fence；若需父卡 reset 或发现父级健康故障，立即隔离 physical GPU 和全部 sibling partitions，终止或 checkpoint 可恢复 sibling，重建设备分区并通过显存清理、ECC、拓扑健康检查后才提升 physical fence、重新准入。节点不可达时整张父卡保持隔离。抢占仍需校验 checkpoint digest 与兼容谱系。

过载先限制低优租户新 gang，再回收借用和 backfill，之后抢占“恢复成本/释放资源”比最低者；不会破坏设备隔离或把部分 gang 当成功。job 记录模型/数据快照、代码、容器、驱动/runtime、并行配置、随机种子和 checkpoint 版本。它支持故障归因，但 collective 顺序、硬件数值和上游概率模型仍使结果可能不确定。

## 运行与演进

SLI 包括按 gang size/租户的排队与启动延迟、设备利用率、可分配/物理空闲显存差、拓扑降速、reservation 回滚、fence 延迟、孤儿进程、抢占 checkpoint/恢复时间和丢失 GPU 小时。成本按成功训练 step 或完成 job 的设备小时、网络与 checkpoint 存储计算；质量侧对固定模型任务监控收敛/推理指标，防止 runtime 或拓扑变化静默影响结果。

故障演练：t0 父卡 P 的 MIG slice A/B 分别运行 allocation epoch 5/9；t1 A 触发需父卡 reset 的故障；t2 调度器不能只把 A 标坏而保留 B，agent 隔离 P、推进 physical fence，并终止或从 checkpoint 重建 A/B；t3 任何旧 partition/allocation epoch 提交均被拒绝；t4 reset、显存清理、ECC 与 P2P 健康确认通过后重建 slices 并提升各自 fence；t5 才重新准入。演练证明 sibling 的故障域在父卡。

每次 release 绑定不可变 eval manifest：job trace/设备拓扑与故障注入集 digest、期望可行放置与优先级标签 digest、运维公平 rubric 或 simulator/judge version、排队/碎片/设备小时/收敛质量指标实现版本。新算法只在同一 manifest 上与旧 scorer 比较；trace 或指标改版先重建基线。小池灰度与回滚都记录 manifest。fence 超时越界立即停灰，若等待下降却使恢复损失或固定训练任务收敛质量恶化则回滚。

## 面试考察本质

给定“一个 gang 必须整体获得满足拓扑的设备，且设备只能被当前 fence epoch 使用”这一不变量，因为显存不可简单求和、跨卡带宽决定有效算力、控制面无法瞬时知道节点是否停止，候选人应推导出拓扑感知原子 reservation、设备侧 fencing 和 checkpoint-aware preemption，并在公平等待、碎片、恢复损失与设备成本间取舍。

优秀信号是用四卡缺一卡和 80GB 碎片反例否定逐卡调度，区分 lease 判断与设备实际 fencing，用收益公式决定抢占，并讨论 backfill 对未来 gang 的影响。常见误区是把 GPU 当 CPU 核、只优化利用率、控制面标记过期就复用设备、或假设 checkpoint 免费且跨 runtime 永远兼容。

二十分钟给出 job/allocation/device 模型、gang barrier 和碎片公式；四十分钟加入拓扑、backfill、公平与抢占；六十分钟讨论故障 fence、多租户安全、checkpoint 谱系、算法灰度和成本。追问用“三张卡先占等第四张”检验 gang 原子性，用“分区节点旧进程仍跑”检验真实所有权，用“剩两分钟却恢复八分钟”检验抢占判断。本题考的是稀缺异构设备的可撤销分配权，不是再写一层请求路由。
