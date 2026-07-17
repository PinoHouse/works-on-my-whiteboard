# 网约车调度

## 表面题目

乘客发起叫车，系统从当前可服务司机中发出 offer，得到明确接受后创建唯一行程。成功不是地图上找到最近车辆，而是等待、接驾时间、司机公平与区域供需之间取得可运营结果，同时守住“一个司机同一时刻至多承诺一个行程”。题名隐藏了司机位置、在线状态和意愿都在变化：索引命中只说明曾经在附近，不说明此刻仍有分配权。

## 反问与边界

先问派单是一对一顺序 offer、并行少量 offer 还是司机抢单；接受超时、乘客取消、司机拒绝怎样定义。确认匹配 p99、最大接驾 ETA、位置 max_age、热点区域峰值、车型/无障碍等硬资格、跨区调度和公平目标。是否允许同一司机收到多个未决 offer，与“接受多个”必须区分。价格、ETA 预测和防作弊可作输入但不是本题权威。待业务校准取消成本、司机空驶、公平窗口和无车时是否扩圈。

权威源有两个不能假装成同一事务域的 owner：司机 owner 持有会话租约和占用状态，request/trip owner 持有该请求的唯一 winner 与行程状态；位置 cell 和可用司机列表只是派生候选。offer token 连接两个 owner，跨 owner 收口依赖幂等状态机与对账，而不是一次虚构的原子写。更快位置刷新提高网络、索引迁移和移动端电量成本。

## 客观模型

命令为 `RequestRide(request_id,pickup,constraints)`、`Offer(offer_id,driver_id,lease_epoch,expires_at)`、`Accept(offer_id,response_id)` 与 `Cancel(actor,version)`。司机状态为 `offline`、`available(epoch)`、`reserved(token,request_id,epoch,expires_at)`、`assigned(trip_id,token)` 或 `released`；request/trip 状态为 `searching`、`winner_pending(driver_id,token)`、`assigned`、`cancelled`。司机 owner 的线性化点是当前 epoch 下 CAS `available -> reserved`，它只保护该司机；一单唯一司机的线性化点另在 request owner CAS `searching -> winner_pending(token)`。两个点不跨分片原子，靠 token 化 propose/finalize 状态机收敛。

候选 ETA 可近似 `pickup_eta = route(driver_location,pickup)+dispatch_latency`。位置 age 为 a、速度上界 v 时，误差至少含 `v*a`。若每轮给 b 个司机、单人接受概率 p，粗略轮次成功率 `1-(1-p)^b`，但并行度提高会增加被拒 offer 与司机干扰；该式只用于容量推导，p 需测量。

## 必然约束

[DEDUCED:ride-hailing-dispatch-parallel-offer-needs-request-winner] 司机 owner 的 CAS 只能防止一名司机接两单，并行 offer 还必须由 request/trip owner 对 winner token 做唯一 CAS，才能防止一单匹配多名司机。反例：同一请求并行发给 D1、D2，两名司机分别在各自 owner 上从 available CAS 成功；两个 CAS 互不冲突，若各自直接建 trip，就会得到两个“赢家”。因此每个成功接受先产生 driver reservation，request owner 只能让一个未过期 token 从 searching 赢得 winner CAS，输家必须按 token 释放。

[DEDUCED:ride-hailing-dispatch-acceptance-must-bind-driver-lease-epoch] 接单预约必须在当前司机租约 epoch 下把 available 原子变为 `reserved(token,request,epoch)`，否则超时 offer、断线重连和重复响应会把同一司机分给多单。D 在 epoch 7 收 offer，断线后以 epoch 8 重连；迟到的 epoch 7 接受必须被 fencing，即使签名与 offer_id 合法。后续 finalize/release 也必须同时匹配 token 和 epoch，不能释放新会话的占用。

[DEDUCED:ride-hailing-dispatch-radius-trades-recall-for-wait-and-deadhead] 扩大候选半径提高匹配召回，却必然增加接驾等待与司机空驶，不能把找到司机等同于高质量派单。半径从 2 km 扩到 10 km 可能多五倍候选，却让乘客等候和司机无载里程显著增加；扩圈应由 ETA/等待预算而非结果数量驱动。

## 从简单方案演进

单区域单调度器维护内存司机状态并顺序 offer，是最简单正确基线。可用司机数或请求率使单机 CPU 七成、状态恢复超 SLO 时，按地理 zone 分片；边界司机只归一个 owner，邻区索引仅引用候选。跨区候选增多会产生 owner fanout，因此当跨区 offer 超百分之十，按供需动态调整 zone，而非复制写权。

顺序 offer 对司机干扰小，却在多次超时后拉长等待；当搜索 p95 超目标一半且单轮接受率经测量偏低，可并行发有限 b 个 offer。并行意味着可能有多个司机 owner 同时预约成功，必须增加 request owner winner CAS、输家释放和孤儿 reservation 对账，不能只靠各司机 CAS。b 增大提升短期成功率，也提高暂时占用、无效通知和公平偏差。位置 age 超 `max_age/2` 时提升司机上报或主动探测，但要计移动端成本。

未选“广播给区域所有司机”，因热点放大与抢单公平难控；低密度紧急网络且司机数量很少时可重新变优。也未选全局最优批匹配作为每请求同步路径；只有可等待批窗口且整体空驶收益超过延迟时用于微批。

## 设计决定

选择区域候选索引、司机 owner 租约、request owner winner 和有界并行 offer。请求先由 request owner 幂等创建 searching，再按车型、age 与 ETA 分轮取候选；每个 offer 携带 owner 签发的 `token,request_id,driver_id,lease_epoch,expires_at`。接受先在司机 owner CAS `available(epoch) -> reserved(token,request,epoch,expires)`；多个司机可暂时预约同一请求。协调器把这些 token 提交给 request owner，只有一个仍有效 token 能 CAS `searching -> winner_pending(driver,token)`，随后司机 owner 幂等 CAS `reserved(token) -> assigned(trip,token)`，ACK 后 request owner 才发布 assigned；任何阶段重试都按 token 返回同一状态。

输家收到 `Lose(token)` 后只在 token/epoch 匹配时 `reserved -> released/available`，不会误释放后来的行程。若进程在预约后、winner CAS 前崩溃，sweeper 查询 request owner：明确 loser、cancelled 或未选择且 token 已过期才释放；若该 token 已是 winner 就重试 finalize；request owner 不可达时保持隔离而非冒险重新可用。若在 winner CAS 后崩溃，对账从 durable `winner_pending` 完成 driver finalize。乘客取消也在 request owner 与 winner CAS 竞争版本，取消获胜则所有 token 补偿释放，winner 获胜则进入正常行程取消。offer 到期只阻止新的 winner 决策，不能推翻已持久化 winner。

索引流停滞时过滤超龄位置并缩小声明 coverage，不能把旧司机当可用。司机 owner 不可用时不跨分片抢占其司机。未采用缓存删除作为锁，因为缓存失效和复制 lag 既不能保护司机，也不能给 request 选出唯一 winner。

## 运行与演进

SLI 包括请求到接受 p50/p95、pickup ETA/实际偏差、stale candidate 比例、每单 offer 数、同请求 reservation 数、winner CAS 冲突、winner_pending age、孤儿 reservation age、补偿释放失败、空驶距离、区域无车率、司机公平分布与取消率。过载先减少候选排序特征，再减小并行 b，最后按明确产品策略拒绝低优先级请求；绝不绕过 driver reservation 或 request winner CAS。

故障时间线：18:00 同一请求 R 的 D1/tokenA、D2/tokenB 分别预约成功；18:00:01 request owner 让 tokenA 赢得 winner CAS，通知 D2 释放；协调器随即崩溃。对账看到 R 的 durable winner_pending(A)，重试 D1 finalize 并释放 B，最终只得到 trip T。另演练 D/epoch7 收 offer 后断线并以 epoch8 重连，epoch7 的迟到接受被 fencing；演练 winner 提交成功但响应丢失，重试仍返回同一 T。灰度新 zone 时双写位置索引但 owner 不变。压测指标是 driver/request owner CAS p99 超预算、winner_pending age 越界、跨区 fanout p95 超三；业务校准 max_age、最大接驾 ETA 和并行 offer 上限。

## 面试考察本质

本题本质是：在“一个司机不能被双重承诺、一个请求也只能有一个赢家”的双向不变量下，因为位置、在线与接受意愿都只能滞后获知且两个 owner 不能假装成一个原子事务，候选人必须推导空间候选、driver reservation、request winner CAS、lease epoch 与补偿对账，再按等待、暂时占用、空驶、公平与移动端刷新成本选择并行度和派单范围。

优秀回答会画出司机 reservation 与 request winner 两套状态机，用双乘客反例否定索引锁、用同一请求两名司机都预约成功的反例否定“各司机 CAS 就够了”，并处理 winner 响应丢失和孤儿释放。常见误区是最近即最优、删除缓存即占用、offer 超时就回滚已持久化 winner。追问可进入跨区、批匹配、供需倾斜和位置作弊。20 分钟讲候选与双 owner CAS，40 分钟补租约、取消竞态和补偿，60 分钟讨论公平、批处理、跨地域与指标实验。
