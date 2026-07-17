# 配置与功能开关

## 表面题目

设计配置与功能开关系统，表面上是让操作者发布键值或规则，客户端快速判断某用户是否启用功能。真正的状态变化是租户的不可变 policy snapshot 经校验发布并传播，某次请求必须基于一个可标识版本求值；普通变更可最终收敛，但紧急撤权和 kill switch 要有明确 freshness 上界。

题名掩盖了“配置分发”与“授权决定”的风险不同。颜色开关陈旧一分钟可能无害，关闭有资金损失的路径或撤销访问权则不能沿用长缓存。本设计覆盖版本、规则、分群、发布、SDK 求值、回滚和紧急 deny；复杂身份认证属于授权服务，但策略输入必须可追踪。

## 反问与边界

先问配置是服务端读取、SDK 本地求值还是边缘求值；更新频率、客户端数、规则复杂度、分群规模和多地域离线时长。语义上确认同一请求内是否要求一致快照、跨服务是否需同版本、百分比 rollout 是否对同一主体稳定、缺数据默认 allow 还是 deny。SLO 分发布接受、普通传播、紧急撤权、评估延迟和错误决定数。

期望状态是租户当前 publish epoch 指向的 snapshot 与紧急 deny generation；观测状态是各 relay/SDK 已应用版本、租约、评估日志和心跳。publisher 拥有版本指针，relay reconcile 缺失 snapshot，SDK 只在 policy lease 内求值。旧 publisher 失租约后可完成构建，但不能推进 head；旧 SDK 的处理取决于配置风险等级，不是一律继续服务。

非目标是让永不联网的客户端即时收到撤销。安全配置需短租约、在线 introspection 或到期 fail closed；可用性配置可使用更长缓存并 fail open。隐私上本地规则不应下发不必要的敏感分群成员列表。

## 客观模型

实体为 `Draft`、`Snapshot(version,digest,rules,segments)`、`Publish(epoch,head)`、`EmergencyDeny(deny_generation)` 和 `ClientLease(max_age,risk_class)`。发布流程校验引用闭包和类型后写不可变 snapshot，再以匹配旧 head 的条件写推进当前指针。求值输入是 `{subject attributes, resource, context, snapshot_version, deny_generation}`，输出含 decision、reason 和版本。

不变量是一次求值不混合 snapshot；百分比分桶对固定 `{flag, subject, salt_version}` 稳定；只有当前 publish epoch 推进 head；高风险 allow 不得使用超过 freshness 上限的 deny generation。若客户端数 `C`、snapshot 大小 `S`、更新率 `u`，全量推送带宽约 `C*S*u`；用增量降低带宽，却增加基线缺失和乱序合并失败。规则求值成本近似候选规则数乘属性匹配成本，复杂分群不能靠缓存掩盖最坏尾延迟。

反例：SDK 先收到规则 v12，分群仍是 v11，规则引用 v12 新分群定义；混用可把本不属于实验的人放行。另一个反例是撤权 generation 8 发布后，离线 SDK 持有一小时 lease 继续 allow，系统不能声称即时撤销。

## 必然约束

[DEDUCED:configuration-feature-flags-evaluation-needs-one-policy-snapshot] 一次请求的开关求值必须绑定一个不可变策略快照，否则规则、分群与默认值跨版本混用会产生任何发布版本都不存在的决定。将规则、分群和盐作为内容闭包发布，客户端先完整验证摘要再原子切 head。逐键通知可作优化，但不得逐键直接改变正在服务的逻辑视图。

[DEDUCED:configuration-feature-flags-deny-freshness-trades-offline-availability] 客户端离线缓存越久越可用，紧急撤权和 kill switch 生效越慢；强制 deny freshness 必须付出在线检查、短租约或停止服务的代价。若 token/策略允许离线一小时，网络分区中 deny 至少可迟一小时。高风险开关到期 fail closed，低风险展示开关可继续旧值，这是业务风险选择而非缓存技术细节。

[DEDUCED:configuration-feature-flags-expired-publisher-cannot-advance-head] 发布者租约过期后生成的快照可留作审计，但只有更高 publish epoch 能推进租户当前版本指针。P1 epoch 20 构建慢，P2 获 21 发布 v14，P1 随后完成 v13；若最后写胜，head 倒退且撤销丢失。版本指针和下游 relay 都要拒绝低 epoch，不能只给 publisher 表加锁。

## 从简单方案演进

最简单基线是中心数据库保存不可变 JSON snapshot，服务每分钟拉取，校验后原子替换内存指针。它适合少量内部服务。第一个待压测指标是 `C*S/interval` 使出口持续超过带宽百分之六十，或普通传播 `p99` 超过五分钟；此时加 relay、长轮询和按摘要差分，新增缓存层、断点基线与版本观测。

第二个待业务校准指标是高风险 deny 的 `p99` 传播超过三十秒或任一活跃客户端 snapshot age 超过其风险租约；此时独立紧急 deny 通道、缩短 lease，并让过期客户端 fail closed。三十秒须由损失预算决定：支付路径可能要求一秒，推荐开关可数分钟。规则评估 `p99` 超过请求预算百分之十或单 flag 规则超过一千条时预编译索引和限制表达式。

未选择每个请求同步读取中心服务，因为它提供新鲜度却把配置系统变成所有业务可用性的硬依赖；只有极少数高风险决定且中心服务有相应 SLO 时重新变优。未选择无版本逐键推送，因为难以原子表达相关配置。

## 设计决定

编辑产生 draft，publisher 在租户串行 epoch 内解析、类型检查、检测循环并构建 content-addressed snapshot。条件推进 head 后 relay 按版本拉取，客户端仅在完整摘要验证成功时原子切换。评估记录 snapshot、deny generation、bucket 和 reason。百分比规则用固定盐哈希主体，改变盐显式形成重新分桶版本。

旧 publisher 租约过期后返回时，snapshot blob可保留，head 更新被拒绝。客户端 policy lease 过期后，低风险 flag 使用最后值并标 stale，高风险 allow 变 deny 或转在线检查；收到更高 deny generation 时先安装 deny，再异步拉完整 snapshot。发布响应丢失时查询 head/epoch，不重复生成不同版本。

反选是推送每个用户的预计算决定，读取极快；当规则少但分群极大时存储与更新扇出严重。若主体集合小、决定高风险且集中式计算便于审计，它可重新变优。默认选择规则快照与本地评估。

## 运行与演进

SLI 包括 publish duration、snapshot/deny propagation、活跃客户端 version 分布、snapshot age、评估延迟、unknown attribute、默认值命中、低 epoch 拒绝和 fail-closed 数。过载先延迟低风险普通发布，保留 emergency deny 带宽；限制租户规则复杂度，不能为了可用绕过快照校验。

演练：T0 P1 epoch 30 开始构建 v20；T1 租约过期，P2 epoch 31 发布紧急 deny 与 v21；T2 P1 恢复尝试推进 v20。head 必须拒绝，SDK 先观察 deny 31，不因 snapshot 下载慢继续 allow。另一轮让客户端只收到 delta 无基线，必须请求全量而非拼接未知状态。

待演练指标一是超过百分之一活跃 SDK 的版本落后两个 generation 或十分钟时停止普通 rollout；二是 emergency deny ACK 在三十秒内低于百分之九十九时升级事件并让高风险入口在线校验。阈值按在线覆盖与资产损失校准。schema 演进先让 SDK 忽略未知字段，再发布新规则；回滚推进一个新 snapshot，不改写历史。审计保留操作者、审批、diff 和求值版本。

## 面试考察本质

给定“一次配置决定必须来自一个真实发布过的快照，且高风险 deny 在声明时间内生效”这一不变量，因为客户端可能离线、更新会乱序且发布者会失租约，候选人应推导出不可变 snapshot、原子 head、publish fencing 与风险分级租约，并在读取可用性、传播带宽和撤权新鲜度之间决策。

优秀信号是区分普通配置和安全 deny，解释分群与规则为何要同版本，处理旧 publisher 和离线 SDK，并给稳定分桶公式。常见误区是逐键推送后声称事务一致、所有缓存永不过期、缺配置默认 allow，或把 WebSocket 当成必达证明。

二十分钟覆盖快照、拉取和本地求值；四十分钟加入 relay、紧急 deny、稳定 rollout 与审计；六十分钟讨论表达式治理、隐私分群、SDK 兼容和多地域。本题独特本质是把“快”与“新”拆成可声明风险等级，而不是设计一个巨大键值缓存。
