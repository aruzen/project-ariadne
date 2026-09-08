//go:build cgo && (darwin || (linux && amd64) || (windows && amd64))

package libghostty

/*
#include <stdlib.h>
#include <string.h>
#include <ghostty/vt.h>

typedef struct {
	uint8_t bytes[64];
	uint8_t length;
	uint8_t width;
	uint8_t foreground_r;
	uint8_t foreground_g;
	uint8_t foreground_b;
	uint8_t background_r;
	uint8_t background_g;
	uint8_t background_b;
	uint8_t flags;
} AriadneGhosttyCell;

enum {
	ARIADNE_CELL_BOLD = 1 << 0,
	ARIADNE_CELL_ITALIC = 1 << 1,
	ARIADNE_CELL_UNDERLINE = 1 << 2,
	ARIADNE_CELL_STRIKETHROUGH = 1 << 3,
	ARIADNE_CELL_FAINT = 1 << 4,
	ARIADNE_CELL_BLINK = 1 << 5,
};

typedef struct {
	uint16_t cols;
	uint16_t rows;
	uint16_t cursor_x;
	uint16_t cursor_y;
	uint8_t cursor_visible;
	AriadneGhosttyCell* cells;
} AriadneGhosttyScreen;

typedef struct {
	GhosttyTerminal terminal;
	GhosttyRenderState render;
	GhosttyRenderStateRowIterator rows;
	GhosttyRenderStateRowCells cells;
	uint8_t* response;
	size_t response_length;
	size_t response_capacity;
	uint8_t response_failed;
} AriadneGhosttyTerminal;

static void ariadne_ghostty_write_pty(
	GhosttyTerminal terminal, void* userdata, const uint8_t* data, size_t length) {
	(void)terminal;
	AriadneGhosttyTerminal* value = userdata;
	if (value == NULL || length == 0 || value->response_failed) return;
	if (length > SIZE_MAX - value->response_length) {
		value->response_failed = 1;
		return;
	}
	size_t required = value->response_length + length;
	if (required > value->response_capacity) {
		size_t capacity = value->response_capacity == 0 ? 64 : value->response_capacity;
		while (capacity < required) {
			if (capacity > SIZE_MAX / 2) {
				capacity = required;
				break;
			}
			capacity *= 2;
		}
		uint8_t* response = realloc(value->response, capacity);
		if (response == NULL) {
			value->response_failed = 1;
			return;
		}
		value->response = response;
		value->response_capacity = capacity;
	}
	memcpy(value->response + value->response_length, data, length);
	value->response_length += length;
}

static void ariadne_ghostty_terminal_free(AriadneGhosttyTerminal* value) {
	if (value == NULL) return;
	ghostty_render_state_row_cells_free(value->cells);
	ghostty_render_state_row_iterator_free(value->rows);
	ghostty_render_state_free(value->render);
	ghostty_terminal_free(value->terminal);
	free(value->response);
	free(value);
}

static int ariadne_ghostty_terminal_new(
	uint16_t cols, uint16_t rows, AriadneGhosttyTerminal** out) {
	AriadneGhosttyTerminal* value = calloc(1, sizeof(AriadneGhosttyTerminal));
	if (value == NULL) return GHOSTTY_OUT_OF_MEMORY;
	GhosttyResult result = ghostty_terminal_new(NULL, &value->terminal, cols, rows);
	if (result != GHOSTTY_SUCCESS) goto fail;
	result = ghostty_terminal_set(
		value->terminal, GHOSTTY_TERMINAL_OPT_USERDATA, value);
	if (result != GHOSTTY_SUCCESS) goto fail;
	result = ghostty_terminal_set(
		value->terminal, GHOSTTY_TERMINAL_OPT_WRITE_PTY, ariadne_ghostty_write_pty);
	if (result != GHOSTTY_SUCCESS) goto fail;
	result = ghostty_render_state_new(NULL, &value->render);
	if (result != GHOSTTY_SUCCESS) goto fail;
	result = ghostty_render_state_row_iterator_new(NULL, &value->rows);
	if (result != GHOSTTY_SUCCESS) goto fail;
	result = ghostty_render_state_row_cells_new(NULL, &value->cells);
	if (result != GHOSTTY_SUCCESS) goto fail;
	*out = value;
	return GHOSTTY_SUCCESS;

fail:
	ariadne_ghostty_terminal_free(value);
	return result;
}

static int ariadne_ghostty_terminal_resize(
	AriadneGhosttyTerminal* value, uint16_t cols, uint16_t rows) {
	return ghostty_terminal_resize(value->terminal, cols, rows, 0, 0);
}

static int ariadne_ghostty_terminal_write(
	AriadneGhosttyTerminal* value, const uint8_t* data, size_t length,
	const uint8_t** response, size_t* response_length) {
	value->response_length = 0;
	value->response_failed = 0;
	ghostty_terminal_vt_write(value->terminal, data, length);
	if (value->response_failed) return GHOSTTY_OUT_OF_MEMORY;
	*response = value->response;
	*response_length = value->response_length;
	return GHOSTTY_SUCCESS;
}

static void ariadne_ghostty_screen_free(AriadneGhosttyScreen* screen) {
	if (screen == NULL) return;
	free(screen->cells);
	memset(screen, 0, sizeof(*screen));
}

static int ariadne_ghostty_terminal_snapshot(
	AriadneGhosttyTerminal* value, AriadneGhosttyScreen* out) {
	memset(out, 0, sizeof(*out));
	GhosttyResult result = ghostty_render_state_update(value->render, value->terminal);
	if (result != GHOSTTY_SUCCESS) return result;
	result = ghostty_render_state_get(
		value->render, GHOSTTY_RENDER_STATE_DATA_COLS, &out->cols);
	if (result != GHOSTTY_SUCCESS) return result;
	result = ghostty_render_state_get(
		value->render, GHOSTTY_RENDER_STATE_DATA_ROWS, &out->rows);
	if (result != GHOSTTY_SUCCESS) return result;

	GhosttyRenderStateColors colors = GHOSTTY_INIT_SIZED(GhosttyRenderStateColors);
	result = ghostty_render_state_get(
		value->render, GHOSTTY_RENDER_STATE_DATA_COLORS, &colors);
	if (result != GHOSTTY_SUCCESS) return result;
	GhosttyRenderStateCursor cursor = GHOSTTY_INIT_SIZED(GhosttyRenderStateCursor);
	result = ghostty_render_state_get(
		value->render, GHOSTTY_RENDER_STATE_DATA_CURSOR, &cursor);
	if (result != GHOSTTY_SUCCESS) return result;
	if (cursor.visible && cursor.viewport_has_value) {
		out->cursor_visible = 1;
		out->cursor_x = cursor.viewport_x;
		out->cursor_y = cursor.viewport_y;
	}

	size_t count = (size_t)out->cols * (size_t)out->rows;
	out->cells = calloc(count, sizeof(AriadneGhosttyCell));
	if (out->cells == NULL && count != 0) return GHOSTTY_OUT_OF_MEMORY;
	result = ghostty_render_state_get(
		value->render, GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, &value->rows);
	if (result != GHOSTTY_SUCCESS) goto fail;

	uint16_t y = 0;
	while (y < out->rows && ghostty_render_state_row_iterator_next(value->rows)) {
		result = ghostty_render_state_row_get(
			value->rows, GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, &value->cells);
		if (result != GHOSTTY_SUCCESS) goto fail;
		uint16_t x = 0;
		while (x < out->cols && ghostty_render_state_row_cells_next(value->cells)) {
			AriadneGhosttyCell* target = &out->cells[(size_t)y * out->cols + x];
			target->width = 1;

			GhosttyCell raw = 0;
			GhosttyCellWide wide = GHOSTTY_CELL_WIDE_NARROW;
			if (ghostty_render_state_row_cells_get(
					value->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_RAW, &raw) == GHOSTTY_SUCCESS) {
				ghostty_cell_get(raw, GHOSTTY_CELL_DATA_WIDE, &wide);
			}
			if (wide == GHOSTTY_CELL_WIDE_WIDE) target->width = 2;
			if (wide == GHOSTTY_CELL_WIDE_SPACER_TAIL) target->width = 0;

			GhosttyBuffer text = {
				.ptr = target->bytes,
				.cap = sizeof(target->bytes),
				.len = 0,
			};
			result = ghostty_render_state_row_cells_get(
				value->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_UTF8, &text);
			if (result == GHOSTTY_SUCCESS) {
				target->length = (uint8_t)text.len;
			} else if (result == GHOSTTY_OUT_OF_SPACE) {
				target->bytes[0] = '?';
				target->length = 1;
			} else {
				goto fail;
			}

			GhosttyStyle style = GHOSTTY_INIT_SIZED(GhosttyStyle);
			result = ghostty_render_state_row_cells_get(
				value->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_STYLE, &style);
			if (result != GHOSTTY_SUCCESS) goto fail;
			GhosttyColorRgb foreground = colors.foreground;
			GhosttyColorRgb background = colors.background;
			ghostty_render_state_row_cells_get(
				value->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_FG_COLOR, &foreground);
			ghostty_render_state_row_cells_get(
				value->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_BG_COLOR, &background);
			if (style.inverse) {
				GhosttyColorRgb swap = foreground;
				foreground = background;
				background = swap;
			}
			if (style.invisible) target->length = 0;
			target->foreground_r = foreground.r;
			target->foreground_g = foreground.g;
			target->foreground_b = foreground.b;
			target->background_r = background.r;
			target->background_g = background.g;
			target->background_b = background.b;
			if (style.bold) target->flags |= ARIADNE_CELL_BOLD;
			if (style.italic) target->flags |= ARIADNE_CELL_ITALIC;
			if (style.underline != 0) target->flags |= ARIADNE_CELL_UNDERLINE;
			if (style.strikethrough) target->flags |= ARIADNE_CELL_STRIKETHROUGH;
			if (style.faint) target->flags |= ARIADNE_CELL_FAINT;
			if (style.blink) target->flags |= ARIADNE_CELL_BLINK;
			x++;
		}
		y++;
	}
	ghostty_render_state_clean(value->render);
	return GHOSTTY_SUCCESS;

fail:
	ariadne_ghostty_screen_free(out);
	return result;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

const (
	cellBold = 1 << iota
	cellItalic
	cellUnderline
	cellStrikethrough
	cellFaint
	cellBlink
)

type Color struct {
	R uint8
	G uint8
	B uint8
}

type Style struct {
	Foreground    Color
	Background    Color
	Bold          bool
	Italic        bool
	Underline     bool
	Strikethrough bool
	Faint         bool
	Blink         bool
}

type Cell struct {
	Text  string
	Width uint8
	Style Style
}

type Cursor struct {
	X       int
	Y       int
	Visible bool
}

type Screen struct {
	Cols   int
	Rows   int
	Cells  []Cell
	Cursor Cursor
}

func (screen Screen) At(x, y int) Cell {
	if x < 0 || y < 0 || x >= screen.Cols || y >= screen.Rows {
		return Cell{}
	}
	return screen.Cells[y*screen.Cols+x]
}

type Terminal struct {
	mu     sync.Mutex
	handle *C.AriadneGhosttyTerminal
}

func NewTerminal(cols, rows int) (*Terminal, error) {
	if err := validSize(cols, rows); err != nil {
		return nil, err
	}
	var handle *C.AriadneGhosttyTerminal
	if result := C.ariadne_ghostty_terminal_new(C.uint16_t(cols), C.uint16_t(rows), &handle); result != 0 {
		return nil, fmt.Errorf("ghostty_terminal_new: result %d", int(result))
	}
	terminal := &Terminal{handle: handle}
	runtime.SetFinalizer(terminal, (*Terminal).Close)
	return terminal, nil
}

func (terminal *Terminal) Write(data []byte) error {
	_, err := terminal.WriteWithResponse(data)
	return err
}

// WriteWithResponse updates terminal state and returns bytes generated for the PTY.
// The response includes device status and mode query replies.
func (terminal *Terminal) WriteWithResponse(data []byte) ([]byte, error) {
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if terminal.handle == nil {
		return nil, errors.New("libghostty: terminal is closed")
	}
	if len(data) == 0 {
		return nil, nil
	}
	var response *C.uint8_t
	var responseLength C.size_t
	result := C.ariadne_ghostty_terminal_write(
		terminal.handle, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data)),
		&response, &responseLength)
	if result != 0 {
		return nil, fmt.Errorf("ghostty_terminal_vt_write: result %d", int(result))
	}
	return C.GoBytes(unsafe.Pointer(response), C.int(responseLength)), nil
}

func (terminal *Terminal) Resize(cols, rows int) error {
	if err := validSize(cols, rows); err != nil {
		return err
	}
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if terminal.handle == nil {
		return errors.New("libghostty: terminal is closed")
	}
	if result := C.ariadne_ghostty_terminal_resize(terminal.handle, C.uint16_t(cols), C.uint16_t(rows)); result != 0 {
		return fmt.Errorf("ghostty_terminal_resize: result %d", int(result))
	}
	return nil
}

func (terminal *Terminal) Screen() (Screen, error) {
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if terminal.handle == nil {
		return Screen{}, errors.New("libghostty: terminal is closed")
	}
	var raw C.AriadneGhosttyScreen
	if result := C.ariadne_ghostty_terminal_snapshot(terminal.handle, &raw); result != 0 {
		return Screen{}, fmt.Errorf("ghostty render snapshot: result %d", int(result))
	}
	defer C.ariadne_ghostty_screen_free(&raw)
	count := int(raw.cols) * int(raw.rows)
	result := Screen{
		Cols: int(raw.cols), Rows: int(raw.rows), Cells: make([]Cell, count),
		Cursor: Cursor{X: int(raw.cursor_x), Y: int(raw.cursor_y), Visible: raw.cursor_visible != 0},
	}
	if count == 0 {
		return result, nil
	}
	cells := unsafe.Slice(raw.cells, count)
	for index := range cells {
		source := &cells[index]
		flags := uint8(source.flags)
		length := int(source.length)
		result.Cells[index] = Cell{
			Text:  C.GoStringN((*C.char)(unsafe.Pointer(&source.bytes[0])), C.int(length)),
			Width: uint8(source.width),
			Style: Style{
				Foreground:    Color{R: uint8(source.foreground_r), G: uint8(source.foreground_g), B: uint8(source.foreground_b)},
				Background:    Color{R: uint8(source.background_r), G: uint8(source.background_g), B: uint8(source.background_b)},
				Bold:          flags&cellBold != 0,
				Italic:        flags&cellItalic != 0,
				Underline:     flags&cellUnderline != 0,
				Strikethrough: flags&cellStrikethrough != 0,
				Faint:         flags&cellFaint != 0,
				Blink:         flags&cellBlink != 0,
			},
		}
	}
	return result, nil
}

func (terminal *Terminal) Close() {
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if terminal.handle != nil {
		C.ariadne_ghostty_terminal_free(terminal.handle)
		terminal.handle = nil
		runtime.SetFinalizer(terminal, nil)
	}
}

func validSize(cols, rows int) error {
	if cols < 1 || rows < 1 || cols > 65535 || rows > 65535 {
		return fmt.Errorf("libghostty: invalid terminal size %dx%d", cols, rows)
	}
	return nil
}
