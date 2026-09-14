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
)

type inputDecoder struct {
	prefix bool
}

func (decoder *inputDecoder) Feed(input []byte) ([]byte, []inputAction) {
	data := make([]byte, 0, len(input))
	var actions []inputAction
	for _, value := range input {
		if decoder.prefix {
			decoder.prefix = false
			switch value {
			case 'd':
				actions = append(actions, actionQuit)
			case 'h':
				actions = append(actions, actionFocusLeft)
			case 'j':
				actions = append(actions, actionFocusDown)
			case 'k':
				actions = append(actions, actionFocusUp)
			case 'l':
				actions = append(actions, actionFocusRight)
			case 0x08:
				actions = append(actions, actionResizeLeft)
			case 0x0a:
				actions = append(actions, actionResizeDown)
			case 0x0b:
				actions = append(actions, actionResizeUp)
			case 0x0c:
				actions = append(actions, actionResizeRight)
			case 'z':
				actions = append(actions, actionZoom)
			case '%':
				actions = append(actions, actionSplitHorizontal)
			case '"':
				actions = append(actions, actionSplitVertical)
			case 'x':
				actions = append(actions, actionClosePane)
			case 'r':
				actions = append(actions, actionRestart)
			case 'c':
				actions = append(actions, actionNewWindow)
			case 'n':
				actions = append(actions, actionNextWindow)
			case 'p':
				actions = append(actions, actionPreviousWindow)
			case ')':
				actions = append(actions, actionNextWorkspace)
			case '(':
				actions = append(actions, actionPreviousWorkspace)
			case ':':
				actions = append(actions, actionCommandPrompt)
			case ',':
				actions = append(actions, actionRenameWindow)
			case '$':
				actions = append(actions, actionRenameWorkspace)
			case '[':
				actions = append(actions, actionCopyMode)
			case ']':
				actions = append(actions, actionPaste)
			case 's':
				actions = append(actions, actionStashPane)
			case 'S':
				actions = append(actions, actionListStash)
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
	return data, actions
}
