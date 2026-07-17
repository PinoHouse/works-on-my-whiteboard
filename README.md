# Works on My Whiteboard

“Works on My Whiteboard” 玩的是 “Works on My Machine” 的梗：系统设计题很容易在白板上看起来什么都能跑，但图上的箭头并不能证明容量、并发、不变量和故障恢复真的成立。这个仓库把面试中的系统设计题还原为可以追问、推导、执行和证伪的工程问题。

## 从第一性原理出发

这里不背品牌架构，也不把某家公司的历史实现当作标准答案。每个案例沿同一条推导链展开：

1. 明确目标、SLO、流量模型、数据边界与不讨论的范围；
2. 写出必须守住的不变量，以及允许牺牲的属性；
3. 从容量、分片、一致性、并发、缓存、异步、背压和失败模式推导设计；
4. 把关键分歧改写成可证伪的 `ASSUMED`、`DEDUCED`、`MEASURED` 或 `SOURCED` 主张；
5. 用固定输入的实验、不可变证据和明确局限检验结论。

问题的客观本质不是“能不能背出一张架构图”，而是能否在约束冲突时识别不变量、做出可解释取舍，并说明结论的证据边界。

## 范围与当前完成度

[`scope.yaml`](scope.yaml) 冻结了 10 个问题族、75 个规范案例；这 **75 个规范案例全部属于项目范围**。品牌化题目只作为排除项映射到规范问题，不能制造新的“同义完成”。读者可以从按问题族整理的[系统设计题索引](cases/README.md)进入任意案例。

- **题目内容覆盖：75/75**。这里的“覆盖”表示每个规范案例都已有 manifest 与完整的第一性原理文章，可用于阅读、练习和讨论。
- **实验与证据闭环：1/75**。这里的“闭环”沿用严格的 manifest 依赖语义：内容、原则、实验、证据要求与来源必须全部闭合；当前只有 `distributed-rate-limiter` 满足。

两项进度不能互相替代。其余 74 个案例的 `status: draft` 表示实验与证据生命周期尚未闭环，不表示正文缺失。生成的 [`generated/coverage.md`](generated/coverage.md) 只呈现第二项机器覆盖率，仍应严格显示 **1/75**，不能手工修饰成内容覆盖率。

## W0 的六个可执行单元格

W0 对三个对照分别执行 baseline 与 variant；单元格由 lab、required run、binding、claim、implementation、adapter 六字段共同确定，role 另行记录。

| Lab / required run | Role | Binding | Claim | Implementation | Adapter | Workload / fault |
| --- | --- | --- | --- | --- | --- | --- |
| `token-bucket` / `burst-and-refill-boundary` | baseline | `token-bucket-burst-boundary` | `token-bucket-bounds-burst-and-average-rate` | `token-bucket-reference-model` | — | `burst-refill-boundary` / none |
| `token-bucket` / `burst-and-refill-boundary` | variant | `token-bucket-burst-boundary` | `token-bucket-bounds-burst-and-average-rate` | `token-bucket` | — | `burst-refill-boundary` / none |
| `distributed-rate-limiter` / `per-node-vs-shared-quota` | baseline | `distributed-rate-limiter-global-quota` | `distributed-rate-limiter-per-node-multiplies-global-quota` | `shared-token-bucket` | — | `two-node-burst` / none |
| `distributed-rate-limiter` / `per-node-vs-shared-quota` | variant | `distributed-rate-limiter-global-quota` | `distributed-rate-limiter-per-node-multiplies-global-quota` | `per-node-token-bucket` | — | `two-node-burst` / none |
| `distributed-rate-limiter` / `coordinator-outage-policy` | baseline | `distributed-rate-limiter-outage-policy` | `distributed-rate-limiter-outage-policy-trades-availability-for-quota` | `shared-fail-closed` | — | `coordinator-outage` / `coordinator-unavailable` |
| `distributed-rate-limiter` / `coordinator-outage-policy` | variant | `distributed-rate-limiter-outage-policy` | `distributed-rate-limiter-outage-policy-trades-availability-for-quota` | `shared-fail-open` | — | `coordinator-outage` / `coordinator-unavailable` |

这六格只支持令牌桶边界、配额放大和协调器故障策略等有限结论；它们不代表生产容量或真实分布式环境认证。身份、选择、A/B 提交与本地限制详见[证据方法论](docs/methodology/evidence.md)。

## 工具链与验证入口

仓库要求 `go.mod` 指定的精确工具链 **go1.26.5**。Make recipes 固定 `LC_ALL=C`、`TZ=UTC` 和只读模块模式，并校验工具链与模块摘要。

常用入口：

```sh
make fmt vet unit fuzz race content coverage
make verify-fast
make verify-deep
make evidence
make audit-evidence
```

- `make verify-fast` 是 push/PR 的开发门禁，包含格式、静态检查、单测、fuzz、race、内容、覆盖率和外部临时目录中的 smoke 证据。
- `make verify-deep` 在独立临时证据根运行完整六格；CI 传入 artifact root 时会保留可审计产物，但不会把 CI 结果提交回仓库。
- `make evidence` 只用于规定的 source commit A 独立 clone 生成不可变证据；`make audit-evidence` 只读审计已提交证据。
- `make clean` 仅删除 `generated/.bin` 与 `generated/.verify`，不会删除证据或覆盖率文档。

## W0 发布警告

正式发布当前被有意阻塞。`make verify` 在 W0 **必须返回非零**：release validation 应当只产生一条 `release_scope_incomplete` 错误，精确报告 `complete=1 baseline=75 missing=74` 和排序后的 74 个缺失 ID。

需要验证“阻塞状态本身准确”时运行：

```sh
scripts/assert-w0-release-contract.sh "$(pwd)"
```

该包装器只在上述 expected-negative 契约完全匹配时返回成功；它不会把 W0 描述成通过正式发布。

## 许可证

本项目采用 [Apache License 2.0](LICENSE)。外部来源仍受各自许可和引用边界约束。
