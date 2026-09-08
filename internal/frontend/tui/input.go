package tui

type inputAction uint8

const (
	actionNone inputAction = iota
	actionQuit
	actionFocusLeft
	actionFocusDown
	actionFocusUp
	actionFocusRight
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
