# 内容分发网络

## 表面题目

设计 CDN，是把可缓存响应放到离用户更近的边缘，在满足内容版本、访问控制和新鲜度契约的前提下降低时延与源站出口。请求先映射到边缘，边缘根据完整 cache key 判断 fresh、stale 或 miss，必要时回源、验证并原子填充。成功不能只看 HTTP 200：响应必须属于当前请求的租户、语言、编码与授权范围，并明确是新鲜、允许的 stale，还是回源失败。

CDN 不拥有业务“当前内容”的最终事实；源站版本与授权控制面拥有。边缘拥有某个不可变 generation 的副本以及它观察到的 freshness/purge 状态。公开静态对象、个性化页面和私有下载的 stale 规则不同，不能用一个 TTL 包打天下。本文覆盖分层边缘、回源保护、失效与 purge，不假设全球节点可瞬时同步。

## 反问与边界

要问对象大小、读写比、热门度、可缓存状态码、地域、源站容量、目标命中率与首字节 SLO。内容通过版本 URL 永不变，还是同 URL 更新；TTL 到期必须阻塞回源还是可后台 revalidate；源站故障可 stale 多久；purge 是运营纠错还是安全撤权。cache key 是否受 host、path、query、method、语言、压缩、设备、cookie 和授权范围影响，需要逐项从响应差异推导。

设总请求率 `Q`、命中率 `h`、平均对象字节 `b`，理想回源请求约 `Q(1-h)`，出口约 `Q(1-h)b`；但热点 TTL 同时过期时，若 `m` 个边缘各有 `c` 个并发 miss，瞬时可到 `m×c` 次，平均命中率掩盖风暴。全球复制容量约 `Σedge cached_bytes`，缓存小会 churn，过大则复制冷数据。阈值必须以源站突发承受力和真实 Zipf 偏斜校准。

## 客观模型

实体包括 `CacheKey`、`ObjectGeneration(digest,headers,body)`、`Freshness(fresh_until,stale_windows)`、`PurgeOperation(epoch,selector)`、`EdgeObservation(last_purge_epoch)` 与 `OriginPolicyGeneration`。key 由规范化 scheme/host/path/query 和所有响应变体维度构成；私有响应还需分区到授权主体或只缓存字节并在每次命中校验短令牌。条目写临时对象，摘要与长度完成后原子 ready。

读取状态机为 fresh hit、允许的 stale hit、single-flight miss/revalidate、明确失败。请求合并只在完整 key 一致时共享。TTL 是“无需联系源站可服务”的时间，不是对象到时自动从全球消失；条件回源可把 304 转成新 freshness。purge 控制面先持久化递增 epoch，再扇出各边缘确认，查询返回已确认范围和未完成节点，而非一个误导性的布尔值。

若边缘到用户 RTT `Le`、回源额外 RTT `Lo`，命中首字节近似 `Le`，miss 至少 `Le+Lo+origin_compute`。服务 stale 能去掉 `Lo`，却可能返回旧事实。权威边界按内容类别写入策略：版本化公开资产可长 stale；价格或库存短 stale；撤权对象当前 deny generation 高于条目观察值时绝不能返回。

## 必然约束

[DEDUCED:cdn-cache-key-must-cover-response-variance] 若 `/report` 根据租户 cookie 返回 A 或 B 的数据，而 key 只有 path，A 首次填充后 B 会命中 A 字节。语言、压缩和签名范围也同理。所有改变字节或资格的输入必须入 key、禁止缓存，或在命中后重新校验；不存在靠“通常相同”保证隔离的正确方案。反之把无关随机 query 全入 key 会遭 cache busting，因此需规范化白名单。

[DEDUCED:cdn-purge-is-a-distributed-propagation-process] 控制面在 0 秒接受 purge，边缘 E1 在 100 毫秒确认，断网 E2 十分钟后才收到。在这十分钟内“全球已删除”不成立。系统可通过短 TTL、请求时检查 epoch 或阻断未追平节点缩小窗口，但都付出回源、控制查询或可用性成本。purge API 必须区分 accepted、覆盖比例、完成与超时。

[DEDUCED:cdn-stale-availability-must-not-cross-authorization-revocation] 源站宕机时，公开 logo 服务昨天版本通常优于失败；但员工在撤权后若边缘仍以 stale 私有报表服务，便是越权。stale 策略不能只按错误类型，还要按对象敏感度与当前授权代际。无法联系授权权威时，高风险资源 fail-closed，低风险公开资源才可 fail-stale。

## 从简单方案演进

最简单基线是单源站加浏览器缓存，适合低流量单地域。源站出口持续超过预算 60%，或远区首字节 `p95` 超过目标两倍时，引入一个反向代理，先缓存版本化公开对象。多地域用户增长后按就近路由增加边缘；代价是副本、配置偏差和 purge 扇出。命中率不是唯一切换指标，必须同时观察 offload 与用户延迟。

热点 miss 并发超过源站单 key 安全并发，加入 single-flight、分层 shield 和带抖动 TTL。若源站错误率超过待校准 2% 且对象允许 stale，启用 stale-if-error；恢复后后台 revalidate。两组待演练阈值是：多少边缘同刻 miss 会击穿源站连接池；purge 95%/99.9% 确认分别要多久，安全对象在未确认节点应摘流还是请求校验。具体百分比由风险分级决定。

未选择所有对象统一短 TTL，因为它把更新传播问题变成周期性回源风暴；当内容高度动态、源站计算便宜且错误 stale 的损失很高时，短 TTL 或不缓存重新更优。未选择每次更新广播全量删除作为唯一正确性机制，因为离线边缘无法瞬时确认；若节点很少、专网可靠且必须秒级更新，它可配合 ACK barrier 使用。

## 设计决定

生产路径优先使用内容寻址或版本 URL，边缘缓存不可变 generation；逻辑 URL 用短元数据解析到 generation。miss 由 shield 级按完整 key 合并，回源响应校验长度和摘要后原子填充。TTL 到期根据策略同步 revalidate 或先 stale 后后台更新。purge 带单调 epoch，边缘持久记录最后应用值，控制面报告确认集合并对高风险选择器设置 barrier。

过载从低可见到高可见：停止冷对象预取；压缩日志与非关键统计；对公开版本资产延长 stale；限制大对象冷 miss 并优先小对象/已热对象；将非关键动态图回源排队；最后拒绝新冷 miss。私有撤权对象不进入 stale 降级。反选把边缘视为独立真相并允许写入，因为冲突、合规和源站回放复杂；只有离线优先的边缘应用且具备明确合并协议时才考虑。

## 运行与演进

SLI 按内容类别统计 hit、byte hit、origin offload、首字节、miss collapse、eviction、stale 年龄、key cardinality、purge 接受/95%/100% 延迟、授权代际拒绝和错误内容事件。切换指标一：byte hit 低于 request hit 20 个百分点且源站出口超限时，优化大对象策略；切换指标二：单 key 回源并发 `p99` 超过源站安全份额时增加 shield 或预热。数值必须通过故障压测和账单校准。

故障时间线：0 秒公开对象 G7 fresh；10 秒 TTL 到期且源站断网，按策略服务带 Age 的 G7；20 秒私有对象策略从 P3 撤到 P4，边缘虽有 G7 也因 deny epoch 拒绝；30 秒 purge 8 接受，E1/E2 确认、E3 断网，控制面只报告部分完成；摘除 E3 后才达到安全 barrier。再恢复源站，验证合并 revalidate 只有一次而不是百万次。

配置升级先在影子边缘计算新旧 key 与缓存资格差异，检测租户、语言和编码串键，再灰度区域。回滚保留双读旧 generation，但不能回滚 deny epoch。日志不记录原始签名与敏感 query；租户配额覆盖缓存容量、回源带宽与 purge 频率，防止一个租户用随机键驱逐他人。

## 面试考察本质

本题本质是：给定“边缘只能返回完整 key 对应、满足 freshness 且仍获授权的 generation”，因为全球副本状态和 purge 到达情况无法瞬时获知，边缘容量与源站出口又有限，候选人应推导版本键、请求合并、可观测传播，并按内容风险在 stale 可用性与新鲜/撤权之间决策。

优秀回答会从响应变体推 key，算 `Q(1-h)` 但指出平均值掩盖同步过期，明确 TTL、revalidate、purge 和授权不是同一概念，并构造离线边缘反例。常见误区是只画 DNS 到边缘、追求命中率不看字节、把控制面 accepted 说成全球删除、用 stale 服务私有撤权，或让随机 query 无界膨胀 key。

二十分钟完成 key、状态机、容量与源站保护；四十分钟加入层级、失效、stale 分类和故障；六十分钟再讨论多租户、安全 barrier、灰度和成本。考察点不是背缓存算法，而是能否指出缓存何时有资格代表权威、何时只能承认自己不知道，以及不知道时哪类请求可以旧而可用、哪类必须失败。
