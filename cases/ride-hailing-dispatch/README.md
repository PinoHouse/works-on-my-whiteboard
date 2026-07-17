# 网约车调度

## 表面题目

乘客发起叫车，系统从当前可服务司机中发出 offer，得到明确接受后创建唯一行程。成功不是地图上找到最近车辆，而是等待、接驾时间、司机公平与区域供需之间取得可运营结果，同时守住“一个司机同一时刻至多承诺一个行程”。题名隐藏了司机位置、在线状态和意愿都在变化：索引命中只说明曾经在附近，不说明此刻仍有分配权。

## 反问与边界

先问派单是一对一顺序 offer、并行少量 offer 还是司机抢单；接受超时、乘客取消、司机拒绝怎样定义。确认匹配 p99、最大接驾 ETA、位置 max_age、热点区域峰值、车型/无障碍等硬资格、跨区调度和公平目标。是否允许同一司机收到多个未决 offer，与“接受多个”必须区分。价格、ETA 预测和防作弊可作输入但不是本题权威。待业务校准取消成本、司机空驶、公平窗口和无车时是否扩圈。

权威源是司机会话租约及 trip assignment 状态机；位置 cell 和可用司机列表只是派生候选。offer 服务拥有 offer 状态，司机 owner 分片以 lease_epoch 仲裁接受。更快位置刷新提高网络、索引迁移和移动端电量成本。

## 客观模型

命令为 `RequestRide(request_id,pickup,constraints)`、`Offer(offer_id,driver_id,lease_epoch,expires_at)`、`Accept(offer_id,response_id)` 与 `Cancel(actor,version)`。司机状态为 offline、available(epoch)、offered、assigned(trip_id)，trip 为 searching、assigned、cancelled。分配线性化点是司机当前 epoch 下 CAS `available -> assigned`，并幂等绑定 request_id。

候选 ETA 可近似 `pickup_eta = route(driver_location,pickup)+dispatch_latency`。位置 age 为 a、速度上界 v 时，误差至少含 `v*a`。若每轮给 b 个司机、单人接受概率 p，粗略轮次成功率 `1-(1-p)^b`，但并行度提高会增加被拒 offer 与司机干扰；该式只用于容量推导，p 需测量。

## 必然约束

[DEDUCED:ride-hailing-dispatch-location-index-cannot-grant-driver-ownership] 附近司机索引只能发现候选，不能授予司机所有权；最终分配必须由司机会话权威仲裁。两个乘客同时读到司机 D，若各自在本地删除索引后成功，就会创建两单；只有同一 owner 的 CAS 能让一个接受获胜。

[DEDUCED:ride-hailing-dispatch-acceptance-must-bind-driver-lease-epoch] 接单必须在当前司机租约 epoch 下原子提交，否则超时 offer、断线重连和重复响应会把同一司机分给多单。D 在 epoch 7 收 offer，断线后以 epoch 8 重连；迟到的 epoch 7 接受必须被 fencing，即使签名与 offer_id 合法。

[DEDUCED:ride-hailing-dispatch-radius-trades-recall-for-wait-and-deadhead] 扩大候选半径提高匹配召回，却必然增加接驾等待与司机空驶，不能把找到司机等同于高质量派单。半径从 2 km 扩到 10 km 可能多五倍候选，却让乘客等候和司机无载里程显著增加；扩圈应由 ETA/等待预算而非结果数量驱动。

## 从简单方案演进

单区域单调度器维护内存司机状态并顺序 offer，是最简单正确基线。可用司机数或请求率使单机 CPU 七成、状态恢复超 SLO 时，按地理 zone 分片；边界司机只归一个 owner，邻区索引仅引用候选。跨区候选增多会产生 owner fanout，因此当跨区 offer 超百分之十，按供需动态调整 zone，而非复制写权。

顺序 offer 对司机干扰小，却在多次超时后拉长等待；当搜索 p95 超目标一半且单轮接受率经测量偏低，可并行发有限 b 个 offer，以原子接受仲裁和明确“未获胜”通知收口。b 增大提升短期成功率，也提高无效通知和公平偏差。位置 age 超 `max_age/2` 时提升司机上报或主动探测，但要计移动端成本。

未选“广播给区域所有司机”，因热点放大与抢单公平难控；低密度紧急网络且司机数量很少时可重新变优。也未选全局最优批匹配作为每请求同步路径；只有可等待批窗口且整体空驶收益超过延迟时用于微批。

## 设计决定

选择区域候选索引、司机 owner 租约和有界并行 offer。请求先写幂等 request_id，再按车型、age 与 ETA 分轮取候选；发 offer 时读取当前 lease_epoch。接受请求在 owner 分片 CAS，并在同一提交记录 trip_id 与 offer winner；响应丢失后用 response_id 查询结果，不再分配第二司机。

offer 到期只阻止新的接受，已提交 assignment 不因过期回滚。乘客取消与接受并发由 trip version 仲裁，输家得到明确终态。索引流停滞时过滤超龄位置并缩小声明 coverage，不能把旧司机当可用。owner 不可用时不跨分片抢占其司机。未采用缓存删除作为锁，因为缓存失效和复制 lag 无法提供唯一所有权。

## 运行与演进

SLI 包括请求到接受 p50/p95、pickup ETA/实际偏差、stale candidate 比例、每单 offer 数、接受后冲突数、空驶距离、区域无车率、司机公平分布与取消率。过载先减少候选排序特征，再减小并行 b，最后按明确产品策略拒绝低优先级请求；绝不绕过 assignment CAS。

故障时间线：18:00 D/epoch7 收 O1；18:00:05 网络断开，租约到期；18:00:08 D 以 epoch8 重连；18:00:09 O1 迟到接受被 fencing；O2 在 epoch8 提交 trip T。另演练提交成功但响应丢失，重试返回同一 T。灰度新 zone 时双写位置索引但 owner 不变。压测指标是 owner CAS p99 超预算、跨区 fanout p95 超三；业务校准 max_age、最大接驾 ETA和并行 offer 上限。

## 面试考察本质

本题本质是：在“司机不能被双重承诺”的不变量下，因为位置、在线与接受意愿都只能滞后获知，候选人必须推导空间候选与司机 owner 仲裁的分离、lease epoch 和 offer 超时，再按等待、空驶、公平与移动端刷新成本选择派单范围。

优秀回答会画出司机/offer/trip 状态机，用双乘客反例否定索引锁，并处理接受响应丢失。常见误区是最近即最优、删除缓存即占用、offer 超时就回滚已接受。追问可进入跨区、批匹配、供需倾斜和位置作弊。20 分钟讲候选与 CAS，40 分钟补租约和取消竞态，60 分钟讨论公平、批处理、跨地域与指标实验。
