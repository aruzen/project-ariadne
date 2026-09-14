package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	ariadneconfig "github.com/aruzen/ariadne/internal/config"
)

// inputAction remains the internal direction type used by focus and resize.
type inputAction uint8

const (
	actionNone inputAction = iota
	actionFocusLeft
	actionFocusDown
	actionFocusUp
	actionFocusRight
	actionResizeLeft
	actionResizeDown
	actionResizeUp
	actionResizeRight
)

type inputToken struct {
	data     []byte
	commands []string
}

type inputDecoder struct {
	bindings map[string][]string
	prefixes map[string]struct{}
	pending  []byte
}

func newInputDecoder(bindings ariadneconfig.Keybindings) (inputDecoder, error) {
	bindings = resolvedKeybindings(bindings)
	decoder := inputDecoder{
		bindings: make(map[string][]string),
		prefixes: make(map[string]struct{}),
	}
	for specification, commandText := range bindings {
		if strings.TrimSpace(commandText) == "" {
			continue
		}
		sequence, err := parseKeySequence(specification)
		if err != nil {
			return inputDecoder{}, fmt.Errorf("keybinding %q: %w", specification, err)
		}
		commands, err := splitPromptCommands(commandText)
		if err != nil {
			return inputDecoder{}, fmt.Errorf("keybinding %q: %w", specification, err)
		}
		for _, command := range commands {
			fields, _ := parsePromptCommand(command)
			name := ""
			if len(fields) != 0 {
				name = fields[0]
			}
			if !isPromptCommandName(name) {
				return inputDecoder{}, fmt.Errorf("keybinding %q: unknown command %q", specification, name)
			}
		}
		key := string(sequence)
		if _, exists := decoder.bindings[key]; exists {
			return inputDecoder{}, fmt.Errorf("keybinding %q duplicates another encoded sequence", specification)
		}
		decoder.bindings[key] = commands
		for size := 1; size < len(sequence); size++ {
			decoder.prefixes[string(sequence[:size])] = struct{}{}
		}
	}
	for sequence := range decoder.bindings {
		if _, conflict := decoder.prefixes[sequence]; conflict {
			return inputDecoder{}, fmt.Errorf("keybinding sequence %q is a prefix of another binding", printableKeySequence([]byte(sequence)))
		}
	}
	return decoder, nil
}

func resolvedKeybindings(bindings ariadneconfig.Keybindings) ariadneconfig.Keybindings {
	if bindings == nil {
		bindings = ariadneconfig.DefaultKeybindings()
	}
	result := make(ariadneconfig.Keybindings, len(bindings))
	for keys, commands := range bindings {
		result[keys] = commands
	}
	return result
}

// Feed preserves ordering between ordinary PTY bytes and configured commands.
// Bytes that cease to match a binding prefix are forwarded without loss.
func (decoder *inputDecoder) Feed(input []byte) []inputToken {
	var tokens []inputToken
	data := make([]byte, 0, len(input))
	flushData := func() {
		if len(data) == 0 {
			return
		}
		tokens = append(tokens, inputToken{data: append([]byte(nil), data...)})
		data = data[:0]
	}
	for _, value := range input {
		decoder.pending = append(decoder.pending, value)
		for len(decoder.pending) != 0 {
			key := string(decoder.pending)
			if commands, exists := decoder.bindings[key]; exists {
				flushData()
				tokens = append(tokens, inputToken{commands: append([]string(nil), commands...)})
				decoder.pending = decoder.pending[:0]
				break
			}
			if _, prefix := decoder.prefixes[key]; prefix {
				break
			}
			data = append(data, decoder.pending[0])
			decoder.pending = decoder.pending[1:]
		}
	}
	flushData()
	return tokens
}

func parseKeySequence(specification string) ([]byte, error) {
	tokens := strings.Fields(specification)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("empty key sequence")
	}
	var result []byte
	for _, token := range tokens {
		encoded, err := parseKeyToken(token)
		if err != nil {
			return nil, err
		}
		result = append(result, encoded...)
	}
	return result, nil
}

func parseKeyToken(token string) ([]byte, error) {
	lower := strings.ToLower(token)
	switch lower {
	case "space":
		return []byte{' '}, nil
	case "tab":
		return []byte{'\t'}, nil
	case "enter", "return":
		return []byte{'\r'}, nil
	case "escape", "esc":
		return []byte{0x1b}, nil
	case "backspace":
		return []byte{0x7f}, nil
	case "up":
		return []byte("\x1b[A"), nil
	case "down":
		return []byte("\x1b[B"), nil
	case "right":
		return []byte("\x1b[C"), nil
	case "left":
		return []byte("\x1b[D"), nil
	case "page-up":
		return []byte("\x1b[5~"), nil
	case "page-down":
		return []byte("\x1b[6~"), nil
	case "home":
		return []byte("\x1b[H"), nil
	case "end":
		return []byte("\x1b[F"), nil
	case "insert":
		return []byte("\x1b[2~"), nil
	case "delete":
		return []byte("\x1b[3~"), nil
	case "f1":
		return []byte("\x1bOP"), nil
	case "f2":
		return []byte("\x1bOQ"), nil
	case "f3":
		return []byte("\x1bOR"), nil
	case "f4":
		return []byte("\x1bOS"), nil
	case "f5":
		return []byte("\x1b[15~"), nil
	case "f6":
		return []byte("\x1b[17~"), nil
	case "f7":
		return []byte("\x1b[18~"), nil
	case "f8":
		return []byte("\x1b[19~"), nil
	case "f9":
		return []byte("\x1b[20~"), nil
	case "f10":
		return []byte("\x1b[21~"), nil
	case "f11":
		return []byte("\x1b[23~"), nil
	case "f12":
		return []byte("\x1b[24~"), nil
	}
	for _, prefix := range []string{"alt-", "alt+", "meta-", "meta+", "m-", "m+"} {
		if strings.HasPrefix(lower, prefix) {
			encoded, err := parseKeyToken(token[len(prefix):])
			if err != nil {
				return nil, err
			}
			return append([]byte{0x1b}, encoded...), nil
		}
	}
	control := ""
	switch {
	case strings.HasPrefix(lower, "ctrl-"):
		control = lower[len("ctrl-"):]
	case strings.HasPrefix(lower, "ctrl+"):
		control = lower[len("ctrl+"):]
	case strings.HasPrefix(lower, "c-"):
		control = lower[len("c-"):]
	case strings.HasPrefix(lower, "c+"):
		control = lower[len("c+"):]
	case strings.HasPrefix(lower, "^"):
		control = lower[1:]
	}
	if control != "" {
		if control == "?" {
			return []byte{0x7f}, nil
		}
		if len(control) == 1 && control[0] >= '@' && control[0] <= '_' {
			return []byte{control[0] & 0x1f}, nil
		}
		if len(control) == 1 && control[0] >= 'a' && control[0] <= 'z' {
			return []byte{control[0] - 'a' + 1}, nil
		}
		return nil, fmt.Errorf("unsupported control key %q", token)
	}
	if utf8.RuneCountInString(token) == 1 {
		return []byte(token), nil
	}
	return nil, fmt.Errorf("unsupported key %q", token)
}

func printableKeySequence(sequence []byte) string {
	return fmt.Sprintf("% x", sequence)
}
