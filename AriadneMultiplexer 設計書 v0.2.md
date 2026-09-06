# AriadneMultiplexer 設計書 v0.2

## 1. 概要

AriadneMultiplexer は、複数の対話型ターミナルおよび AI coding agent を効率よく操作・監督するためのターミナルマルチプレクサ / workspace manager である。

一般的な terminal multiplexer として以下を提供する。

- 複数 terminal の生成
- Window / Pane 管理
- Pane 分割
- Pane 移動
- kill / stash
- Workspace 管理
- keyboard-oriented な TUI 操作
- daemon による terminal lifetime 管理

加えて AriadneMultiplexer は、Codex、Claude Code 等の coding agent を多数同時に動作させる利用形態を主要ユースケースとする。

各 TerminalInstance を Plugin が監視し、

- WAITING
- REQUIRE
- ERROR
- その他ユーザー定義 Label

を付与することで、「現在どの terminal を見るべきか」を管理する。

AriadneMultiplexer の中心的な差別化要素は、

> 複数の terminal を管理するだけではなく、人間の attention を必要としている terminal を効率よく発見・巡回できること

である。

---

# 2. oct / streammux との関係

AriadneMultiplexer と oct は完全に独立したソフトウェアである。

## oct

PTY / stream の分配・ルーティングを主目的とする。

例:

- 1:N
- N:1
- M:N
- stdout → file
- 複数 consumer
- stream routing

## AriadneMultiplexer

人間が terminal / coding agent を操作・監督するための workspace manager。

AriadneMultiplexer は oct の API や内部モデルには依存しない。

必要であれば Ariadne 内の shell から通常のコマンドとして oct を実行する。

```text
AriadneMultiplexer
    └─ Terminal
         └─ Shell
              └─ oct
```

将来的に 1 PTY に複数 frontend を attach する等の stream 分配機能が必要になった場合には、Ariadne 自身で再実装せず oct の内部利用を検討する。

## streammux

AriadneMultiplexer は daemon / frontend 間の通信に streammux を利用する。

streammux は、1 本の full-duplex stream 上で、複数の PTY data、request、response、event を frame として多重化する。

これは oct が担当する汎用的な 1:N / N:1 / M:N stream routing とは別の責務である。

```text
Ariadne frontend
       ↕
  streammux.Conn
       ↕
Ariadne daemon
```

Ariadne 独自の framing protocol は定義せず、通信の framing、識別子、request / response / event の分類には streammux の protocol を利用する。

---

# 3. 開発方針

## 3.1 Cross-platform Architecture

Core は OS 非依存とする。

OS 固有機能は Platform Layer に隔離する。

```text
Portable Core
     |
Platform abstraction
     |
+----+----+
|         |
Unix    Windows
```

## 3.2 Initial Development Platform

初期開発は macOS / Linux を中心に行う。

理由:

- メイン開発機が macOS
- Codex 等を利用した開発環境との相性
- Unix PTY を利用して Core / daemon / TUI を先行して開発できる
- GUI 実装を待たずに Ariadne 自身を実運用できる

初期段階では macOS/Linux 向け GUI は実装しない。

## 3.3 Windows

後に Windows Platform Backend を実装する。

予定:

```text
PTY:
ConPTY

GUI:
C# + WinUI 3

IPC:
Named Pipe
```

GUI は OS ごとに別実装でもよい。

GUI toolkit の統一は目的としない。

---

# 4. 全体構成

```text
AriadneMultiplexer

├─ Core
│   ├─ Workspace
│   ├─ Window
│   ├─ Pane
│   ├─ TerminalInstance
│   ├─ Label System
│   ├─ Attention Navigation
│   ├─ Command System
│   ├─ Event System
│   ├─ Plugin System
│   ├─ Configuration
│   └─ Persistence
│
├─ Platform
│   ├─ PTY
│   ├─ Process
│   ├─ IPC
│   ├─ Notification
│   └─ Platform Metadata
│
├─ Platform Implementations
│   ├─ Unix
│   └─ Windows
│
├─ Daemon
│
└─ Frontend
    ├─ TUI
    └─ GUI
```

Core には、

- ConPTY
- Win32
- POSIX PTY
- Unix Domain Socket
- Named Pipe

等の OS 固有概念を持ち込まない。

---

# 5. TerminalInstance

## 5.1 定義

TerminalInstance は、

> daemon が所有する 1 PTY と、その PTY 上で動作する対話環境の lifetime 単位

である。

tmux の session と混同しないため、TerminalSession という名称は使用しない。

概念:

```text
TerminalInstance
├─ PTY
├─ shell / process
├─ cwd
├─ command
├─ environment
├─ terminal size
├─ metadata
├─ labels
└─ raw history buffer
```

---

# 6. Pane と TerminalInstance

Pane と TerminalInstance は別概念とする。

```text
Pane
  |
  └─ TerminalInstance?
```

Pane は UI 上の表示領域。

TerminalInstance は daemon 上で生存する terminal 実体。

関係は疎に保つ。

初期仕様では、

```text
1 PTY : 1 frontend
```

のみ対応する。

同一 PTY の複数 frontend attach は考慮しない。

---

# 7. Workspace / Window / Pane

階層:

```text
Workspace
  ├─ Window
  │   ├─ Pane
  │   └─ Pane
  └─ Window
```

## Workspace

1 Workspace は複数 Window を持つ。

Workspace は一つの作業環境を表す。

例:

```text
Workspace: Ariadne

Window: backend
├─ codex
├─ tests
└─ shell

Window: docs
├─ claude
└─ shell
```

## Window

1 Window は複数 Pane を持つ。

Window は tmux における window に近い画面単位。

## Pane

1 Pane は通常 1 TerminalInstance を表示する。

ただし TerminalInstance を持たない Pane も許可する。

---

# 8. FixedPane

TerminalInstance を持たない Pane を許可する。

例:

```text
Pane
├─ TerminalPane
└─ FixedPane
```

FixedPane の利用例:

- Ariadne status
- stash list
- agent status
- help
- command UI
- system information
- future plugin UI

Pane という概念を PTY に限定しないことで、将来の拡張性を確保する。

---

# 9. Pane 移動

Pane は Window 間で移動可能とする。

```text
Window A
  └─ Pane X

        ↓ move

Window B
  └─ Pane X
```

Pane と TerminalInstance の関係は移動によって変化しない。

---

# 10. Terminal Lifetime

## 10.1 自然終了

通常は shell / process 側が exit することで PTY stream が終了する。

```text
shell exit
    ↓
PTY stream EOF
    ↓
TerminalInstance終了
    ↓
対応Pane削除
```

これを通常の terminal 終了とする。

---

# 11. kill

明示的に terminal を終了する操作。

```text
kill
 ↓
TerminalInstanceを終了
 ↓
PTY / child processを終了
 ↓
Pane削除
```

---

# 12. stash

stash は Pane 単位の detach に相当する Ariadne 独自操作。

```text
stash
 ↓
Paneを表示構造から削除
 ↓
TerminalInstanceはdaemon上で生存
```

stash 後:

```text
daemon
  └─ TerminalInstance
       └─ agent/process
```

TerminalInstance は引き続き Plugin の監視対象となる。

---

# 13. stash list

stash 済み TerminalInstance は Ariadne CUI から一覧表示・復帰できる。

特定 key binding から Ariadne CUI を開く。

例:

```text
Ariadne Command UI

> stash list

ID    NAME              LABELS
12    backend/codex     WAITING
17    tests             ERROR
21    shell             -
```

ここから TerminalInstance を Pane として再表示できる。

---

# 14. Window kill / stash

Window にも、

```text
window kill
window stash
```

を提供する。

## Window kill

配下 Pane に対して kill を適用する。

TerminalPane:

```text
TerminalInstance終了
Pane削除
```

FixedPane:

```text
Pane削除
```

## Window stash

TerminalPane:

```text
TerminalInstanceを残してPane削除
```

FixedPane:

```text
Pane状態を保存して非表示化
```

Window 自身も通常の表示対象から外れる。

---

# 15. Daemon Lifetime

TerminalInstance の lifetime は daemon が所有する。

Frontend は PTY / child process を直接所有しない。

```text
Frontend
    |
    | IPC
    v
Daemon
    |
    v
TerminalInstance
    |
    v
PTY
```

frontend を終了しても TerminalInstance は生存する。

---

# 16. Daemon Restart

daemon が停止または再起動する場合、

> 全 TerminalInstance を停止する。

daemon より先に child process / PTY を残す設計は採用しない。

理由:

- ownership が不明瞭になる
- orphan process が発生する
- attach不能な process が残る危険性
- lifetime の保証が難しい

daemon lifetime と TerminalInstance lifetime の上限を一致させる。

---

# 17. Platform Layer

OS 固有機能を小さな capability 単位で抽象化する。

例:

```text
Platform
├─ PTY
├─ Process
├─ IPC
├─ Notification
├─ Shell
└─ Metadata
```

巨大な Platform interface は作らない。

例:

```go
type PTY interface {
    Read([]byte) (int, error)
    Write([]byte) (int, error)
    Resize(cols, rows int) error
    Close() error
}
```

必要であれば、

```go
type Killable interface {
    Kill() error
}
```

等の capability を別途定義する。

---

# 18. Unix Backend

初期実装。

予定:

```text
PTY
  → POSIX PTY

IPC
  → Unix Domain Socket

Process
  → Unix process API
```

macOS / Linux で共通化できる部分は共通化する。

必要であれば build tag 等で差分を吸収する。

---

# 19. Windows Backend

後から追加。

予定:

```text
PTY
  → ConPTY

IPC
  → Named Pipe

Process
  → Win32 process API
```

Core に Windows 固有型を露出させない。

---

# 20. Frontend

## 20.1 TUI

初期 frontend。

Go で実装する。

主な役割:

- Workspace 表示
- Window 表示
- Pane split
- Pane navigation
- stash
- kill
- attention navigation
- Ariadne CUI
- terminal rendering

## 20.2 GUI

Windows 向けに後から実装。

予定:

```text
C# + WinUI 3
```

GUI は daemon protocol の client として実装する。

Core ロジックを GUI 内に再実装しない。

---

# 21. Command System

全操作を Command として統一する。

例:

```text
CreateWorkspace
CreateWindow
CreatePane

MovePane
SplitPane

KillPane
StashPane

KillWindow
StashWindow

RestoreTerminal

FocusPane
NextPane
PreviousPane

Attention
AttentionByLabel

AddLabel
RemoveLabel
```

TUI keybinding、GUI button、CLI、Plugin から同じ Command System を利用する。

---

# 22. Event System

Core の状態変化は Event として扱う。

例:

```text
WorkspaceCreated
WindowCreated
PaneCreated
PaneMoved
PaneClosed

TerminalCreated
TerminalExited

OutputReceived
InputSent

ProcessStarted
ProcessExited

LabelAdded
LabelRemoved

FocusChanged
```

Plugin は Event を購読できる。

---

# 23. Label System

従来の固定 Attention State は採用しない。

TerminalInstance に任意 Label を複数付与できる仕組みとする。

例:

```text
TerminalInstance #12

labels:
  WAITING
  REQUIRE
  CODEX
```

Label 名は固定 enum にしない。

ユーザー定義可能とする。

---

# 24. LabelDefinition

Label 自体の表示や挙動を設定できる。

例:

```toml
[label.WAITING]
priority = 80
border = "yellow"

[label.REQUIRE]
priority = 100
border = "red"

[label.ERROR]
priority = 60
border = "magenta"
```

設定候補:

```text
name
priority
border
foreground
background
notification
```

初期実装ですべて対応する必要はない。

---

# 25. LabelAssignment

実際に TerminalInstance に付与された Label は Assignment として管理する。

```text
LabelAssignment
├─ label
├─ target
├─ source
├─ created_at
└─ metadata
```

例:

```text
label  = WAITING
target = TerminalInstance #12
source = plugin.codex
```

---

# 26. Label Source

複数 Plugin が同じ Label を付与可能とする。

例:

```text
TerminalInstance #12

WAITING
├─ plugin.codex
└─ plugin.generic-prompt
```

表示上は WAITING 一つとして扱える。

plugin.codex が自分の WAITING を解除しても、

```text
plugin.generic-prompt
```

による WAITING が残っている限り、TerminalInstance には WAITING が存在する。

Plugin は原則として自分が追加した Assignment のみ解除する。

---

# 27. Attention Navigation

Attention は状態ではなく、

> Label を条件に対象 TerminalInstance を検索・移動する機能

として実装する。

例:

```text
attention
attention WAITING
attention ERROR
attention WAITING --next
attention WAITING --prev
```

Label priority を利用して対象順序を決める。

---

# 28. Key Binding

Command を key binding に割り当てる。

例:

```toml
[keybindings]
"ctrl+a w" = "attention WAITING"
"ctrl+a r" = "attention REQUIRE"
"ctrl+a e" = "attention ERROR"
"ctrl+a a" = "attention"
```

ユーザーが自由に変更可能とする。

---

# 29. Agent Detection Model

Agent Detection を以下の4段階に分離する。

```text
TerminalInstance
      ↓
Identification
      ↓
Observation
      ↓
Classification
      ↓
User Policy / Navigation
```

---

# 30. Identification

TerminalInstance 上で何が動いているかを識別する。

例:

```text
codex
claude
gemini
shell
vim
ssh
test
server
unknown
```

判定材料:

```text
foreground process
process tree
起動command
terminal title
environment
output pattern
plugin metadata
```

Core は agent type を固定 enum として保持しない。

Plugin が metadata / Label を付与する。

---

# 31. Observation

Plugin は TerminalInstance の event を観測する。

基本は event-driven。

主なイベント:

```text
OutputReceived
InputSent
ProcessStarted
ProcessExited
TitleChanged
CwdChanged
```

---

# 32. Polling

event-driven を基本とするが、必要な Plugin では polling を許可する。

Plugin capability として定義する。

例:

```text
PollingDetector
```

poll interval 等は Plugin / user config 側で設定可能とする。

---

# 33. Classification

Plugin は観測結果から Label を追加・解除する。

例:

```text
Codex Plugin

approval prompt
    ↓
REQUIRE

user input wait
    ↓
WAITING

abnormal exit
    ↓
ERROR
```

Core は WAITING / REQUIRE / ERROR の意味を知らない。

---

# 34. User Policy

Label の意味付けはユーザー設定側で行う。

Plugin:

```text
WAITINGを付ける
```

User configuration:

```text
WAITINGは黄色
priority=80
ctrl+a wで移動
```

という責務分離を行う。

---

# 35. Plugin System

Plugin は主に以下を担当する。

```text
Identification
Observation
Classification
Metadata
Label management
```

初期 capability 候補:

```text
AgentDetector
TerminalObserver
PollingDetector
MetadataProvider
StatusProvider
```

---

# 36. Plugin からの Terminal Input

Plugin から TerminalInstance へ入力を送る機能は初期スコープ外とする。

将来的には自己責任の capability として追加可能。

Ariadne 初期設計では、

```text
observe
 ↓
classify
 ↓
label
```

を中心とする。

複雑な stream 操作は oct で代替可能。

---

# 37. Daemon / Frontend 通信

Ariadne daemon と frontend の間は、1 本の full-duplex IPC stream で通信する。

Transport は OS ごとに異なってよい。

```text
Unix
  → Unix Domain Socket

Windows
  → Named Pipe
```

各 IPC connection を streammux.Conn として扱い、Ariadne の操作、応答、event、および複数 TerminalInstance の入出力を同じ connection 上に多重化する。

```text
Frontend
   ↕
Unix Domain Socket / Named Pipe
   ↕
streammux.Conn
   ↕ frames
Daemon dispatcher
   ├─ Ariadne commands / responses / events
   ├─ TerminalInstance #1 input / output
   ├─ TerminalInstance #2 input / output
   └─ TerminalInstance #N input / output
```

Control Plane と Terminal Data Plane のために別々の connection や framing protocol は設けない。

---

# 38. streammux Frame Routing

streammux frame の各 field を以下の目的で利用する。

```text
MessageType
  → command / response / event / PTY input / PTY output の識別

Flags
  → request / response / event の分類

CorrelationID
  → request と response の対応付け

StreamID
  → TerminalInstance の wire 上の識別
```

TerminalInstance に属する frame では、非 0 の StreamID を使用する。

daemon 全体、Workspace、Window、Pane、Label 等、特定の TerminalInstance に属さない通信では StreamID 0 を使用する。対象となる domain identifier は payload に保持する。

例:

```text
StreamID = 0
  CreateWorkspace request
  WorkspaceCreated event
  LabelAdded event

StreamID = 12
  TerminalInput event
  TerminalOutput event
  ResizeTerminal request / response
  TerminalExited event
```

MessageType の具体的な値と、Ariadne 固有 control message の payload encoding は別途定義する。

daemon / frontend は connection ごとに一つの read loop を持つ。read loop は受信 frame を MessageType、Flags、StreamID に基づいて対応する handler へ dispatch する。

streammux の event と Core Event System は別概念とする。

```text
streammux FlagEvent
  → daemon / frontend 間の wire message 分類

Core Event
  → daemon 内部の状態変化および Plugin 購読イベント
```

streammux 固有型を Core domain model に露出させない。protocol boundary で wire event と Core Event を相互変換する。

---

# 39. Terminal Data

PTY input / output は streammux frame の payload として運ぶ。

payload は raw PTY bytes とし、JSON / protobuf 等への再 encoding は行わない。

ここで raw とは payload の内容を変更しないことを意味する。wire 上では streammux header が付与される。

```text
PTY
 ↓ raw bytes
Daemon
 ↓ streammux Output frame
Frontend dispatcher
 ↓ raw payload
Terminal renderer / outer terminal emulator
```

daemon は VT sequence を解釈しない。

---

# 40. PTY Relay

daemon は PTY output を読み、

```text
PTY
 ↓ raw bytes
daemon
 ├─ streammux Output frame → frontend
 └─ plugin observer
```

へ渡す。

daemon は PTY bytes の内容を変更しない。

概念:

```go
n, err := pty.Read(buf)

observer.Publish(buf[:n])
connection.Send(outputFrame(terminalID, buf[:n]))
```

すべての TerminalInstance の data と control message は同じ connection、write queue、backpressure を共有する。

実際の実装では blocking、buffer lifetime、connection 全体の head-of-line blocking、backpressure 等を考慮する。

streammux connection と TerminalInstance の lifetime は同一視しない。connection 切断時および daemon 終了時の PTY 解放規則は、本章では変更せず「Terminal Lifetime」「Daemon Lifetime」の定義に従う。streammux 実装はその lifetime を満たす必要がある。

---

# 41. stash 中の Terminal Output

stash 中には frontend が存在しない。

その間も daemon は PTY output を読み続ける。

```text
PTY
 ↓
daemon
 ├─ raw history buffer
 └─ plugin observers
```

PTY を読まないことで child process が block することを防ぐ。

---

# 42. Raw History Buffer

stash 中の output を raw byte buffer として保持する。

上限はユーザー設定可能。

例:

```toml
[terminal]
stash_history_bytes = 16777216
```

16 MiB。

上限を超えた場合は古いデータから削除する。

実装は ring buffer または chunk queue を利用する。

---

# 43. History の性質

Raw History Buffer は、

> 完全な terminal screen snapshot

ではない。

VT sequence の途中から履歴が開始される可能性があるため、完全な画面復元は保証しない。

初期段階では、

```text
stash中に流れたoutputを保持する
```

ことを目的とする。

---

# 44. VT Handling

Ariadne は VT terminal emulator を自作しない。

既存ライブラリを adapter 経由で利用する。

Core は VT library に依存しない。

```text
Ariadne TUI
   ↓
VirtualTerminal abstraction
   ↓
Existing VT implementation
```

---

# 45. Fullscreen Attach

TerminalInstance を画面全体で attach する場合、VT parsing を省略可能。

```text
PTY
 ↓
daemon
 ↓ streammux Output frame
Ariadne frontend dispatcher
 ↓ raw payload
outer terminal emulator
```

外側の Terminal.app / iTerm2 / kitty 等に VT interpretation を任せる。

---

# 46. Split Pane

複数 TerminalInstance を一画面に表示する場合は VT interpretation が必要。

```text
PTY A
 ↓
VT emulator
 ↓
Cell buffer
 ↓
Pane A

PTY B
 ↓
VT emulator
 ↓
Cell buffer
 ↓
Pane B
```

VT library は薄い Ariadne adapter 越しに利用する。

---

# 47. VT Abstraction

例:

```go
type VirtualTerminal interface {
    Write(data []byte) error
    Resize(cols, rows int) error

    Width() int
    Height() int

    Cell(x, y int) Cell
    Cursor() Cursor
}
```

具体的な library API を Core / Pane model に漏らさない。

---

# 48. Configuration

設定形式は TOML を第一候補とする。

OS 間で共有可能な設定を優先する。

例:

```toml
[general]
shell = "zsh"

[terminal]
stash_history_bytes = 16777216

[label.WAITING]
priority = 80
border = "yellow"

[label.REQUIRE]
priority = 100
border = "red"

[keybindings]
"ctrl+a w" = "attention WAITING"
"ctrl+a a" = "attention"
```

OS 固有設定:

```toml
[platform.macos]

[platform.linux]

[platform.windows]
```

---

# 49. Persistence

保存対象:

```text
Workspace
Window
Pane layout
TerminalInstance metadata
cwd
起動command
title
Plugin metadata
Label configuration
```

ただし daemon restart 時には TerminalInstance 自体は終了する。

Persistence は、

> UI / workspace configuration を復元する機能

と、

> process を生存させる機能

を分離して考える。

---

# 50. Initial Project Structure

Go 側の初期構造案:

```text
AriadneMultiplexer/
├─ cmd/
│  ├─ ariadne/
│  │   └─ main.go
│  └─ ariadned/
│      └─ main.go
│
├─ internal/
│  ├─ core/
│  │  ├─ workspace/
│  │  ├─ window/
│  │  ├─ pane/
│  │  ├─ terminal/
│  │  ├─ command/
│  │  ├─ event/
│  │  └─ label/
│  │
│  ├─ daemon/
│  │
│  ├─ frontend/
│  │  └─ tui/
│  │
│  ├─ platform/
│  │  ├─ pty/
│  │  ├─ process/
│  │  └─ ipc/
│  │
│  ├─ platform_unix/
│  │
│  ├─ plugin/
│  │
│  ├─ protocol/
│  │
│  ├─ config/
│  │
│  ├─ persistence/
│  │
│  └─ vt/
│
├─ plugins/
│  ├─ codex/
│  └─ generic/
│
├─ docs/
│  ├─ design.md
│  ├─ domain-model.md
│  ├─ daemon-protocol.md
│  ├─ attention-label-model.md
│  └─ plugin-api.md
│
├─ go.mod
└─ README.md
```

package 分割は実装時に過剰にならないよう調整する。

---

# 51. Milestone 1 — PTY + Daemon

まず最低限を動かす。

実装:

```text
Unix PTY
Daemon
TerminalInstance
streammux frame relay
CLI
```

操作:

```text
ariadne new
ariadne list
ariadne attach
ariadne kill
```

成功条件:

- shell が起動する
- attach できる
- shell を操作できる
- frontend を終了しても shell が残る
- 再 attach できる
- daemon 終了時には shell も終了する

---

# 52. Milestone 2 — stash

実装:

```text
stash
stash list
restore
raw history buffer
```

成功条件:

- TerminalInstance を Pane から外せる
- stash 中も process が動く
- stash 中も output を daemon が読み続ける
- stash list から復帰できる

---

# 53. Milestone 3 — Workspace / Window / Pane

実装:

```text
Workspace
Window
Pane
Pane move
Window kill
Window stash
FixedPane
```

成功条件:

- 複数 terminal を管理可能
- Pane を Window 間で移動可能
- Pane と TerminalInstance が疎結合
- FixedPane を生成可能

---

# 54. Milestone 4 — Split TUI + VT

実装:

```text
Pane layout
VT adapter
existing VT library
cell rendering
resize
```

成功条件:

- 2個以上の interactive terminal を同時表示
- vim / shell / agent 等が Pane 内で正常表示
- Pane resize に PTY resize が追従

---

# 55. Milestone 5 — Label System

実装:

```text
LabelDefinition
LabelAssignment
Label source
priority
style
query
```

成功条件:

```text
add-label
remove-label
query-by-label
```

等を内部 Command / CLI から確認できる。

---

# 56. Milestone 6 — Attention Navigation

実装:

```text
attention
attention <label>
attention --next
attention --prev
```

priority と key binding を設定可能にする。

stash 中 TerminalInstance も検索対象とする。

---

# 57. Milestone 7 — Plugin

最初の plugin として Codex Detector を実装する。

初期機能:

```text
Identification
OutputReceived observation
WAITING
REQUIRE
ERROR
Label removal
```

Codex 固有処理を Core に入れない。

---

# 58. Milestone 8 — Windows Backend

Unix backend と同じ Core を利用して、

```text
ConPTY
Named Pipe
Win32 process management
```

を追加する。

OS abstraction の実用性をここで検証する。

---

# 59. Milestone 9 — Windows GUI

```text
C#
WinUI 3
```

で GUI frontend を実装する。

GUI は daemon protocol の client とする。

---

# 60. 非目標

初期段階では以下を行わない。

- tmux 完全互換
- 独自 VT100 emulator 実装
- 同一 PTY stream を複数 frontend へ分配する M:N routing
- 複数 frontend の同一 PTY attach
- daemon restart を跨ぐ process lifetime
- oct との直接統合
- plugin による terminal 自動操作
- macOS/Linux GUI
- plugin marketplace
- remote multiplexer
- AI による汎用 terminal 内容解析

---

# 61. 設計原則

## Principle 1

Core は OS を知らない。

## Principle 2

PTY lifetime は daemon が所有する。

## Principle 3

Pane と TerminalInstance を同一視しない。

## Principle 4

daemon / frontend 間の framing と複数 stream の多重化には streammux を利用する。

同一 PTY stream の複数 consumer への分配等、汎用的な stream routing は Ariadne の責務にしない。必要なら oct を利用する。

## Principle 5

Attention は固定状態ではなく Label で表現する。

## Principle 6

Agent 固有知識は Plugin に隔離する。

## Principle 7

Plugin は event-driven を基本とする。

## Principle 8

Terminal data は streammux frame の raw payload として扱い、内容を再 encoding しない。

## Principle 9

VT emulator は自作しない。

## Principle 10

Ariadne 独自の価値は terminal multiplexing そのものではなく、

> Label / Attention / Agent supervision

に置く。

---

# 62. Codex 実装時の指示

実装時は以下を厳守すること。

1. Windows 対応を先に実装しない。
2. macOS/Linux の Unix Backend から実装する。
3. Core package から OS 固有 API を直接呼ばない。
4. Core に ConPTY / POSIX PTY 等の型を露出させない。
5. `Pane` と `TerminalInstance` を同一 struct にしない。
6. `WAITING` / `ERROR` 等を enum として Core にハードコードしない。
7. Label 名は string または同等の拡張可能な identifier とする。
8. Plugin ごとの LabelAssignment source を保持する。
9. PTY input / output を JSON 等へ変換せず、streammux frame の raw payload として扱う。
10. daemon / frontend 間の全通信は一つの streammux connection 上に多重化する。
11. Plugin は raw output event を観測可能とする。
12. stash 中も PTY を読み続ける。
13. stash history は byte 上限付きとする。
14. VT implementation を Core に直接依存させない。
15. 最初から巨大な Plugin API を設計せず、最初の Codex Plugin 実装から必要 capability を抽出する。
16. GUI は実装しない。
17. 各 milestone ごとに動作確認可能な executable を維持する。
18. 過度な abstraction を避ける。ただし OS boundary、Pane/TerminalInstance boundary、Plugin boundary は厳守する。

---

# 63. 最初の実装目標

最初の目標は非常に小さくする。

```text
ariadned
  ↓
Unix PTY
  ↓
zsh/bash
```

に対して、

```text
ariadne new
ariadne attach
```

で shell を操作できること。

その後 frontend を終了し、

```text
ariadne attach
```

で同じ shell に戻れること。

最後に daemon を終了したとき、PTY / child process も確実に終了すること。

この一連が AriadneMultiplexer の最初の vertical slice となる。
