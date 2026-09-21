# Go process Plugin

repository rootでbuildする。Go DTOだけをimportし、内部Core型は使用しない。

```sh
go build -o examples/plugins/process/plugin-process ./examples/plugins/process
# Windows:
go build -o examples/plugins/process/plugin-process.exe ./examples/plugins/process

ariadne plugin install examples/plugins/process
ariadne plugin enable example-process
ariadne plugin run example-process echo hello world
ariadne plugin grant example-process core.read workspace:1
ariadne plugin run example-process snapshot
ariadne plugin grant example-process frontend.interact all
ariadne plugin run example-process prompt
ariadne plugin grant example-process frontend.editor all
ariadne plugin run example-process editor initial text
ariadne tool new --provider example-process demo
```

TUIでは`:plugin`で管理、`:plugin run example-process prompt`で対話、`:tool example-process/demo`でPaneを作る。editorは`commands.editor`の既定argvを使用する。編集完了まで待機するeditorを設定する。

```toml
[tui.status]
right = ["example-process/input-count", "clock"]
```

監視はmanifestにcore.eventsとpty.observeを宣言しているが、未許可のままでは取得しない。例えば`ariadne plugin grant example-process pty.observe pane:1`で許可範囲のPTY eventを受け取れる。作例はeventをacknowledgeするだけで、保存しない。常駐処理を追加する場合もreaderをcommand／host APIのresponse待ちから分離する。

build後に更新する場合は`ariadne plugin update example-process examples/plugins/process`、その後`enable`する。updateで既存grant/dataを保持し、新しいcapabilityは自動許可しない。
