# Ariadne External Plugin API v1

外部PluginはdaemonがPluginごとに1 processを管理する。`process`はpackageの実行fileを起動し、`native`は同じ`ariadne` executableのhelper roleへDLL／`.so`／`.dylib`を読み込む。本体へnative libraryを直接ロードしない。GUIはこの初版に含まれない。

実行対象は**信頼済みコード**である。capabilityはAriadne APIへのアクセスを制限し、process境界は障害を隔離する。OSのfile／networkアクセスをsandboxする仕組みではない。

## Packageと管理

packageはローカルdirectoryで、rootに`manifest.json`を置く。download、marketplace、自動更新はない。install時には対象OS／architectureのentrypointを選び、regular fileであることを検査する。processはUnixで実行権限も必要。nativeはlibrary形式とarchitectureも検査する。package全体でsymlink／特殊fileは禁止し、10,000 files／256MiBを上限とする。

```json
{
  "id": "my-plugin",
  "version": "1.0.0",
  "api_version": 1,
  "runtime": "process",
  "entrypoints": {
    "darwin/arm64": {"path": "plugin"},
    "linux/amd64": {"path": "plugin", "args": ["--stdio"]},
    "windows/amd64": {"path": "plugin.exe"}
  },
  "capabilities": ["core.read", "frontend.interact", "frontend.editor"],
  "commands": [{"name": "inspect", "description": "Inspect a pane"}],
  "tools": [{"name": "inspector"}],
  "widgets": [{"name": "count"}]
}
```

`id`と宣言名は小文字で始め、英小文字・数字と区切り`.`／`_`／`-`を使用する。ID・宣言名は96 bytes以下。`ariadne`、`agent-marker`および静的Plugin IDは予約済み。entrypointはpackage内の相対pathで、`..`による脱出、backslash、絶対pathは不可。nativeに`args`は指定できない。versionは128 bytes以下の非空文字列。未知field／重複capability／重複宣言／非互換APIは拒否する。

```sh
ariadne plugin list --json
ariadne plugin status my-plugin
ariadne plugin install /absolute/package
ariadne plugin grant my-plugin core.read workspace:1,2
ariadne plugin grant my-plugin frontend.interact all
ariadne plugin grant my-plugin frontend.editor all
ariadne plugin enable my-plugin
ariadne plugin run my-plugin inspect ARG
ariadne plugin --json run my-plugin inspect ARG
ariadne plugin restart my-plugin
ariadne plugin revoke my-plugin core.read workspace:1,2
ariadne plugin disable my-plugin
ariadne plugin update my-plugin /absolute/new-package
ariadne plugin uninstall my-plugin
ariadne plugin uninstall my-plugin --purge
```

`grant`／`revoke`はcapabilityとscopeが一致するgrantを追加／解除する。`all`が省略時のscopeである。scope IDの順序も登録時と同じにする。要求されていないcapabilityは許可できない。新規installはdisabled・未許可、updateもdisabledへ戻る。updateは既存grant・固有data・Tool stateを保持し、追加capabilityは自動許可しない。

管理storageはstate fileと同じdirectoryの`plugins/`配下に分離する。

- `registry.json`: version 1、package参照、enable、grants。temporary file、file sync、atomic replace、directory syncで保存する。保存errorは操作errorとする。
- `packages/`: install時にコピーした実行package。
- `data/ID/`: Plugin固有data。initializeで絶対pathを通知する。Plugin自身が固有fileを管理する。各起動の`stderr.log`は1MiBまで記録する。
- `editors/`: 対話用一時file。終了／cancel時に削除する。

registry破損時はfileを上書きせず、全外部Pluginの自動起動とregistry変更を停止する。`list --json`の`registry_error`で診断できる。復旧はdaemonを停止して保全したregistryを修復する。

uninstallはpackageとregistry登録を解除し、Pane・共有Tool state・固有dataを保持する。再installには新しいgrantが必要。残ったPaneはUnavailable表示となる。`--purge`は関連Pane・Tool state・固有dataも削除し、uninstall済みIDに対しても使用できる。

TUIの`:plugin`は組み込みPlugin manager ToolPaneを開く。`i` install、`u` update、`d` uninstall、`e` enable/disable、`r` restart、`g` grant、`v` revoke、Enterで詳細を表示する。`:plugin ...`はCLIと同じ構文・daemon APIを使用する。Plugin commandはprompt、keybinding、palette、help、completionにも接続する。

## Transport

Pluginとhelper／daemonのstdio通信はUTF-8 JSON-RPC 2.0、1 messageごとのContent-Length framingである。lengthは**JSONのbyte数**で、改行区切りではない。

```text
Content-Length: <byte count>\r\n
\r\n
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{...}}
```

headerは8KiB以下、CRLF必須。`Content-Length`は1つだけ必須で、任意の`Content-Type`も受理する。未知header、未知JSON envelope field、不正JSON、batchは受理しない。request IDは非zeroの`uint64`（文字列IDは不可）。IDは各送信側の方向に独立している。notificationはIDなし。

```json
{"jsonrpc":"2.0","id":1,"result":{"api_version":1}}
```

```json
{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"operation failed"}}
```

resultはnullを含め必ず存在し、errorとは排他的。未知requestへのerrorは`-32601`、handler errorは`-32000`。通常のoperation errorはresponseとして返す。不正protocol、timeout、queue overflowでは対象Pluginを停止する。cancel済みrequestへの遅いresponseは棄却する。未知ID／重複responseはprotocol errorとなる。

stdoutはprotocol専用、stderrはlog。native helperはnative stdoutをstderrへ分離し、protocol送信はABIの`send`だけを使用する。daemon/frontend間は既存streammuxを使用し、daemonへstdio protocolを直接接続しない。既存frontend transportのframe上限も適用されるため、8MiBまでのPlugin messageすべてをfrontendに転送できるとは限らない。

依存するoperationは前のresponseを待ってから送る。readerとresponse処理はcommandやnative callbackから独立させる。対話待ちのhost APIは他frontendのhost API処理を止めない。

## HostからPluginへのmethod

public Go DTOは[`api/plugin/v1`](../api/plugin/v1/types.go)に置く。内部Go型、allocator、Core commandを外部境界に共有しない。

| Method | Params | Result | 既定期限 |
| --- | --- | --- | --- |
| `initialize` | `Initialize` | `{"api_version":1}` | 5s |
| `command` | `Command` | `{"text":"...","json":...}`、両field省略可 | 30s |
| `view.render` | `View` | `Frame` | 1s |
| `view.input` | `Input` | null | 2s |
| `widget` | `Widget` | `{"text":"..."}` | 1s |
| `event` | `Event` | null | 2s |
| `terminal.event` | `TerminalEvent` | null | 2s |
| `shutdown` | null | null | 2s |
| `view.close` notification | `{"view_id":"...","generation":N}` | なし | — |
| `interaction.result` notification | `{"id":"...","result":InteractionResult?,"error":"..."?}` | なし | — |
| `cancel` notification | `{"id":N}` | なし | — |

initializeは`api_version`、Plugin `id`、実行`generation`、manifestが現在要求する承認済み`grants`、`data_directory`を渡す。実装がAPI v1に対応しない場合はerrorを返す。実際の利用可能capabilityはmanifest要求と通知されたgrantの両方で決まる。権限変更時は旧実行generationを無効化し、再起動・initializeする。

commandは宣言の`name`、文字列配列`args`、host発行`context`を渡す。同時実行はfrontendごとに1件。text/JSONの結果とerrorを返せる。TUIはcommandの完了を非同期で受け取り、event loopを継続する。command期限はそのcommand自身のhost対話待ちを除外する。cancel後の再実行は行わない。

`Context`はPlugin ID、opaque `token`、frontend／Workspace／Window／Pane／Terminal IDを持つ。host APIへ渡すのはtokenだけ。contextはcommand／render／input／widgetの呼び出し時の対象へ固定され、完了・cancel・detach・権限変更で無効になる。Pluginが任意IDでcontextを作ることはできない。監視のみの常駐APIにはtokenを省略できるが、context scopeは適用されない。

## PluginからHostへのAPI

`log`と`cancel`以外は共通envelopeをparamsに用いる。

```json
{
  "jsonrpc": "2.0", "id": 2, "method": "label.set",
  "params": {
    "context": "<host-issued token>",
    "params": {"kind":"pane", "id":12, "name":"state", "value":"working"}
  }
}
```

operationのparamsはstrictに検査し、未知fieldを拒否する。source偽装や汎用の内部Core command実行口は提供しない。下表の`?`は省略可。通常成功時のresultは、記載がなければnull。

| Method | Capability | Operation params／result |
| --- | --- | --- |
| `core.snapshot` | `core.read` | `{}` → 許可された`Snapshot` |
| `core.subscribe` | `core.events` | `{}`と有効context → 初期`Snapshot`、以後context付き`event` |
| `pty.subscribe` | `pty.observe` | `{}`と有効context → 以後context付き`terminal.event` |
| `label.set`／`label.remove` | `label.write` | `kind` pane/window/workspace、`id`、`name`、`value?`。sourceは`plugin:ID`に固定 |
| `attention.raise` | `attention.write` | `pane_id`、`key`、`class` waiting/completed/warning/error、`severity` info/warning/error/critical、`message`。sourceは自身に固定 |
| `tool.state.update` | `tool.state.write` | `descriptor`、`expected_generation`、`state_version`、JSON `state` → `{"tool":ToolInstance}` |
| `layout.workspace.create` | `layout.write` all | `name` → `workspace_id` |
| `layout.workspace.rename`／`delete` | `layout.write` | `workspace_id`、`name?` |
| `layout.window.create` | `layout.write` | `workspace_id`、`name?` → `window_id` |
| `layout.window.rename`／`delete`／`stash` | `layout.write` | `window_id`、`name?` |
| `layout.window.restore` | `layout.write` | `window_id`、`workspace_id` |
| `layout.pane.restore`／`layout.move-pane` | `layout.write` | `pane_id`、`window_id`、`target_pane_id?`、`direction?` horizontal/vertical |
| `layout.resize` | `layout.write` | `window_id`、`split_id`、正の整数配列`weights` |
| `layout.create-tool`／`layout.split-tool` | `layout.write` | `window_id?`／`target_pane_id?`、`direction?`、宣言済み`type`、`instance`、`title?` → `pane_id` |
| `layout.stash-pane`／`layout.close-tool` | `layout.write` | `pane_id`。closeはToolのみ |
| `terminal.new` | `terminal.lifecycle` | `window_id`必須、`target_pane_id?`、`direction?`、`title?`、`presentation?`、`argv`、`cwd`、`env`、`initial_size:{cols,rows}` → `{"pane":Pane}` |
| `terminal.restart` | `terminal.lifecycle` | `pane_id`、`env`、`initial_size` → `{"pane":Pane}` |
| `terminal.run` | `terminal.lifecycle` | `pane_id`、`argv`、`cwd?`、`fallback_cwd`、`env`、`initial_size` → `{"pane":Pane}` |
| `terminal.stop`／`terminal.delete` | `terminal.lifecycle` | `pane_id`。stopはPaneを保持、deleteは停止済みPaneを削除 |
| `pty.input` | `pty.input` | `pane_id`、`terminal_id`、base64 `data`。context必須、取得時のTerminalと現在のTerminalの両方に一致すること |
| `clipboard.read`／`clipboard.write` | `clipboard.read`／`clipboard.write` | `{}` → `{"text":"..."}`／`text`。Ariadne clipboard deny policyは優先 |
| `frontend.interact` | `frontend.interact`（prompt/confirm）、`frontend.editor`（editor） | `kind` prompt/confirm/editor、`message?`、`text?`。commandからは`InteractionResult`、`view.input`からは`{"id":"..."}`を返す。context必須 |

`log`は共通envelopeを使わず`{"level":"info","message":"..."}`を渡す（level 32 bytes、message 4KiB以下）。`cancel` notificationは`{"id":N}`。

command callbackからの対話は同期で、結果をAPI responseとして返す。待機時間はcommand期限に算入しない。`view.input` callbackからの対話はboundedな非同期sessionとして開始し、APIは直ちにinteraction IDを返す。開始responseを送信してから対話を開始し、完了時は`interaction.result` notificationで同じIDと`result`または`error`を通知する。`view.render`と`widget` callbackから対話は開始できない。frontend detach、view close、Plugin停止／再起動／権限変更、または`interaction_ms`超過でsessionを取り消す。detachや旧runtime破棄後には完了notificationを送らない。

### Capability scope

resourceを扱うcapabilityに`all`、`workspace`、`pane`、`context`を用いる。

- `all`: 存在する対象resource全部。stashed resourceも含む。一時editor Paneは外部APIから除外する。
- `workspace`: `ids`で指定したWorkspaceと**現在所属する**Window／Pane。stashの移動元IDは所属として扱わない。
- `pane`: `ids`で指定したPaneだけ。stashed Paneにも使用できる。親Window／Workspaceの操作権限は含まれない。
- `context`: host tokenに固定されたWorkspace／Window／Paneだけ。focus変更で別resourceへ追随しない。stashed Paneの移動元はcontextの親にしない。

複数対象の操作は全対象を検査する。moveはPane・移動元Window・移動先Window・指定target、Window stash/restoreは配下の全Pane、resizeはWindow内の全Pane、共有Tool stateは同じdescriptorを参照する全Paneを検査する。Core変更では検査と変更を同じexecutor turnで行い、検査後の移動／共有view追加も再検査する。

clipboard、frontend.interact、frontend.editorはglobal capabilityでscopeは`all`のみ。PTY入力はPane権限に加えて、contextのPane／Terminalに限定する。再起動によるTerminal差し替え後の古い入力は拒否する。Label／attentionのsource、Tool stateのproviderは自身のIDに限定する。

### Snapshotとevent

Snapshotは`revision`と配列`workspaces/windows/panes/labels/attentions/tool_instances`を持つ許可範囲への投影で、内部Core Snapshotの保存形式ではない。[固定v1 resource型](../api/plugin/v1/resources.go)で配列要素をdecodeできる。空配列は`[]`。

- Workspace: `id/name/window_ids`。未許可Window IDは取り除く。
- Window: `id/workspace_id/name/layout?/stashed?`。layoutも未許可Pane leafを取り除き、残りが1 childならcollapseする。stashed Windowのworkspace_idは0。
- Pane: `id/window_id/kind/title?/presentation/terminal?/tool?/stashed?`。stashed Paneのwindow_idは0。Terminalは`id?/state/launch:{argv,cwd}/exit?/history_available`。envを返さない。
- Layout: `kind` pane/split、`pane_id?`、`split_id?`、`direction?`、`children?`、`weights?`。
- Tool descriptor: `provider/type/instance`。Tool instance: `descriptor/state_version/generation/state`。返すstateは自身のproviderと全共有Paneが許可されるdescriptorのみ。
- Label: `target_kind/target_id/source/name/value`。
- Attention: `id/pane_id/source/key/class/severity/message?/occurred_at/updated_at/acknowledged_at?`。時刻はRFC3339。
- Exit: `kind` process/signal/pty_error/killed、`code?/signal?/message?`。

`event`は`kind`と**最新の許可Snapshot**、任意のcontext tokenを渡す。外部eventは内部のraw event payloadを含めない。`revision`は非連続でもよい。未許可resourceだけの更新は通知しない。削除／移動では変更前と後の両方を検査し、権限内から離れたresourceが消えることも通知する。

non-contextのcore.events／pty.observe grantには常駐通知を自動で開始する。context scopeでは有効な呼び出し中にsubscribeする。購読はその呼び出しの完了／cancel／detachまでであり、終了したtokenを保持して監視に再利用できない。

`terminal.event`は`kind`（output/exited）、`pane_id/terminal_id/sequence/data?/exit?`、任意のcontextを渡す。dataはbase64でbyte列を表し、UTF-8とは限らない。PTY出力の取得はdaemonのdrainを待たせず、最新の所属scopeを再検査する。

## ToolPaneとwidget

frontend管理APIは描画前に`view.open`（IDとView）でviewを登録する。`render`／`input`は既存viewだけを使用し、`view.close`で直ちに解放する。close後の遅延render／inputはviewを再作成しない。viewの寿命はfrontendに属し、Plugin runtimeのrestartでは保持する。同時に開けるviewはdaemon全体で4096件まで。openとcloseの順序はfrontendが保証し、closeしたIDは新しいviewに再利用しない。

`view.input`中に開始した対話sessionはcallback終了後も有効で、command対話と合わせた同時数をdaemon全体の`max_interactions`で制限する。Pluginは`interaction.result`を通常のnotificationとして処理し、interaction IDで自身の状態へ対応付ける。

Tool descriptorのproviderはPlugin ID、typeはmanifestのtool名、instanceは利用者が選ぶlogical instance名。CLIでは`ariadne tool new --provider ID TYPE`、TUIでは`:tool ID/TYPE [INSTANCE] [h|v]`で作る。Tool stateはCoreの既存JSON永続化を用い、state_versionとexpected_generationで更新する。独立したstorage APIは提供しない。

各frontend/viewにhostが異なるopaque view IDを発行する。`View`は`id/generation/runtime_generation/pane_id/type/instance/width/height/context/state_version/state_generation/state`。resizeでgenerationを増やし、resize前・close後の古いframeを棄却する。Pluginは返すFrameのview_idとgenerationをrequestからコピーする。

Frameは`view_id/generation/width/height/cells/cursor`とhostが付ける`runtime_generation`。frontendのinputは最後のframeからruntime_generationをコピーし、旧Plugin実行へのqueued inputをrestart後に実行しない。cellsはrow-majorでwidth×height個、寸法は4096以下・合計65,536 cells以下。各cellは単一のUTF-8 graphemeの`text`、`width`（1または2）、`style`。wide graphemeの直後は`text:""`・width 0のtail cellを置き、行をまたがない。text 256 bytes以下、control文字は禁止。styleはRGB foreground/background、bold/italic/underline/strikethrough/faint/blink、underline_style 0..5、任意underline_color。cursorは`x/y/visible/shape`、shapeは0..6。chrome・layout・最終ANSI描画はAriadneが担当する。

`view.input`は`view`とbase64 `data?`、`paste?`、`mouse?`。mouseは`x/y/button/action/wheel/shift/alt/ctrl`で、座標はcontent内。actionはpress=0、release=1、move=2。通常入力／paste／mouse／command／responseは置換しない。frontendの最終描画frameだけは既存latest-frame方式で置換できる。

widgetはmanifest名をnamespace化した`ID/widget`を既存status配置へ登録する。

```toml
[tui.status]
right = ["example-process/input-count", "clock"]

# 名前をaliasする場合
[tui.status.widgets.plugin_inputs]
plugin = "example-process/input-count"
format = " {output} "
style = "accent"
interval_ms = 5000
timeout_ms = 1000
```

builtin widgetの上書きは禁止。取得は非同期、1 widget同時1件・frontend全体4件、結果は4KiB以下。最後の正常値とerror markerを保持し、focus/CWD/generationが変わった古い結果を棄却する。format/styleは既存widgetと同じ。

## Frontend対話とeditor

prompt、confirm、既定editorは起動元frontendへ要求する。TUIはmodalを表示するがevent loopを継続する。CLIはstdin/stdoutが対話可能なterminalの場合だけ対応し、headlessでは明示errorを返す。frontend切断、利用者cancel、command取消で対話を取り消す。

editorは既定`commands.editor` argvに一時fileのpathを1 argument追加する。**編集完了までprocessが待機すること**が契約で、外部windowへ即時detachするeditorにはwait flagを設定する。textはUTF-8・64KiB以下。

editor PTYは直接一時stashed Paneとして作り、通常layoutへ差し込まない。TUIはstashと同じ全画面previewで表示し、以前のWorkspace/Window/preview/focus/zoomを保存する。正常exitでtextを返し、一時Pane/fileを削除して元表示へ戻す。close、preview離脱、detach、異常exitはcancel。一時Paneは永続snapshotとPluginのresource APIへ含めない。

## Native C ABI

[`ariadne_plugin.h`](../api/plugin/ariadne_plugin.h)はC/C++で利用できる。ABI versionとstruct_sizeを持つ`ariadne_plugin_init(host, plugin)`をexportする。host tableは`send(json,length)`と`log(text,length)`、plugin tableはinstance、`message(instance,json,length)`、`shutdown(instance)`を提供する。

ABI bufferはContent-Length headerを含まないJSON byte列で、そのcall中だけ有効。保持する側が必ずcopyする。allocator、Go内部型／pointerは境界を越えない。host tableはshutdown完了まで有効。

message callbackとshutdownはhelper内で逐次実行する。callbackは2秒以内に戻る短い処理とし、commandや対話はworkerへ渡して後でsendからresponseを返す。helperのprotocol reader・writerはcallbackと独立している。callback hangはhelper watchdogで終了する。shutdownが戻るまでにPlugin workerを停止し、停止後のhost callbackを呼ばない。helperは動作中のlibraryをunloadして復旧しない。

callback非zero returnは該当requestのhandler error。3回連続の通常handler errorで停止する。nativeのcrash、ABI不一致、protocol不正、timeoutは他Pluginや本体へ伝播させない。

## Limitsと障害処理

configの`[plugins]`で変更できる。全値は正、0は既定値を選択する。

| Config | 既定値 |
| --- | --- |
| `max_views` | 4096件（同時open、最大65536件） |
| `max_interactions` | 16件（同期・非同期対話の合計、最大1024件） |
| `message_bytes` | 8MiB |
| `control_queue` | 64件 |
| `control_bytes` | 16MiB |
| `pty_queue_bytes` | 1MiB |
| `initialize_ms` | 5000 |
| `api_ms` | 2000（native callbackも同じ） |
| `render_ms` | 1000（widgetも同じ） |
| `command_ms` | 30000 |
| `interaction_ms` | 900000 |
| `shutdown_ms` | 2000 |

message最大64MiB、control queue最大4096件、control／PTY queue bytes最大256MiB、期限は最大24時間。frontend管理requestも64件／16MiBまで。queue／PTY observation overflowでは対象Pluginを停止する。handler errorは連続3回で停止し、成功したhandlerで連続回数をresetする。

停止時はpending requestを失敗させ、自身のLabel・attentionを除去する。Pane・Tool state・固有data・永続enable設定は保持する。稼働中の自動復旧やcommand／入力の自動再送は行わず、restartする。daemon再起動時は保存されたenable設定に従って起動する。終了ではUnix process group／Windows Job Objectを使用し、通常の子孫processも片付ける。
