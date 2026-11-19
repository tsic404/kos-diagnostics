# kos-diagnostics

KOS 系统应用项目：诊断页与诊断包导出。页面只读呈现降级原因、阻塞对象与证据链，唯一动作是诊断包导出。

- 设计：`tsic/kos-design` 仓的 `kos-diagnostics/`（范围、契约、验收、决策记录）
- 交付形态：独立扩展包，装成系统组件；默认 UI-only，不带自有后端（ADR-033）

## 模块

| 模块 | 职责 | 契约 |
|---|---|---|
| `declaration.go` | 页面、展示项与导出动作的声明；贡献随组件就绪激活、停用撤销 | `DIAG-C-1`、`KOSD-C-18`、`KOSD-C-28` |
| `readings.go` | 条目读取的分派与可见性 | `KOSD-C-53`、`KOSD-C-59` |
| `health.go` | 降级原因与建议动作（计算源：健康与条件的聚合，应用另取证据链结论） | `DIAG-C-1`、`KOSD-C-53` |
| `blocked.go` | 卸载卡住与阻塞对象（对象源：应用阶段、阻塞清单与卷的阻塞条件） | `DIAG-C-1`、`KOSD-C-33`、`KOSD-C-38` |
| `evidence.go` | 各段证据与事实/推断区分 | `DIAG-C-1`、`KOSD-C-26` |
| `export.go` | 诊断包导出入口与导出目标 | `DIAG-C-1`、`KOSD-C-12`、`KOSD-C-26` |

模块不持有状态：证据与诊断包读时生成，不落库。

## 页面

| 展示项 | 来源类型 | 内容 |
|---|---|---|
| 降级原因与建议动作 | 计算源 | 聚合规则与判定过的对象 |
| 卸载卡住与阻塞对象 | 对象源 | 卡住的应用与阻塞清单中的对象 |
| 各段证据 | 对象源 | 引用链逐段证据，扩展段按声明段位插入 |
| 诊断包导出 | 计算源 | 可导出的应用；动作 `POST /api/v1/diagnostics/bundles` |

页面只读：非导出项各自声明只读原因，页面唯一动作是导出；导出读时生成，不写对象。导出入口随组件启停出现与消失。

## 边界

- 证据链组装与诊断包组装留在 kosd（`pkg/diagnostics`，`KOSD-C-26`）；本仓只经控制面读取，不复制其判定。
- 不保存日志，诊断包不含日志内容（`KOSD-C-64`）；不导出凭据或敏感参数（`KOSD-C-14`）。
- 不带自有后端、控制器、对象类或新 API（ADR-033）。

## 构建与测试

```sh
make lint        # gofmt 检查 + go vet
make test        # 单元与验收用例
make acceptance  # 只跑 tests/acceptance/，-v
```

验收用例：`tests/acceptance/DIAG-TC-1/`、`DIAG-TC-12/`、`DIAG-TC-13/`、`DIAG-TC-89/`（均 P1）。
用例读的是内存 API server：证据链与诊断包由 kosd 的真实实现组装，世界夹具见 `tests/acceptance/fixture/`。
