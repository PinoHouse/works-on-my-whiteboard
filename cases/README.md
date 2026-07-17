# 系统设计题索引

这里按 [`scope.yaml`](../scope.yaml) 冻结的问题族顺序收录 75 道规范题目。每道题都已有一篇完整的第一性原理文章；清单中的 `draft` 是实验与证据生命周期状态，不表示正文缺失。当前只有“分布式限流器”完成了实验、来源与不可变证据的严格依赖闭环。

## 寻址与流量

- [短链接服务](./url-shortener/README.md)
- [文本分享服务](./pastebin/README.md)
- [分布式 ID 生成器](./distributed-id/README.md)
- [DNS 与服务发现](./dns-service-discovery/README.md)
- [负载均衡器与 API 网关](./load-balancer-api-gateway/README.md)
- [分布式限流器](./distributed-rate-limiter/README.md)（实验与证据已闭环）
- [一致性哈希路由器](./consistent-hash-router/README.md)

## 分布式存储

- [键值存储](./key-value-store/README.md)
- [分布式缓存](./distributed-cache/README.md)
- [分布式 SQL 数据库](./distributed-sql/README.md)
- [宽列与文档存储](./wide-column-document-store/README.md)
- [对象存储](./object-storage/README.md)
- [云文件同步](./cloud-file-sync/README.md)
- [时序数据库](./time-series-database/README.md)

## 信息流、社交与排序

- [社交信息流](./social-news-feed/README.md)
- [图片分享](./photo-sharing/README.md)
- [问答与新闻聚合](./qa-news-aggregation/README.md)
- [Top-K 与高频项统计](./top-k-heavy-hitters/README.md)
- [排行榜](./leaderboard/README.md)
- [评论与互动](./comments-reactions/README.md)
- [推荐系统](./recommendation-system/README.md)
- [广告投放与排序](./ad-serving-ranking/README.md)

## 实时通信与协作

- [聊天与即时通信](./chat-messenger/README.md)
- [通知投递](./notification-delivery/README.md)
- [分布式邮件服务](./distributed-email-service/README.md)
- [Webhook 投递](./webhook-delivery/README.md)
- [在线状态服务](./presence-service/README.md)
- [协同编辑器](./collaborative-editor/README.md)
- [实时评论](./live-comments/README.md)
- [视频会议](./video-conferencing/README.md)

## 搜索、抓取与地理服务

- [网络爬虫](./web-crawler/README.md)
- [自动补全](./autocomplete/README.md)
- [全文搜索](./full-text-search/README.md)
- [社交图谱](./social-graph/README.md)
- [附近地点](./nearby-places/README.md)
- [地图与导航](./maps-navigation/README.md)
- [网约车调度](./ride-hailing-dispatch/README.md)
- [外卖配送调度](./food-delivery-dispatch/README.md)

## 媒体处理与分发

- [视频点播](./video-on-demand/README.md)
- [直播](./live-streaming/README.md)
- [图片服务](./image-service/README.md)
- [内容分发网络](./cdn/README.md)
- [大文件传输](./large-file-transfer/README.md)
- [转码流水线](./transcoding-pipeline/README.md)
- [音乐与播客流媒体](./music-podcast-streaming/README.md)

## 交易与争用

- [票务系统](./ticketing/README.md)
- [预约系统](./appointment-booking/README.md)
- [在线拍卖](./online-auction/README.md)
- [支付系统](./payment-system/README.md)
- [复式记账账本与钱包](./double-entry-ledger-wallet/README.md)
- [证券交易与经纪系统](./trading-brokerage/README.md)
- [电商订单与库存](./ecommerce-order-inventory/README.md)
- [银行转账](./bank-transfer/README.md)

## 流处理、分析与可观测性

- [分布式日志与消息队列](./distributed-log-message-queue/README.md)
- [发布订阅系统](./pubsub/README.md)
- [广告点击聚合](./ad-click-aggregation/README.md)
- [指标、监控与告警](./metrics-monitoring-alerting/README.md)
- [集中式日志](./centralized-logging/README.md)
- [流处理](./stream-processing/README.md)
- [批处理数据流水线](./batch-data-pipeline/README.md)

## 调度与控制平面

- [作业调度器](./job-scheduler/README.md)
- [DAG 工作流](./dag-workflow/README.md)
- [CI 运行器](./ci-runner/README.md)
- [部署系统](./deployment-system/README.md)
- [容器编排器](./container-orchestrator/README.md)
- [配置与功能开关](./configuration-feature-flags/README.md)
- [多租户云控制平面](./multi-tenant-cloud-control-plane/README.md)
- [身份与授权服务](./identity-authorization-service/README.md)

## 人工智能与向量系统

- [大语言模型聊天服务](./llm-chat-serving/README.md)
- [RAG 助手](./rag-assistant/README.md)
- [代码助手](./code-assistant/README.md)
- [向量数据库](./vector-database/README.md)
- [嵌入与索引流水线](./embedding-index-pipeline/README.md)
- [推理网关](./inference-gateway/README.md)
- [GPU 调度器](./gpu-scheduler/README.md)
