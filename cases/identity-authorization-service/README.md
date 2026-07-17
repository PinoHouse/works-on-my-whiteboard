# 身份与授权服务

## 表面题目

设计身份与授权服务，表面上是验证用户名、密码或令牌，再回答某主体能否对资源执行动作。真正的状态变化包含 credential、session、policy 和 revocation generation；一次 allow 必须绑定这些版本与资源属性，签名有效只证明谁签发过令牌，不证明当前仍有权限。高风险 deny 还要在声明时间内越过缓存生效。

题名掩盖认证、会话与授权是不同问题：认证建立主体，授权基于主体、动作、资源与上下文求值，撤销改变未来信任。设计覆盖凭证验证、短期 session/token、策略快照、决策缓存、密钥轮换和撤销；用户目录搜索、组织业务模型与应用数据面不展开，但资源属性版本必须可输入决策。

## 反问与边界

先问主体类型、身份源、MFA、账户恢复和租户边界；授权模型是角色、属性还是关系图，资源数量与组嵌套多深。令牌是离线验证还是每次 introspection，允许多长撤销窗口；密码重置、员工离职、设备丢失和策略 deny 各自需要多快。SLO 分登录、token 签发、授权判断、策略发布、deny 生效和审计可用。

期望状态是主体有效性、credential/session generation、当前 policy snapshot 和 revocation watermark；观测状态是验证节点的密钥/策略版本、会话最后使用、撤销传播与资源属性。issuer 拥有 credential/session 状态，policy publisher 拥有不可变策略版本，authorizer 求值当前可接受快照。旧 issuer 或 policy worker 的租约过期后可返回诊断，但不能签发或推进 head。

非目标是让永不过期的离线 token 立即撤销，也不把身份服务网络分区时默认 allow 当高可用。不同动作按风险选择 fail closed、短期 grace 或只读降级。密码和恢复凭证永不进入普通日志，租户策略与组缓存必须带租户键。

## 客观模型

实体为 `Principal(status,generation)`、`Credential(type,generation)`、`Session(session_id,authn_context,session_generation,expiry)`、`PolicySnapshot(version)`、`Revocation(kind,id,generation)` 和 `Decision(input_versions,allow,reason,ttl)`。token 至少含 issuer/key id、subject、audience、session generation、authn level、issued/expiry；authorizer再结合 action、resource attributes、policy version 与 deny watermark。

不变量是 credential 验证不跨租户；被禁用主体不能创建新 session；allow 使用一个可复现策略快照；缓存 key 包含影响结果的身份、策略和资源世代；高风险 action 不接受超过 deny freshness 的状态。若每秒请求 `Q`、在线检查比例 `r`，introspection 负载为 `Qr`；离线比例提高会降低中心负载，但撤销窗口至少为 `min(token_lifetime, revocation_propagation)`。

反例：员工离职后主体 generation 从 4 升 5，但旧 token 仍合法签名且过期在一小时后；只验签会继续放行。另一个反例是策略 v9 删除 admin 角色，缓存仅以 `{user,action}` 为键，旧 allow 继续命中；必须绑定 policy/resource generation 或使其 TTL 不超过 deny 边界。

## 必然约束

[DEDUCED:identity-authorization-service-authentication-does-not-prove-current-authorization] 有效签名只能证明某凭证由可信世代签发，不能证明主体在当前策略与资源状态下仍被允许执行该动作。资源可能换所有者、角色被移除、会话风险升高。认证结果是授权输入而非结论；即使 token 自含角色，高风险路径仍需校验可接受 policy 和 revocation generation。

[DEDUCED:identity-authorization-service-offline-tokens-bound-revocation-freshness] 完全离线验证令牌时，撤销生效下界受令牌寿命或撤销信息传播时间约束，无法同时获得无限离线可用与即时 deny。T0 签发一小时 token，T1 网络分区，T2 撤销；验证点没有新信息，在 T0 与 T2 世界观察相同，只能继续 allow 或统一拒绝。短 token、在线检查或推送 deny 都付出可用性和负载。

[DEDUCED:identity-authorization-service-policy-decisions-need-versioned-inputs] 授权决定必须记录身份、会话、策略和资源属性版本，否则审计无法复现允许原因，缓存也无法安全失效。仅记录“规则 allow”无法回答当时用户属于哪个组。版本化输入让审计证明决定，也让策略 v10 到达时淘汰 v9 缓存，而无需猜哪些规则受影响。

## 从简单方案演进

最简单基线是单租户服务端 session，每次请求在线读取用户状态和少量角色表。它新鲜且易审计。第一个待压测指标是 authorizer `p99` 超过业务延迟预算百分之十，或身份存储读取超过安全吞吐百分之六十；此时使用短期签名 session 和带 policy generation 的本地缓存，新增撤销延迟与密钥分发。

第二个待业务校准指标是高风险 deny `p99` 超过三十秒，或离职后仍成功的写请求出现一条；此时缩短该 action token、增加在线 introspection 或独立 deny watermark。三十秒取决于资产损失，普通内容读取可数分钟。关系图展开超过五层或候选边超一万导致尾延迟时，预计算组闭包，但更新需要 generation 和一致失效。

未选择所有请求都依赖中心授权 RPC，因为网络故障会阻断整个数据面；当动作极高风险、QPS 低且必须秒级撤销时它重新变优。未选择把完整长期权限放进长寿命 token，因为资源与策略变化无法及时反映；离线设备且权限固定时才可接受。

## 设计决定

登录验证 credential generation 与 MFA，issuer 在当前 signing epoch 下创建短 session；私钥由受限签发器持有，验证节点只拿公钥。策略以不可变 snapshot 发布，authorizer基于固定版本、主体/session generation、资源属性和 deny watermark 求值，返回带 reason 的有界 TTL decision。不同 action 定义 maximum stale deny。

issuer/policy worker 都持有单调 epoch。旧 worker 租约过期后返回时，签发接口和 policy head 拒绝低 epoch；已签但未交付 token 若使用旧撤销世代，高风险验证点拒绝。密钥轮换先分发新公钥、再签发、后停止旧 key，紧急泄露则将旧 key generation 加入 deny。响应丢失时登录可用稳定 challenge/session 请求查询，不能无限生成并发会话。

反选是只使用角色表，简单易懂；当资源类型少、租户小且角色稳定时重新变优。属性/关系模型只有在业务确实需要资源关系时引入，避免把任意代码塞入策略。

## 运行与演进

SLI 包括登录成功/失败、MFA 与恢复、授权 `p50/p99`、policy/revocation propagation、验证节点 snapshot age、deny 后成功请求、缓存命中、key generation 分布和审计缺口。过载先限制登录爆破和低风险目录查询，保留撤销、key 分发与高风险 introspection；身份服务降级不能默认把 unknown 变 allow。

演练：T0 token S/generation 12 签发，有效十分钟；T1 验证节点与控制面分区；T2 主体禁用，generation 13；T3 S 尝试转账。若该动作 freshness 两分钟且节点 deny watermark 已过期，必须 fail closed/在线检查；普通只读可按策略继续。另演练旧 issuer epoch 8 在新 issuer epoch 9 轮换后恢复，任何签发和 session head 更新均被拒绝。

待演练指标一是百分之九十九验证节点在十秒内 ACK emergency key revoke，否则暂停高风险 allow；二是 policy snapshot age 超过五分钟的节点超过百分之一时摘除高风险流量。阈值按地域 RTT 与风险校准。策略 schema 升级先双读并影子比较 decision diff，再灰度；资源服务更改属性版本时必须让缓存键或失效通道同步演进。审计防篡改并最小化敏感内容。

## 面试考察本质

给定“allow 必须由当前可接受的身份、会话、策略与资源世代共同证明，deny 要有风险对应的新鲜度”这一不变量，因为离线验证点无法知道刚发生的撤销，候选人应推导出短期凭证、版本化决策、撤销 watermark 和 fail-closed 边界，并按动作损失交换中心依赖、延迟与离线可用性。

优秀信号是先区分 authentication 与 authorization，明确 token 验签不是当前授权，写出策略/资源版本缓存键，并画网络分区中的撤销反例。常见误区是长 token + 黑名单却不谈传播、缓存只按用户键、密钥轮换瞬间删除所有旧公钥，或故障时统一 allow。

二十分钟覆盖主体、session、policy 与 token；四十分钟加入撤销、缓存、密钥轮换和审计；六十分钟讨论关系授权、租户隔离、账户恢复和多地域。本题独特本质是：信任不是一次认证后永久成立的布尔值，而是多个独立世代在有限新鲜度内形成的可解释决定。
