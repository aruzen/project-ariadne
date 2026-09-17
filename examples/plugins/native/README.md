# C/C++ native Plugin

repository rootで、現在のOS／architecture向けlibraryをbuildする。大きなSDKは不要で、C ABI headerだけをincludeする。

```sh
# macOS C
cc -dynamiclib -Wall -Wextra -Werror -o examples/plugins/native/plugin.dylib examples/plugins/native/plugin.c
# macOS C++
c++ -x c++ -dynamiclib -Wall -Wextra -Werror -o examples/plugins/native/plugin.dylib examples/plugins/native/plugin.c
# Linux C / C++
cc -shared -fPIC -Wall -Wextra -Werror -o examples/plugins/native/plugin.so examples/plugins/native/plugin.c
c++ -x c++ -shared -fPIC -Wall -Wextra -Werror -o examples/plugins/native/plugin.so examples/plugins/native/plugin.c
# Windows MinGW C / C++
gcc -shared -Wall -Wextra -Werror -o examples/plugins/native/plugin.dll examples/plugins/native/plugin.c
g++ -x c++ -shared -Wall -Wextra -Werror -o examples/plugins/native/plugin.dll examples/plugins/native/plugin.c

ariadne plugin install examples/plugins/native
ariadne plugin enable example-native
ariadne plugin run example-native hello
ariadne tool new --provider example-native demo
```

TUIでは`:tool example-native/demo`を使用する。widgetは`example-native/status`。この作例にはhost操作がなく、capability grantは不要。

libraryは本体へ読み込まず、同じAriadne binaryの専用helper processへ読み込む。native stdoutはPlugin data directoryのstderr.logへ記録する。bufferはcall中だけ有効で、保持時はcopyする。message/shutdownは逐次、send/logはworker threadからも呼べる。

作例のJSON検査は短い説明用のcompact JSON parserで、Ariadneが生成するmessageだけを対象にする。production Pluginには完全なJSON parserを使用する。callbackから同期的にhost API responseを待つと期限切れになるため、長いcommand・対話はworkerへ渡し、callbackを戻してからsendで結果を返す。shutdown完了前にすべてのworkerを停止する。

manifestに対象OS／architectureのentrypointを宣言する。Ariadne側のnative loaderにはCGOが必要。DLL依存libraryがあればpackageに含め、Windowsではpackage directoryと安全なdefault DLL search directoryから読み込む。
