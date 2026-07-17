# 推理网关

## 表面题目

设计统一推理网关：多个租户通过一个入口调用不同版本、大小和硬件需求的模型，网关完成鉴权、配额、路由、准入、动态批处理、流式返回、取消与计费。成功不只是后端返回结果，而是在 deadline 内由契约指定的模型版本执行，实际 token/设备预算没有被少数长请求透支，重试和取消不会产生两个可计费 attempt。

它与单一聊天服务不同：核心不是会话历史或回答语义，而是跨模型、跨租户共享昂贵执行池时的 admission 和 scheduling。也与 GPU 调度器不同：网关分配请求级 token、deadline 与 batch slot，不直接决定某训练 gang 占哪几张物理卡。模型输出可能是文本、embedding、图像或分类，本题统一的是资源与协议边界。

## 反问与边界

先问模型目录规模、各模型权重/显存、输入输出形状、冷加载时间、支持的硬件和并发方式；流量按模型、租户、输入 token、最大输出和 deadline 分布描述，不能只问 QPS。质量需按模型定义任务准确率或固定评测得分并防止路由静默换模；体验看排队、首输出、完成 `p95/p99` 和 deadline miss；成本看每请求或每有效 token 的 GPU 秒、冷加载与取消浪费。

契约还要明确同步/流式、幂等重试、客户端断开是否等同取消、是否可降级到量化/小模型、优先级与最低公平份额。多租户需要 prompt/adapter 隔离、日志保留和侧信道限制；模型、tokenizer、预后处理、runtime/kernel、adapter、batching policy 和采样参数都需归因。非目标是调度训练作业，也不因网关状态机确定就承诺模型输出逐字相同。

## 客观模型

接口为 `Infer(model_ref, input, max_output, deadline, priority, idempotency_key)`、`Stream(attempt_id, offset)`、`Cancel(attempt_id, expected_epoch)`。模型目录记录 `(model_digest, tokenizer/preprocessor, runtime, hardware_class, memory_floor, quality_tier, residency_generation)`；admission ledger 保存租户 token 信用、并发和设备秒预算；attempt 保存估算/实际 token、deadline、route、batch、lease_epoch、输出 offset、状态与完整版本谱系。

设请求输入 token `I`、输出上限 `O`、模型驻留显存 `W`、每 token KV 系数 `v`，单 attempt 峰值预算近似 `M=W+(I+O)*v`，批次共享权重但各自保留序列状态。队列的有效工作量更接近 `Σ(prefill(I_i)+decode(O_i))`，不是请求数。多模型池还承担冷切换成本 `L_load(model)`；频繁在两个大模型间抖动会让 GPU 大量时间花在加载而非推理。

控制面目录拥有可路由模型代，admission ledger 是预算权威，当前执行租约拥有 attempt，输出存储按 `(attempt, lease_epoch, offset)` 条件提交。关键不变量：一次 attempt 固定模型与处理谱系；未获 token/驻留预算不得入执行队列；deadline 到期或取消确认后旧 epoch 不得再提交/计费；租户消耗不得越过明确借贷上限。确定路由与版本可解释输出来源，不能消除采样、数值内核和 batch 顺序带来的概率差异。

## 必然约束

[DEDUCED:inference-gateway-admission-must-price-token-and-residency] 多模型推理的成本由输入输出令牌、模型驻留与内存预算共同决定，只按请求数准入会制造显存溢出和冷加载抖动。反例：两个队列都各有 10 个请求，A 每个 100 token 且模型已驻留，B 每个允许 8,000 token 并轮流调用两个需 40GB 权重的模型。计数相同，B 的 KV 和加载工作可高两个数量级。准入必须先解析模型、输入形状和输出上限，并将未知输出作为分段信用而非零成本。

[DEDUCED:inference-gateway-deadline-and-fairness-require-explicit-scheduling] 吞吐最优的动态批处理可以持续拖延短截止期或低流量租户，deadline 与公平必须成为显式调度约束。若批处理器总选同模型以避免加载，热门模型请求持续到达时，冷门租户即使只有一个 100ms deadline 请求也可能永不执行；若总选最大 batch，长 prefill 又能阻塞交互解码。调度目标必须给 deadline slack、租户 token debt 和切换成本明确权重，并在不可能时准入前拒绝。

[DEDUCED:inference-gateway-cancellation-requires-fenced-attempts] 取消只有在旧执行 attempt 被世代栅栏阻止继续生成、提交和计费时才完成，连接断开本身不是资源回收证明。事件序列：客户端断线，网关把状态标 canceled，但 worker 在网络分区内继续生成 2,000 token。提交侧 epoch 只能丢弃输出，不能取回已经消耗的 GPU；因此执行器本地 watchdog 必须按签发的 deadline/epoch 强制停止 decode，并由 device owner ACK。拿不到 ACK 时要隔离该 worker、KV 与设备容量，不能仅等控制面租约时间后复用。

## 从简单方案演进

基线是每模型一个静态队列和固定副本，请求只在确认容量后串行或小批运行。它隔离清晰，但低流量模型浪费设备、热点模型排队。当某模型 GPU 利用率低于百分之三十同时其他模型 deadline miss 超过百分之一，才允许兼容模型共享池；共享提高利用率，却新增权重驻留选择与冷加载。百分比是待压测和业务 SLO 校准的初始切换点。

第二步按 `(model_digest, shape bucket)` 连续批处理，admission 以输入精确 token、输出分段信用和 KV 余量计价。若准入后 OOM/驱逐超过万分之一，或 p99 首输出达到 SLO 的百分之八十，就收紧估算、分离 prefill/decode 或保留交互容量。阈值越低，闲置成本越高；越高，用户中途失败越多，必须通过压力测试校准。

第三步跨模型做 deadline-aware、token-fair 调度，并维护驻留集合。若某租户连续一分钟 token debt 超过其突发额度，限流而不让它占满 batch；若模型冷加载 `p95` 超过 deadline 的一半，保持热副本、预取或直接拒绝短 deadline。离线评测还要规定：路由到量化模型使任务质量下降超过允许百分点时，禁止自动降级。

未选“任意空 GPU 立即执行”，因为它忽略驻留和 batch 合并；模型单一、负载低时该方案重新变优。也未选全局吞吐最大化，因为会饿死低流量/短 deadline；离线批任务且无租户公平要求时，吞吐优先调度才重新变优。

## 设计决定

请求进入后先鉴权并解析不可变 model_ref，tokenizer/preprocessor 计算输入工作量，admission ledger 原子扣除租户信用、并发和池级 KV/设备秒预留，失败则带可解释原因拒绝。路由只选已声明兼容的 runtime/hardware；scheduler 以 deadline slack、token debt、驻留代价和 batch 增益排序，连续批处理在安全边界加入/移除序列。

worker 取得带绝对 deadline 的 attempt lease_epoch，执行器本地 watchdog 在控制面失联时也会于 deadline、取消 epoch 或信用耗尽处停止 decode；输出和实际用量按 epoch/offset 条件追加。Cancel 推进 epoch，向 executor/device owner 下发 fence，并只在收到“执行已停、KV 已释放”的 ACK 后归还容量。若 ACK 不可达，控制面拒绝旧输出/计费但把整个 worker、KV 和对应设备份额置为隔离，不按时间推测它已停止。幂等键相同且输入摘要相同返回原 attempt；重试新采样创建新 attempt。

过载先停止低优先离线批量，缩小可选输出上限和多候选，再对明确同意的请求切质量等级，最后拒绝；绝不静默换模型或越过租户隔离。完整记录 model/data adapter/prompt 或 preprocessing/tokenizer/runtime/batching/sampling 版本，用于质量与成本归因。固定这些仍不保证概率模型逐次一致。

## 运行与演进

SLI 按模型和租户观察 admission reject、排队、首输出、完成延迟、deadline miss、token throughput、准入后 OOM/驱逐、取消到 GPU 停止和公平 debt。质量侧对每个 model/runtime generation 跑固定任务指标和安全回归；成本看有效输出 token 的 GPU 秒、冷加载占比、padding/KV 浪费和取消后浪费。不能用总吞吐掩盖某租户饥饿。

故障演练：t0 attempt A 以 epoch 7 在 worker W 执行；t1 客户端取消，ledger 推进到 8 并停止后续信用；t2 W 网络隔离，本地 watchdog 到签发边界强制停 decode，但 ACK 暂时不可达；t3 控制面拒绝 epoch 7 输出和计费，同时隔离 W、其 KV 与设备预算，新请求不得复用；t4 device owner 恢复并确认进程停止、KV 清理后才释放容量。对照演练关闭 watchdog，应看到容量持续隔离而非仅凭租约到期超卖。

每次 release 绑定不可变 eval manifest：各模型任务/流量回放 digest、正确性与安全标签 digest、人工 rubric 或 judge version、准确率/deadline/fairness/成本指标实现版本。模型或调度升级只在同一 manifest 上比较任务质量、首输出、deadline miss 与单位 token 成本；数据或 judge 改版需新基线。灰度 attempt 和回滚目录都记录该 manifest。定期演练冷加载风暴、取消隔离和 adapter 越权；质量越过下限即回滚，冷加载成本越界则增加驻留或拆池。

## 面试考察本质

给定“已接受请求只能消耗批准的模型、token、deadline 与租户预算，取消后的旧执行权不得继续提交”这一不变量，因为输出长度未知、模型驻留稀缺且动态 batch 的吞吐目标会伤害 deadline 与公平，候选人应推导出 token/驻留感知 admission、显式公平调度和 fenced attempt，再依据质量等级、尾延迟和 GPU 成本选择。

优秀信号包括用 token/KV 而非 QPS 容量规划，区分准入失败与执行失败，讨论连续批处理中的 prefill/decode 干扰、冷门模型饥饿和取消 ACK。常见误区是一个队列轮询所有模型、断连即算取消、只优化 GPU 利用率、静默换小模型，或把版本化网关当作模型确定性证明。

二十分钟给出模型目录、ledger、attempt 状态机与容量公式；四十分钟加入 deadline/fairness、continuous batching、取消栅栏和过载；六十分钟讨论多地域、adapter 隔离、质量灰度和成本归因。追问用“10 个短请求对 10 个 8k 请求”检验准入，用“热门模型永续到达”检验公平，用“取消后分区 worker 回传”检验真正资源所有权。
