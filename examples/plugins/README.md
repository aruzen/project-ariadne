# External Plugin examples

- [Go process](process/README.md): echo、許可されたCore snapshot、prompt/editor、ToolPane、入力数widget。
- [C/C++ native](native/README.md): helper process経由のcommand、セル描画、widget、stdoutのlog分離。
- [API v1仕様](../../docs/plugin-protocol-v1.md): manifest、capability/scope、JSON-RPC、C ABI、障害処理。

信頼済みのローカルcodeだけをinstallする。Ariadne API権限はOSのfile／network sandboxではない。新規install・updateはdisabledで、grantを明示してからenableする。GUI／marketplace／downloadは初版の対象外。
