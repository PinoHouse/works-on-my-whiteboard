# W0 Task 4 报告 — Content Validation and Catalog CLI

## 状态

DONE。实现基于 `95ac3e255e2a4030875d2fa6149bf040bf7ab0d2`，提交信息固定为 `feat: add validation and coverage CLI`，未执行 push。

## 范围

本任务只新增内容校验、`validate` / `coverage` CLI 与薄入口：

- `internal/content/headings.go`、`headings_test.go`
- `internal/content/tags.go`、`tags_test.go`
- `internal/content/links.go`、`links_test.go`
- `internal/content/validate.go`、`validate_test.go`
- `internal/cli/app.go`、`app_test.go`
- `internal/cli/validate.go`、`validate_test.go`
- `internal/cli/coverage.go`、`coverage_test.go`
- `cmd/whiteboard/main.go`

没有修改 catalog/validator、规范或计划，没有新增 harness、evidence 或真实 case/principle 内容。

## 实现摘要

### Markdown 内容契约

- 使用 Goldmark AST（启用 table extension），不以正则解析 Markdown。
- complete case 精确校验 8 个直属 H2 的名称、唯一性和顺序；complete principle 对应校验 6 个直属 H2。
- 每节必须有非空 paragraph/list/table/indented code/fenced code；H3 与纯 HTML 不计为正文。
- 对排除 code、HTML、link destination 后的可见 prose 做 Unicode whitespace collapse，并按 Unicode rune 计数；阈值完全冻结为 brief 指定值。
- 在可见内容中拒绝 `TODO`、`TBD`、`FIXME`、`XXX`、`待补充`、`待完善`；代码、HTML comment、link destination 被忽略。
- 支持且只支持四种精确 claim marker：`ASSUMED`、`DEDUCED`、`MEASURED`、`SOURCED`；拒绝 malformed、未知 claim、缺 marker 与同 claim 冲突 class。
- `ASSUMED` 的同一内容块必须同时包含 `原因：` 与 `变化影响：`。
- `MEASURED` 必须经 owner evidence requirement 到达正确 lab kind、唯一精确 owner/claim binding，并被 required run 引用。
- `SOURCED` 必须解析为 catalog 中存在、且被 owner manifest 列出的 source；alias resolution 同样生效。
- draft 内容不提前施加 complete prose contract；所有已编写 Markdown 的内部链接仍由仓库级链接检查覆盖。

### 仓库链接契约

- 确定性发现仓库内 `.md`，跳过 `.git`、`.superpowers`、`evidence`、`generated`。
- 校验相对目标存在、大小写精确、不能越出 root、symlink 最终仍在 root 内，且目标必须是普通文件或目录。
- 目录 fragment 解析到 `README.md`；非 Markdown 目标不校验 heading fragment。
- Markdown fragment 使用仓库冻结的 Unicode slug 与重复 heading 后缀规则；目标解码一次，不接受反斜杠、NUL 或绝对仓库路径。
- complete manifest 缺少规范 `cases/<id>/README.md` 或 `principles/<id>/README.md` 时给出稳定诊断。

### CLI

- 暴露 `Run(ctx, args, stdout, stderr) int`；使用 `flag.FlagSet` + `ContinueOnError`，parser 输出定向到 stderr。
- `validate` 只加载 catalog 一次，并总是运行 semantic validation；只有 `--content` 或 release 才增加内容/链接校验，release 强制启用内容校验。
- `--release` 仅接受 `current` 或 `sha256:<64 lowercase hex>`；诊断排序后以 text 或 two-space-indented JSON 输出并以单个换行结尾。
- `coverage` 支持 text/json/markdown；stdout/output 的 bytes 完全一致且确定性生成。
- `--output` 使用同目录临时文件、短写检查、close、`0644` 与 rename；不会隐式创建父目录。
- `--check` 只读现有 output 并逐字节比较，绝不写文件。
- 稳定退出码为 success 0、argument/load 2、development validation 3、release/audit 4、lab execution 5。
- `cmd/whiteboard/main.go` 只调用 `os.Exit(cli.Run(...))`，没有业务逻辑。

## TDD RED / GREEN 证据

### Cycle 1：内容校验 API

先写内容测试，生产 API 尚不存在。

```text
$ go test ./internal/content
exit 1
internal/content/headings_test.go:...: undefined: Result
internal/content/headings_test.go:...: undefined: ValidateCase
internal/content/links_test.go:...: undefined: ValidateRepository
FAIL github.com/PinoHouse/works-on-my-whiteboard/internal/content [build failed]
```

加入最小 AST 实现后，同包测试转绿：

```text
$ go test ./internal/content
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/content 1.096s
```

### Cycle 2：CLI API 与行为

先写 in-process `bytes.Buffer` 测试，`Run` 尚不存在。

```text
$ go test ./internal/cli -run 'TestValidateCommand|TestCoverageCommand'
exit 1
internal/cli/app_test.go:45:14: undefined: Run
FAIL github.com/PinoHouse/works-on-my-whiteboard/internal/cli [build failed]
```

实现 CLI composition 后，同一 focused 命令转绿：

```text
$ go test ./internal/cli -run 'TestValidateCommand|TestCoverageCommand'
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/cli 1.008s
```

### 对抗回归 RED / GREEN

实现阶段继续以小循环锁定边界：

- lowercase/uppercase 保留 claim class 候选最初漏报 malformed，同时普通 `RFC` / `ADR` 文本被误判；测试先 RED，再限定为四个保留 class 后 GREEN。
- 稳定 I/O path、非 Markdown fragment、普通未闭合 `[` 后的 marker、coverage writer failure 五项边界测试先 RED；移除平台错误文本、只对 Markdown 校验 fragment、继续扫描嵌套 marker、传播输出错误后 GREEN。
- `[DEDUCEDLY:note]` / `[ASSUMEDNESS]` 前缀最初被错误视作保留类；回归测试先 RED，加入 class 后必须为 colon、Unicode whitespace 或结束的边界后 GREEN。

最终 focused 回归：

```text
$ go test ./internal/content -run 'TestClaimCandidateDetection|TestOrdinary|TestValidateRepository|TestValidateCase' -count=1
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/content 1.244s
```

## 最终验证

### 格式、单包与全仓

```text
$ gofmt -d internal/content internal/cli cmd/whiteboard
exit 0（无输出）

$ go test ./internal/content -count=1
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/content 0.952s

$ go test ./internal/cli -count=1
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/cli 1.692s

$ go test ./internal/content ./internal/cli ./cmd/whiteboard -count=1
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/content 2.326s
ok github.com/PinoHouse/works-on-my-whiteboard/internal/cli 1.999s
?  github.com/PinoHouse/works-on-my-whiteboard/cmd/whiteboard [no test files]

$ go test ./... -count=1
exit 0
catalog 0.982s; cli 1.455s; content 1.200s; validator 1.706s

$ go test -race ./... -count=1
exit 0
catalog 3.493s; cli 3.837s; content 5.010s; validator 4.328s

$ go vet ./...
exit 0（无输出）

$ git diff --check
exit 0（无输出）
```

### CLI smoke

```text
$ go run ./cmd/whiteboard validate --root .
exit 0
validation passed

$ go run ./cmd/whiteboard coverage --root . --format json
exit 0
{
  "baseline_total": 75,
  "complete_total": 0,
  "missing_case_ids": [75 个冻结 canonical case ID]
}
```

### Modules

普通 `go mod tidy` 被当前环境的 Go proxy 连接拒绝阻断，失败仅发生在 `go.yaml.in/yaml/v3` 的上游测试依赖，不是本任务生产依赖解析失败：

```text
$ go mod tidy
exit 1
gopkg.in/check.v1@v0.0.0-20161208181325-20d25e280405: Get "https://proxy.golang.org/...zip": proxyconnect tcp: ... connect: connection refused
```

使用 Go 官方容错选项完成可达部分，并确认模块文件未被改变：

```text
$ go mod tidy -e
exit 0

$ git diff --exit-code -- go.mod go.sum
exit 0（无差异）

$ go mod verify
all modules verified
```

`go.mod` 仍恰好保留两项 direct dependency：Goldmark 1.8.4 与 YAML v3 3.0.4。

## 规格裁决

- Goldmark 默认 heading ID 不适合作为跨版本仓库契约，因此 fragment 使用显式、测试冻结的 Unicode slug 实现。
- HTML block 整块不计 prose/unfinished/claim；inline raw HTML 为零宽忽略，其相邻可见文本仍参与检查。
- `MEASURED` 的 W0 条件止于 required run 对 binding 的静态闭包；不在 Task 4 擅自要求尚不存在的 evidence snapshot。
- link destination 按 Goldmark unescape 后交给 URL parser 解码一次；不进行递归解码。
- 诊断消息不泄漏绝对临时路径或平台错误文本，排序键保持 `(code, path, entity_id, message)`。

## 独立复审

独立只读 reviewer 的最终结论为 Approved：Critical / Important / Minor 均无。Reviewer 独立运行并通过 `go test ./...`、content/CLI race、`go vet ./...` 以及两条 CLI smoke；未修改或提交文件。

## 风险与遗留

唯一非代码限制是当前环境无法连接 Go proxy 下载一个上游测试依赖；现有 module cache 足以让本仓库 full/race/vet/CLI 全部通过，模块文件保持不变。除此之外无已知阻塞。
