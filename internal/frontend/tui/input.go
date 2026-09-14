package tui

type inputAction uint8

const (
	actionNone inputAction = iota
	actionQuit
	actionFocusLeft
	actionFocusDown
	actionFocusUp
	actionFocusRight
	actionResizeLeft
	actionResizeDown
	actionResizeUp
	actionResizeRight
	actionZoom
	actionSplitHorizontal
	actionSplitVertical
	actionClosePane
	actionRestart
	actionNewWindow
	actionNextWindow
	actionPreviousWindow
	actionNextWorkspace
	actionPreviousWorkspace
	actionCommandPrompt
	actionRenameWindow
	actionRenameWorkspace
	actionCopyMode
	actionPaste
	actionStashPane
	actionListStash
	actionNextAttention
	actionPreviousAttention
	actionAcknowledgeAttention
	actionHelp
)

type inputDecoder struct {
	prefix bool
}

type inputToken struct {
	data   []byte
	action inputAction
}

// FeedOrdered preserves the ordering between PTY bytes and prefix actions.
// This matters for pasted input such as "text^A:command", where bytes before
// the prefix belong to the Pane and bytes after it belong to the prompt.
func (decoder *inputDecoder) FeedOrdered(input []byte) []inputToken {
	var tokens []inputToken
	data := make([]byte, 0, len(input))
	flush := func() {
		if len(data) != 0 {
			tokens = append(tokens, inputToken{data: append([]byte(nil), data...)})
			data = data[:0]
		}
	}
	action := func(value inputAction) {
		flush()
		tokens = append(tokens, inputToken{action: value})
	}
	for _, value := range input {
		if decoder.prefix {
			decoder.prefix = false
			switch value {
			case 'd':
				action(actionQuit)
			case 'h':
				action(actionFocusLeft)
			case 'j':
				action(actionFocusDown)
			case 'k':
				action(actionFocusUp)
			case 'l':
				action(actionFocusRight)
			case 0x08:
				action(actionResizeLeft)
			case 0x0a:
				action(actionResizeDown)
			case 0x0b:
				action(actionResizeUp)
			case 0x0c:
				action(actionResizeRight)
			case 'z':
				action(actionZoom)
			case '%':
				action(actionSplitHorizontal)
			case '"':
				action(actionSplitVertical)
			case 'x':
				action(actionClosePane)
			case 'r':
				action(actionRestart)
			case 'c':
				action(actionNewWindow)
			case 'n':
				action(actionNextWindow)
			case 'p':
				action(actionPreviousWindow)
			case ')':
				action(actionNextWorkspace)
			case '(':
				action(actionPreviousWorkspace)
			case ':':
				action(actionCommandPrompt)
			case ',':
				action(actionRenameWindow)
			case '$':
				action(actionRenameWorkspace)
			case '[':
				action(actionCopyMode)
			case ']':
				action(actionPaste)
			case 's':
				action(actionStashPane)
			case 'S':
				action(actionListStash)
			case 'a':
				action(actionNextAttention)
			case 'A':
				action(actionPreviousAttention)
			case 'm':
				action(actionAcknowledgeAttention)
			case '?':
				action(actionHelp)
			case 0x01:
				data = append(data, value)
			default:
				data = append(data, 0x01, value)
			}
			continue
		}
		if value == 0x01 {
			decoder.prefix = true
			continue
		}
		data = append(data, value)
	}
	flush()
	return tokens
}

func (decoder *inputDecoder) Feed(input []byte) ([]byte, []inputAction) {
	data := make([]byte, 0, len(input))
	var actions []inputAction
	for _, token := range decoder.FeedOrdered(input) {
		data = append(data, token.data...)
		if token.action != actionNone {
			actions = append(actions, token.action)
		}
	}
	return data, actions
}
