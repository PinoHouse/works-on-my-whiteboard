# CI 运行器

## 表面题目

设计 CI 运行器，表面上是收到代码变更后拉取仓库、执行用户定义步骤并上传日志与制品。真正的状态变化是一个绑定提交、流水线定义和信任上下文的 build，从排队变为被隔离 runner claim，最后由当前 attempt 发布可验证结果。仓库代码应被视为攻击者可控；“测试通过”不仅要防重复执行，还要证明输出来自正确输入和受约束环境。

本题区别于通用作业调度器：runner 主动执行不可信程序，并接触源码、缓存、凭证和制品发布权。设计覆盖隔离、短期 secret、缓存与制品 provenance、日志和 runner 回收；流水线 DAG 可复用工作流机制，但安全边界不能委托给 YAML。

## 反问与边界

先问触发来源是否包含外部 fork，哪些分支可信，构建能否发布生产制品，secret 按仓库、环境还是人工审批授予。隔离目标是防普通误操作、恶意租户，还是抵御内核逃逸；是否允许嵌套容器、网络访问和自托管 runner。SLO 包括 webhook 到启动、总构建时间、日志延迟和制品可用时间，安全指标包括越权 secret 访问、跨租户残留与缓存污染。

容量由每秒 build 到达率 `B`、各资源类别执行时间 `T_r`、仓库偏斜、镜像与依赖下载字节决定。期望状态是 build spec、信任域、资源和允许能力；观测状态是 runner attestation、sandbox、attempt epoch、日志、缓存命中和 artifact commit。build controller 拥有状态，runner manager 拥有 sandbox 生命周期，artifact registry 只接受当前 epoch 和可信 provenance。

非目标是在共享普通进程中执行任意不可信代码后声称强隔离，也不允许把长期云密钥注入所有步骤。外部 fork 默认没有生产 secret；若构建必须访问敏感环境，能力应绑定仓库、提交、步骤、有效期和目标。

## 客观模型

实体为 `Build(input_digest, pipeline_digest, trust_domain)`、`Attempt(epoch, runner_id, lease)`、`Sandbox(attestation, capability_set)`、`CacheEntry(key, provenance)` 和 `Artifact(manifest, signature)`。状态 `QUEUED -> PROVISIONING -> RUNNING -> PUBLISHED|FAILED|CANCELLED`。runner 输出先进入 attempt namespace；控制面验证 epoch、输入摘要、退出状态和 runner attestation 后原子推进 artifact tag。

不变量是未授权代码不可获得 secret；租户之间 CPU、网络、磁盘和残留状态隔离；缓存不能跨不相容信任域或输入复用；只有当前 build epoch 能发布状态和制品。容量近似 `runner_r ≥ λ_r * E[T_r] / u`，目标利用率 `u<1` 为突发留余量。若镜像大小 `I`、冷启动率 `c`、到达率 `λ`，下载带宽约 `λcI`；盲目扩 runner 会先打满镜像仓库。

具体反例：外部 fork 修改构建脚本打印环境变量；若 secret 在 checkout 前全局注入，日志即泄露。另一个反例是恶意构建向共享 key `linux-main` 写入后门编译器，可信 main 构建命中缓存并发布。缓存 key 必须包含依赖摘要、平台和信任域，写权限还要受 provenance policy 约束。

## 必然约束

[DEDUCED:ci-runner-untrusted-code-requires-capability-bounded-isolation] 仓库代码可完全不可信，构建隔离与短期能力必须确保它不能读取未授权 secret、控制宿主或访问其他租户运行。仅把步骤放进同一宿主用户空间容器，若挂载守护进程 socket 或宿主工作目录，攻击者可逃逸信任边界。隔离强度必须匹配威胁模型，高风险公共构建应使用一次性 VM 或等价沙箱，并默认拒绝网络和凭证。

[DEDUCED:ci-runner-cache-and-artifacts-require-provenance] 共享缓存与制品若不绑定输入摘要、构建世代和信任域，攻击者或旧 runner 就能让后续可信构建消费被投毒结果。内容摘要解决偶然碰撞，却不能证明谁有权把摘要绑定到“release”标签；因此缓存读取策略、制品签名和标签推进都要验证来源。只读公共依赖缓存可更宽松，发布缓存不可。

[DEDUCED:ci-runner-expired-attempt-must-not-publish-success] runner 租约过期后返回的日志可以留作诊断，但其状态、缓存和发布制品必须被当前 build epoch 拒绝。W1 epoch 6 卡住，W2 epoch 7 发现测试失败；W1 恢复并报告成功。若最后写胜，坏提交被标绿。单调 epoch 使状态库、缓存写入口与 registry 都拒绝 6；仅在数据库检查 status 仍挡不住 W1 直接推送制品。

## 从简单方案演进

最简单基线是一台专用 runner 顺序执行可信内部仓库，每次构建前后清空工作目录，状态条件绑定 attempt。它在威胁面小且吞吐低时正确。第一个待压测切换指标是 queue `p95` 超过五分钟或 `queue_time/build_time > 0.5` 连续十五分钟；此时按 CPU、内存、架构和信任域建 runner 池并自动扩缩，新增冷启动、碎片和镜像洪峰。

第二个待安全演练指标是工作区清理 `p99` 超过三十秒、发现一次跨 build 残留，或公共 fork 超过总量百分之十；此时把该信任域迁移到一次性沙箱，不与可信 runner 复用宿主。阈值需按攻击损失校准：严格场景一次事件即切换，低风险内网可权衡成本。缓存命中低于百分之二十且校验与下载成本高于节省时间时，应关闭宽泛共享并重新设计键，而非不断扩容缓存。

未选择所有构建共用常驻宿主以追求最高命中，因为残留和宿主控制权风险过高；当代码完全可信、runner 专属单仓库且无敏感能力时会重新变优。未选择每步永久云密钥；只有隔离环境无法使用短期联合身份且密钥可严格限定只读目标时，才作为受审计例外。

## 设计决定

入口固定 commit SHA 与 pipeline digest，计算 trust domain 并进行准入。controller 授予 build epoch；runner manager 启动干净、可证明配置的 sandbox，只向需要步骤发放短期 capability。checkout、依赖和用户代码网络分别受策略控制。日志按 attempt 追加并脱敏；artifact 先写临时 namespace，registry 校验当前 epoch、manifest digest、runner attestation 和签名后推进不可变引用。

runner 心跳丢失时先吊销 capability，再发更高 epoch 重试。旧 runner 返回后日志标记 `STALE` 可查看，但完成状态、缓存写、制品标签和环境部署请求一律拒绝；已上传的未引用 blob 由 GC 清理。若外部发布接口已收到请求但响应丢失，用 build identity 查询，不换新 release ID 重发。取消也是增加 epoch，不声称能撤回已泄露信息。

反选是允许缓存按分支名读写，简单且命中高，但 fork 可预测分支名投毒。只有缓存内容经过摘要校验、写者可信且分支空间不可跨租户时重新变优。生产发布最好由独立可信阶段消费已签名制品，而非让测试 runner 持有部署凭证。

## 运行与演进

SLI 包括按池 queue age、冷启动、构建耗时、镜像下载、缓存命中与校验失败、secret 请求拒绝、旧 epoch 发布拒绝、沙箱清理、逃逸告警和制品 provenance 缺失数。过载先暂停低优先级定时构建和 speculative retry，再限制每仓库并发；保护当前构建日志与制品提交，避免已消耗计算因控制面拥塞全部重跑。

演练时间线：T0 W1 获 build epoch 12 和五分钟 token；T1 上传临时 artifact 后网络隔离；T2 token 到期，W2 获 13 并报告测试失败；T3 W1 恢复尝试推进 `release/latest`。registry 必须拒绝 epoch 12，token 无法访问生产，临时 blob 最终回收。另演练恶意 fork 写共享缓存，可信 main 构建必须因 trust-domain/provenance 不匹配拒绝。

待演练指标一是镜像下载占构建总时长超过百分之三十且仓库出口利用率超过百分之七十时部署按摘要预热；二是 sandbox 启动 `p95` 超过 queue SLO 的四分之一时增加 warm pool，但 warm 实例仍须重置身份和磁盘。升级 runner 镜像用不可变版本小流量灰度，回滚不复用新版本写出的可信缓存，签名密钥轮换保留验证旧制品的公钥。

## 面试考察本质

给定“不可信仓库代码不得越权，而只有当前 build attempt 能把结果提升为可信制品”这一不变量，因为 runner 状态、secret 生命周期和缓存来源在失败后都可能不确定，候选人应推导出一次性隔离、最小短期能力、attempt fencing 与 provenance，并依据威胁损失交换冷启动、缓存命中和成本。

优秀信号是先画信任边界，再讨论队列；主动区分日志、blob 上传与制品发布；说明外部 fork 为什么不能拿 secret，以及旧 runner 恢复为何不能标绿。常见误区是把容器名字当强隔离、按分支共享可写缓存、把 secret 打码当作不泄露，或只在 build 表检查 epoch 却允许直接推 registry。

二十分钟覆盖 build 状态、runner 池与隔离；四十分钟加入 capability、缓存投毒和制品签名；六十分钟再讨论多架构、供应链证明、warm pool 与应急吊销。此题独特本质是：调度对象本身就是潜在攻击者，正确性必须同时约束“谁运行”和“其输出为何值得信任”。
