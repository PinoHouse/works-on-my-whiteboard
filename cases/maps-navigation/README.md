# 地图与导航

## 表面题目

给定起终点、交通方式和偏好，系统返回一条可执行路线、预计到达时间，并在现实变化足够大时重规划。成功不是永远知道“此刻绝对最短路”，而是路线相对于声明的路网与交通快照可解释，禁行约束不被突破，估时和最优性误差有边界。地图展示、地址检索和导航虽然相邻，本题核心是动态图上的受约束路径。

## 反问与边界

先问步行、驾车或公交，优化时间、距离、费用还是多目标；是否必须精确最优，允许多少次优，离线能否用，交通变化多久生效。确认 route p99、路网规模、更新率、长途/城市查询偏斜、封路传播 SLO、GPS 噪声与错过转弯处理。安全约束如单行、限高、禁行应是硬过滤，拥堵时间是软权重；还要确认封路允许短租约界定的暴露窗口，还是必须以区域 ACK barrier 阻断服务直到执行节点确认。业务需校准 ETA 误差、重规划收益阈值与驾驶干扰成本，本题不虚构这些数字。

权威源是版本化道路拓扑与已验证限制；实时交通是带观测时间和置信度的边权估计。预计算层级、landmark、路由缓存是派生索引。每条路线记录 graph_epoch、traffic_epoch、算法版本，以及经过安全 tile 的 required/observed restriction generation 和限制租约到期时间。

## 客观模型

路网为有向图 `G=(V,E)`，边 e 有硬资格与非负成本 `w_e(t)`。路线成本 `C(P,t)=Σ w_e(t)`，快照最优 `C*`；若返回 P，可声明 gap `C(P)/C*-1`，只有算法或对照计算能证明。ETA 则是未来边权预测，不能由当前最短路自动保证。

查询为 `Route(origin,destination,constraints,snapshot)`，更新为 `ApplyEdge(edge_id,version,restriction,weight,observed_at)`。全量 Dijkstra 成本近似 `O((V+E)logV)`；千万边每次扫描不可接受。交通 age 和 coverage 必须随结果输出，预计算启发式若基于距离可给下界，若掺入可能高估的拥堵就失去最优保证。

硬限制与软权重采用不同发布通道。限制权威按安全 tile 发布单调 `required_generation(tile)`；路由节点记录实际安装完成的 `observed_generation(tile)`，结果释放谓词是对候选路径每个 tile 都满足 `observed>=required` 且权威签发的短租约仍新鲜。节点不能靠“我没收到 invalidation”推断没有更新，因为它尚不知道哪些边刚变；它必须主动取得路径 tile 的当前高水位，或参加更严格的区域 ACK barrier。traffic generation 则可以按产品 SLO 接受有限 age。路线缓存键至少含起终点映射、交通方式、约束摘要和图代际；只用经纬度会把货车限高与普通轿车错误共用。查询扇出还取决于 map matching 候选数，GPS 漂移可能让一个起点同时对应多条道路。

## 必然约束

[DEDUCED:maps-navigation-realtime-optimum-is-snapshot-relative] 交通边权持续变化时最优路线只能相对于一个可声明快照成立，更快刷新以计算、发布和路径抖动为代价。路线计算历时两秒期间前方事故发生，任何结果都可能在返回瞬间过时；不能从“用了实时流”推出绝对实时最优。

[DEDUCED:maps-navigation-optimal-search-needs-admissible-heuristic] 若声称给定快照下最优，启发式估价就不能高估剩余成本，否则提前终止可能遗漏真实最短路。两条路线真实为 10 和 11，若对前者剩余估为 12，搜索可先接受 11 并错误终止。要么证明 admissible/consistent，要么诚实声明近似 gap。

[DEDUCED:maps-navigation-hard-restriction-requires-observed-generation] 硬限制在控制面提交不等于路由节点已经执行，节点只有证明已观察路径安全区域所需代际或通过 ACK barrier 后才能释放路线。反例：封路 generation 71 已写入控制面，而节点仍用本地 70 命中缓存；若把“提交成功”当全球生效，它会继续返回被封道路。短租约方案把最坏旧限制窗口界定为租约 TTL，并在过期或权威未知时 fail-closed；要求更强边界时，区域入口先阻断新请求，只有安装并 ACK 71 的路由池才能恢复，真正边界是 enforcement observation/ACK 而非数据库提交。

重规划收益还必须超过绕行、驾驶认知和路线抖动的切换成本。连续噪声让两条差 10 秒的路线交替领先，逐更新切换会造成掉头；因此即使软交通权重已观察，也需迟滞、最短保持期与安全位置，这与硬限制必须执行是两种不同语义。

## 从简单方案演进

小图用 Dijkstra 是正确基线。路网扩大到搜索访问节点 p95 超十万或 route p99 越界时，加入可采纳 A* 启发式；仍不足再用层级路网/landmark 预计算。它降低查询，却让拓扑更新需要重建。若 closure 发布到路由节点实际 observed 的 lag 超 SLO，硬限制走小型 overlay 降低安装成本；但 overlay 被控制面提交仍不代表生效，释放门仍检查节点观察水位或 barrier ACK。

交通权重频繁变化时，以 traffic overlay 覆盖稳定基础图；当 overlay 边比例超百分之五或查询触碰 overlay p95 超预算，重建基础权重。对热门 OD 缓存路线可节省 CPU，但封路必须以 graph epoch 撤销。刷新间隔缩短一半近似增加一倍权重发布和缓存淘汰，并可能提高 reroute churn。

未选全量 all-pairs shortest path，状态为 `O(V²)`；仅小型静态园区时重新变优。未选择每个交通事件立即推送所有行程，因为扇出巨大；只有影响 corridor 且预计收益超过阈值的行程重算。

## 设计决定

选择版本化基础图、硬限制 overlay、交通权重 overlay 与 A*/层级查询。请求固定 graph/traffic epoch，先 map-match 起终点，硬过滤已安装的禁行边，再求候选路径；释放前向限制权威读取路径所有安全 tile 的当前 required generation，比较节点 observed 水位和短租约。若任一 tile 落后，节点先安装对应 overlay、重新求路并记录 observation；若权威未知、租约过期或安装超时，则返回该安全区域暂不可用，而不是把旧路线降级输出。这样无需节点预先知道哪条边刚更新。封路需要零旧读窗口时，区域入口建立 ACK barrier，阻断未 ACK 代际的执行池；普通限制可用短租约明确界定最坏 freshness。交通缺失则可回退历史分时权重并标记置信度。

更新按 edge_id/version 幂等，旧事件不回退权重。查询超时可返回已知上界路线与未证明 gap，不能标称最优；安全限制高水位不可证明时，无法知道受影响边集合，故不能只避开本地已知边，而要 fail-closed。重规划仅在新 ETA 改善超过切换成本且路线尚未进入不可安全变更区域时触发。未选“缓存命中直接返回”，缓存仍需上述 observation/lease 门校验。

## 运行与演进

SLI 是 route p99、访问节点数、traffic age、restriction required-observed gap、限制租约 age、barrier ACK 率、路线失败率、ETA 绝对误差、可证明 gap、重规划率与用户拒绝重规划率。过载先降低备选路线数，再用较粗交通层，最后回退静态路线；硬封路不可忽略，安全代际未知时宁可返回不可用。路况来源需防伪并限制单来源影响。

故障时间线：08:00 节点 observed 70，以 graph 70/traffic 901 返回；08:01 边 E 封闭，权威提交 tile required 71，但此刻不能声称节点已执行；08:01:01 缓存旧路线命中，释放门读取到 required 71 而本机仍为 70，于是阻断输出；08:01:03 overlay 安装完成并 ACK/observe 71，重算后才返回绕行路线；08:05 基础图重建。若 08:01 后权威不可达且短租约已过期，返回不可用；若产品接受租约 TTL 内旧读，就把该 TTL 明示为封路传播上界。演练 traffic 流停滞，系统暴露 age 并回退而非冻结“实时”标签。灰度新启发式在影子查询与 Dijkstra 小样本比较最优性。压测切换指标是访问节点 p95 十万、overlay 命中五成；业务校准 ETA 误差、restriction TTL 和 reroute 收益阈值。

## 面试考察本质

本题本质是：给定“只有执行节点已观察的硬道路约束才算生效、路线质量必须相对快照可解释”的不变量，因为未来交通不可知、节点无法从未收到失效消息证明没有新限制且动态图索引更新有成本，候选人必须推导权威 generation 到 observation/ACK 的执行边界、最优性证明与重规划稳定性的取舍，再按安全 freshness、ETA 与计算预算选择租约、barrier 和刷新频率。

优秀回答会区分拓扑、硬限制、交通估计和 ETA，说明 A* 可采纳条件及快照最优边界，并把控制面提交与执行节点 observation 分开。常见误区是背最短路算法、声称实时即最优、假定失效消息必达、每次权重变化都重路由。追问可进入层级图更新、公交时刻、离线地图和 map matching。20 分钟讲图模型，40 分钟补 overlay 与限制水位，60 分钟讨论最优性验证、短租约/ACK barrier、预测误差和全球发布。
