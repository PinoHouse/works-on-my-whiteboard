# 视频会议

## 表面题目

设计视频会议系统，让参与者加入房间、协商媒体、发布与订阅音视频轨，在网络变化下保持可理解的互动，并可选录制。成功必须分开控制面与媒体面：成员和权限提交、传输协商完成、媒体包在播放截止前到达、用户实际听见或看见。只有控制面和已提交录制段适合持久恢复，未录制实时帧过期即失去价值。

这道题不是可靠消息投递。音频包晚到 100 毫秒可能比丢弃更糟，因为等待会阻塞后续语音；会议成员显示在线也不证明 NAT 路径可达。核心取舍由码率、jitter、播放 deadline、端侧上行和 mesh/SFU/MCU 拓扑决定。

## 反问与边界

先问最大参与人数、是否多人同时开视频、端到端嘴到耳延迟、音频/视频质量底线、移动网络比例、是否需要端到端加密、录制、屏幕共享和区域合规。容量用参与数 `N`、每轨码率 `r`、订阅矩阵、分层编码和转发出口表达，并明确加入成功、首帧时间、卡顿率和音频可懂度 SLO。

还要确认主持人权限、踢出传播、重连是否创建新媒体会话、撤权 API 在控制库写入还是全数据面 enforcement barrier 后返回，以及录制成功的定义。安全边界包括成员世代、短期媒体租约、密钥轮换、未授权订阅、恶意码率和媒体节点隔离。非目标是补回未录制直播、保证每帧到达、为媒体建立全球总序，或从信令成功推导每个人都能听见。

## 客观模型

最小接口为 `Join(meeting,participant,permission_epoch)`、`Negotiate(transport,tracks)`、`Publish(track,media_epoch,lease)`、`Subscribe(track,layer)`、`RevokeAndFence(participant,expected_epoch)` 与 `CommitRecordingSegment(index,digest)`。权威状态是会议成员、权限世代、短期媒体租约、密钥/协商状态和录制段索引；RTP 包是短生命周期数据。数据面 enforcement points 是当前路径上的 SFU、mesh peers 和接收端媒体栈，而不是控制面路由表；它们按当前 epoch/密钥接受、转发或播放媒体。

不变量是：只有当前成员与权限 epoch 能发布/订阅轨；旧 transport epoch 的迟到包不能混入重连轨；同一 SSRC/epoch 的序号用于 jitter 处理而非跨轨总序；声明已完成的录制段索引必须可恢复。撤权开始时冻结实际媒体路径与 enforcement-point 集合；只有集合中每个 SFU 都已停止旧 epoch 的 ingress/转发、关闭旧 publisher/subscriber transport 并 ACK，且每个 mesh peer/接收端都已安装新 epoch/密钥、关闭或拒绝旧路径并 ACK，撤权才算 committed。无法 ACK 的点只有在旧 transport、短租约或旧密钥已被数据面可验证地失效后才能越过 barrier；从控制面路由摘除本身不计数。

嘴到耳延迟近似 `L=capture+encode+network+jitter_buffer+decode+render`。mesh 中每端上行约 `(N-1)×r`，连接边为 `N(N-1)/2`；SFU 让端侧上行约为 `r`，但总转发可接近 `N×(N-1)×r`，选择性订阅与分层编码可降低它。热点是单房间订阅矩阵和媒体出口，不是会议数平均值。

## 必然约束

[DEDUCED:video-conferencing-media-deadline-dominates-reliable-complete-delivery] 音频包 P 的播放 deadline 为采集后 80 ms，70 ms 时丢失，可靠重传在 120 ms 才到。等待 P 会延迟后续语音，跳过并用 PLC/FEC 掩盖只损失局部音质。因为过期包价值近零，媒体主路径必须优先及时性而非完整交付；文件共享或录制上传没有播放 deadline 时，可靠传输才重新变优。

[DEDUCED:video-conferencing-topology-is-bounded-by-participant-uplink-and-forwarding-fanout] mesh 随 `N` 增长要求每端发送多份媒体，弱上行先失败；SFU 只收一份，却把复制、拥塞控制和出口集中到媒体节点。不存在通用最优拓扑，必须由参与数、上行与订阅决定；MCU 可降低端侧解码，却新增服务器编解码成本与延迟。

[DEDUCED:video-conferencing-control-plane-membership-does-not-prove-media-reachability] Join 提交与 SDP/密钥协商只能证明允许尝试建立路径，NAT、路径切换、丢包或接收端解码仍会让媒体不可用。类似地，成员库推进 epoch 只记录撤权意图；只有全数据面 barrier 才证明当前路径已执行 fencing。因此断连后可精确保证成员/权限最终状态、已完成 barrier 的新 epoch 与已提交录制段；只能 best-effort 保证实时包、首帧、连续可听和界面展示。

[DEDUCED:video-conferencing-revocation-commits-at-every-media-enforcement-point] 主持人在控制库把 A 标成离会时，某 room SFU 可能仍用既有 transport 转发，mesh 中 A 也可绕过 SFU 直达 peer；因此数据库提交和路由摘除都不能支撑“媒体已停止”的声明。commit point 必须覆盖撤权开始时所有实际路径：SFU 停止旧 epoch ingress/转发并关闭旧 transport，mesh peers 与接收端安装新 epoch/密钥并拒绝旧路径。每个点要么 ACK 执行，要么等其短租约、连接或旧密钥在数据面可验证地失效；否则撤权保持 `PENDING`。本地 fence 前已经发到网络、解密或进入播放缓冲的包仍可能播放，精确边界是 barrier 后旧 epoch 不再被当前路径接受、转发或新入播放队列。

## 从简单方案演进

最简单基线是小房间 mesh：控制服务协调成员与信令，端点直接交换媒体。它成本低且路径短，但上行随人数增长，也无法只靠服务端 SFU barrier 提供强撤权。要求强撤权的房间默认使用 SFU；若小房间曾启用 mesh，撤权前先迁移到 SFU，并等待所有保留成员关闭 direct transport、安装新 epoch/密钥并 ACK，未 ACK peer 的旧短租约失效后才进入 SFU barrier。当人数超过待校准的 4，或预测 `(N-1)×r` 达到任一端可用上行的 60%，也因容量切到 SFU；这降低端侧上行，却新增集中出口、房间放置与转发节点故障。

当 SFU 单房间出站达到节点安全容量的 70%，启用选择性订阅、分层编码和房间级媒体节点，先降低不可见视频层。若音频 jitter-buffer 欠载率超过 1%，或丢包 `p95` 超过 3%，降低视频码率、启用 FEC/PLC 并评估换路径；音频优先于视频清晰度。

当 jitter buffer `p95` 已占互动延迟预算的 40%，不再继续加长 buffer，而是降层、换区域或切路径，因为完整度继续增加会破坏对话。4 人、60%、70%、1%、3% 与 40% 都是待压测、网络演练和产品校准的初始参数；调低更早降质且成本增加，调高更容易卡顿或过载。

未选择默认 MCU。大规模语音混音、统一录制布局或低端终端无法解码多轨时，服务器混音才重新变优。

## 设计决定

本设计把成员、权限、短媒体租约、密钥与媒体 epoch 放在可靠控制面，媒体面使用 SFU 为主、小房间可选 mesh。撤权先冻结当前 SFU、direct mesh transport 与接收端集合，再提升 room permission epoch、轮换会话密钥并下发 fence。每个 SFU 停止接受和转发旧 epoch、清空未发旧队列、关闭旧 publisher/subscriber transport 后 ACK；每个保留 peer/接收端关闭 direct transport、安装新 epoch/密钥并承诺拒绝旧 epoch 后 ACK。失联 enforcement point 不能因路由摘除直接算完成，只能等其短期 fencing token/媒体租约到期、旧连接终止且接收端旧密钥拒绝已可验证；任何一点尚未 ACK 或失效时，API 都返回 `PENDING`。

撤权故障时间线是：0 ms A 在含 SFU-1 和一条 direct mesh path 的 epoch 5 房间发布；20 ms 主持人发起撤权，协调器冻结路径集合、写 epoch 6 并轮换密钥；35 ms SFU-1 停止旧转发、关闭旧 transport 后 ACK，peer B 关闭 direct transport 并 ACK 新密钥；peer C 失联但旧 mesh lease 到 120 ms 才到期。50 ms 即使控制路由已移除 C，撤权仍为 `PENDING`；120 ms 接收端媒体栈可验证地拒绝过期 lease/旧密钥，所有实际路径失效，barrier 才返回 committed。各 enforcement point 本地 fence 前已经发到网络或进入 jitter buffer 的包仍可能播放；120 ms 后旧 epoch 不再被当前路径接受、转发或新入播放队列。

未采用“把未 ACK SFU 从控制路由摘除就算撤权完成”，因为它没有关闭既有 publisher/subscriber transport；也未让 mesh 房间宣称仅由 SFU barrier 提供强撤权。无法完成 mesh-to-SFU 路径排空、peer ACK 或短租约失效时，强撤权保持 `PENDING`；若产品宁愿固定暴露窗与更高可用性，可只承诺短租约上界，但不能返回强撤权 committed。另未采用以 TCP 式可靠有序流承载所有音视频，因为队首阻塞会让过期帧拖慢新帧。

## 运行与演进

核心 SLI 是加入成功与首媒体时间、嘴到耳延迟、音频丢包/PLC 比例、jitter-buffer 欠载与长度、视频冻结、每轨码率、SFU 房间出口、撤权 barrier 年龄、未 ACK/未失效 enforcement-point 数、旧 transport 关闭与接收端旧 epoch 拒绝、录制段缺口。按会议、拓扑、地区、网络类型和设备能力分桶。过载先降不可见视频层、分辨率和帧率，再暂停非必要视频；音频和控制面最后降级。

演练移动端 Wi-Fi 切蜂窝，注入乱序与旧路径恢复，验证新 transport epoch fencing、音频保持可懂且延迟不无界增长；再分别让 room SFU 和 mesh peer 在撤权时分区，验证控制库写入或路由摘除后 API 仍为 `PENDING`，旧 SFU transport 关闭 ACK 或短租约/密钥失效、mesh 接收端安装新 epoch 后 barrier 才完成，同时把本地 fence 前已发出的在途包计入允许边界。媒体节点故障演练验证控制面重分配与关键帧恢复。协议升级先协商双方能力、灰度新编解码器并保留旧 codec 回退；已产生录制段格式必须长期可读。

房间按参与者网络与数据驻留选择媒体区域，控制面多地域复制不能替代媒体路径测量。密钥轮换与踢出提升权限世代，媒体节点只持短期密钥。成本按入站码率、订阅转发出口、转码 GPU/CPU 和录制存储分别归因。

## 面试考察本质

给定“撤权只在所有当前 SFU、mesh peers 与接收端 ACK 执行或其旧数据面能力可验证失效后提交，此后旧 epoch 不再被当前路径接受、转发或新入播放队列；互动媒体必须在播放截止前到达”的不变量，因为控制库提交和路由摘除都不等于既有 transport 终止、mesh 又可绕开 SFU，候选人应枚举实际 enforcement points、定义短租约/密钥与全路径 barrier，并明确在途包边界；再依据强撤权要求、参与人数、jitter、丢包和拓扑扇出选择 mesh、SFU、MCU 及降质顺序。

优秀信号包括写出延迟分解、指出迟到重传反例、区分 join 与媒体可达、解释音频优先和新 transport epoch。常见误区是把媒体放进可靠队列、只讨论信令、不算 SFU 出口，或为提升完整度无限拉长 jitter buffer。

二十分钟回答覆盖控制/媒体面、mesh 与 SFU；四十分钟加入拥塞、jitter、FEC 和故障时间线；六十分钟再讨论多地域、密钥、录制、编解码迁移与 MCU。追问应不断要求说明“这个包还有没有播放价值、哪个状态可恢复、哪两个指标触发拓扑或码率切换”。
