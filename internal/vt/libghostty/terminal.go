//go:build cgo && (darwin || linux || windows) && (amd64 || arm64)

package libghostty

/*
#include <stdlib.h>
#include <string.h>
#include <ghostty/vt.h>

typedef struct {
	uint8_t bytes[64];
	uint8_t* extra_bytes;
	size_t length;
	uint8_t width;
	uint8_t foreground_r;
	uint8_t foreground_g;
	uint8_t foreground_b;
	uint8_t background_r;
	uint8_t background_g;
	uint8_t background_b;
	uint8_t flags;
	uint8_t underline_style, underline_r, underline_g, underline_b, underline_color;
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
	uint8_t cursor_shape;
	AriadneGhosttyCell* cells;
} AriadneGhosttyScreen;

typedef struct {
	GhosttyTerminal terminal;
	GhosttySearch search;
	uintptr_t go_handle;
	GhosttyRenderState render;
	GhosttyRenderStateRowIterator rows;
	GhosttyRenderStateRowCells cells;
	uint8_t* response;
	size_t response_length;
	size_t response_capacity;
	uint8_t response_failed;
} AriadneGhosttyTerminal;

	extern int ariadneGoClipboardWrite(
	uintptr_t handle, int location, int kitty,
	uint8_t* name, size_t name_length,
	uint8_t* data, size_t data_length);
extern int ariadneGoClipboardRead(
	uintptr_t handle, int location, int kitty,
	uint8_t* name, size_t name_length,
	uint8_t** data, size_t* data_length);

static bool ariadne_ghostty_text_mime(GhosttyString mime) {
	return mime.len >= 10 && memcmp(mime.ptr, "text/plain", 10) == 0;
}

static void ariadne_ghostty_clipboard_write(
	GhosttyTerminal terminal, void* userdata, const GhosttyClipboardWrite* write) {
	(void)terminal;
	AriadneGhosttyTerminal* value = userdata;
	GhosttyClipboardWriteReply reply = GHOSTTY_INIT_SIZED(GhosttyClipboardWriteReply);
	reply.result = GHOSTTY_CLIPBOARD_WRITE_RESULT_UNSUPPORTED;
	const uint8_t* data = NULL;
	size_t data_length = 0;
	if (write->contents_len == 0) {
		reply.result = (GhosttyClipboardWriteResult)ariadneGoClipboardWrite(
			value->go_handle, write->location, write->can_remember,
			(uint8_t*)write->name.ptr, write->name.len, NULL, 0);
	} else {
		for (size_t index = 0; index < write->contents_len; index++) {
			if (!ariadne_ghostty_text_mime(write->contents[index].mime)) continue;
			data = write->contents[index].data.ptr;
			data_length = write->contents[index].data.len;
			reply.result = (GhosttyClipboardWriteResult)ariadneGoClipboardWrite(
				value->go_handle, write->location, write->can_remember,
				(uint8_t*)write->name.ptr, write->name.len, (uint8_t*)data, data_length);
			break;
		}
	}
	write->reply(write, &reply);
}

static void ariadne_ghostty_clipboard_read(
	GhosttyTerminal terminal, void* userdata, const GhosttyClipboardRead* read) {
	(void)terminal;
	AriadneGhosttyTerminal* value = userdata;
	GhosttyClipboardReadReply reply = GHOSTTY_INIT_SIZED(GhosttyClipboardReadReply);
	GhosttyString text_mime = {.ptr = (const uint8_t*)"text/plain", .len = 10};
	GhosttyClipboardContent content = {0};
	uint8_t* data = NULL;
	size_t data_length = 0;
	if (read->list) {
		reply.available = &text_mime;
		reply.available_len = 1;
	}
	if (read->mimes_len == 0) {
		reply.result = GHOSTTY_CLIPBOARD_READ_RESULT_SUCCESS;
		read->reply(read, &reply);
		return;
	}
	GhosttyString requested = {0};
	for (size_t index = 0; index < read->mimes_len; index++) {
		if (ariadne_ghostty_text_mime(read->mimes[index])) {
			requested = read->mimes[index];
			break;
		}
	}
	if (requested.ptr == NULL) {
		reply.result = GHOSTTY_CLIPBOARD_READ_RESULT_UNSUPPORTED;
		read->reply(read, &reply);
		return;
	}
	reply.result = (GhosttyClipboardReadResult)ariadneGoClipboardRead(
		value->go_handle, read->location, read->can_remember,
		(uint8_t*)read->name.ptr, read->name.len, &data, &data_length);
	if (reply.result == GHOSTTY_CLIPBOARD_READ_RESULT_SUCCESS) {
		content.mime = requested;
		content.data = (GhosttyString){.ptr = data, .len = data_length};
		reply.contents = &content;
		reply.contents_len = 1;
	}
	read->reply(read, &reply);
	free(data);
}

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
	ghostty_search_free(value->search);
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
	result = ghostty_search_new(NULL, &value->search, value->terminal);
	if (result != GHOSTTY_SUCCESS) goto fail;
	size_t scrollback_bytes = 8 * 1024 * 1024;
	result = ghostty_terminal_set(
		value->terminal, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES, &scrollback_bytes);
	if (result != GHOSTTY_SUCCESS) goto fail;
	*out = value;
	return GHOSTTY_SUCCESS;

fail:
	ariadne_ghostty_terminal_free(value);
	return result;
}

static int ariadne_ghostty_terminal_set_clipboard(
	AriadneGhosttyTerminal* value, uintptr_t go_handle) {
	value->go_handle = go_handle;
	GhosttyResult result = ghostty_terminal_set(
		value->terminal, GHOSTTY_TERMINAL_OPT_CLIPBOARD_WRITE,
		ariadne_ghostty_clipboard_write);
	if (result != GHOSTTY_SUCCESS) return result;
	return ghostty_terminal_set(
		value->terminal, GHOSTTY_TERMINAL_OPT_CLIPBOARD_READ,
		ariadne_ghostty_clipboard_read);
}

static int ariadne_ghostty_terminal_set_clipboard_max_bytes(
	AriadneGhosttyTerminal* value, size_t max_bytes) {
	return ghostty_terminal_set(value->terminal,
		GHOSTTY_TERMINAL_OPT_CLIPBOARD_WRITE_MAX_BYTES, &max_bytes);
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

static int ariadne_ghostty_terminal_scroll(
	AriadneGhosttyTerminal* value, int tag, intptr_t amount) {
	GhosttyTerminalScrollViewport scroll = {0};
	scroll.tag = (GhosttyTerminalScrollViewportTag)tag;
	if (scroll.tag == GHOSTTY_SCROLL_VIEWPORT_DELTA) scroll.value.delta = amount;
	else if (scroll.tag == GHOSTTY_SCROLL_VIEWPORT_ROW) scroll.value.row = (size_t)amount;
	ghostty_terminal_scroll_viewport(value->terminal, scroll);
	return GHOSTTY_SUCCESS;
}

static int ariadne_ghostty_terminal_scrollbar(
	AriadneGhosttyTerminal* value, GhosttyTerminalScrollbar* out) {
	return ghostty_terminal_get(value->terminal, GHOSTTY_TERMINAL_DATA_SCROLLBAR, out);
}

static int ariadne_ghostty_terminal_begin_selection(
	AriadneGhosttyTerminal* value, uint16_t x, uint32_t y) {
	GhosttyPoint point = {0};
	point.tag = GHOSTTY_POINT_TAG_VIEWPORT;
	point.value.coordinate.x = x;
	point.value.coordinate.y = y;
	GhosttyGridRef ref = GHOSTTY_INIT_SIZED(GhosttyGridRef);
	GhosttyResult result = ghostty_terminal_grid_ref(value->terminal, point, &ref);
	if (result != GHOSTTY_SUCCESS) return result;
	GhosttySelection selection = GHOSTTY_INIT_SIZED(GhosttySelection);
	selection.start = ref;
	selection.end = ref;
	return ghostty_terminal_set(value->terminal, GHOSTTY_TERMINAL_OPT_SELECTION, &selection);
}

static int ariadne_ghostty_terminal_adjust_selection(
	AriadneGhosttyTerminal* value, int adjustment) {
	GhosttySelection selection = GHOSTTY_INIT_SIZED(GhosttySelection);
	GhosttyResult result = ghostty_terminal_get(
		value->terminal, GHOSTTY_TERMINAL_DATA_SELECTION, &selection);
	if (result != GHOSTTY_SUCCESS) return result;
	result = ghostty_terminal_selection_adjust(
		value->terminal, &selection, (GhosttySelectionAdjust)adjustment);
	if (result != GHOSTTY_SUCCESS) return result;
	return ghostty_terminal_set(value->terminal, GHOSTTY_TERMINAL_OPT_SELECTION, &selection);
}

static int ariadne_ghostty_terminal_clear_selection(AriadneGhosttyTerminal* value) {
	return ghostty_terminal_set(value->terminal, GHOSTTY_TERMINAL_OPT_SELECTION, NULL);
}

static int ariadne_ghostty_terminal_selection_text(
	AriadneGhosttyTerminal* value, uint8_t** out, size_t* out_length) {
	GhosttyTerminalSelectionFormatOptions options =
		GHOSTTY_INIT_SIZED(GhosttyTerminalSelectionFormatOptions);
	options.emit = GHOSTTY_FORMATTER_FORMAT_PLAIN;
	options.unwrap = true;
	options.trim = true;
	return ghostty_terminal_selection_format_alloc(
		value->terminal, NULL, options, out, out_length);
}

typedef struct {
	const uint8_t* data;
	size_t length;
} AriadneGhosttyPasteData;

static bool ariadne_ghostty_paste_read(
	void* userdata, GhosttyString mime, GhosttyWriter writer) {
	(void)mime;
	AriadneGhosttyPasteData* data = userdata;
	if (data->length == 0) return true;
	return writer.write(writer.userdata, data->data, data->length);
}

static int ariadne_ghostty_terminal_paste(
	AriadneGhosttyTerminal* value, const uint8_t* data, size_t length,
	bool allow_unsafe, const uint8_t** response, size_t* response_length) {
	value->response_length = 0;
	value->response_failed = 0;
	GhosttyString mime = {.ptr = (const uint8_t*)"text/plain", .len = 10};
	AriadneGhosttyPasteData paste_data = {.data = data, .length = length};
	GhosttyPaste paste = GHOSTTY_INIT_SIZED(GhosttyPaste);
	paste.location = GHOSTTY_CLIPBOARD_LOCATION_STANDARD;
	paste.source = GHOSTTY_PASTE_SOURCE_CLIPBOARD;
	paste.mimes = &mime;
	paste.mimes_len = 1;
	paste.reader.read = ariadne_ghostty_paste_read;
	paste.reader.userdata = &paste_data;
	paste.allow_unsafe = allow_unsafe;
	bool written = false;
	GhosttyResult result = ghostty_terminal_paste(value->terminal, &paste, &written);
	if (result != GHOSTTY_SUCCESS) return result;
	if (value->response_failed) return GHOSTTY_OUT_OF_MEMORY;
	*response = value->response;
	*response_length = value->response_length;
	return GHOSTTY_SUCCESS;
}

static int ariadne_ghostty_terminal_search(
	AriadneGhosttyTerminal* value, const uint8_t* needle, size_t needle_length,
	bool next, size_t* total, size_t* selected) {
	GhosttyString string = {.ptr = needle, .len = needle_length};
	GhosttyResult result = ghostty_search_set(
		value->search, GHOSTTY_SEARCH_OPT_NEEDLE, needle_length == 0 ? NULL : &string);
	if (result != GHOSTTY_SUCCESS) return result;
	result = ghostty_search_run(value->search);
	if (result != GHOSTTY_SUCCESS) return result;
	result = ghostty_search_set(value->search,
		next ? GHOSTTY_SEARCH_OPT_SELECT_NEXT : GHOSTTY_SEARCH_OPT_SELECT_PREV, NULL);
	if (result != GHOSTTY_SUCCESS) return result;
	result = ghostty_search_get(value->search, GHOSTTY_SEARCH_DATA_TOTAL_MATCHES, total);
	if (result != GHOSTTY_SUCCESS) return result;
	result = ghostty_search_get(value->search, GHOSTTY_SEARCH_DATA_SELECTED_INDEX, selected);
	if (result != GHOSTTY_SUCCESS) return result;
	GhosttySelection match = GHOSTTY_INIT_SIZED(GhosttySelection);
	result = ghostty_search_get(value->search, GHOSTTY_SEARCH_DATA_SELECTED_MATCH, &match);
	if (result != GHOSTTY_SUCCESS) return result;
	return ghostty_terminal_set(value->terminal, GHOSTTY_TERMINAL_OPT_SELECTION, &match);
}

static void ariadne_ghostty_screen_free(AriadneGhosttyScreen* screen) {
	if (screen == NULL) return;
	if (screen->cells) {
		for (size_t i = 0; i < (size_t)screen->cols * screen->rows; i++) free(screen->cells[i].extra_bytes);
	}
	free(screen->cells);
	memset(screen, 0, sizeof(*screen));
}

static GhosttyString ariadne_ghostty_pwd(AriadneGhosttyTerminal* value) {
	GhosttyString pwd = {0};
	ghostty_terminal_get(value->terminal, GHOSTTY_TERMINAL_DATA_PWD, &pwd);
	return pwd;
}

static GhosttyString ariadne_ghostty_title(AriadneGhosttyTerminal* value) {
	GhosttyString title = {0};
	ghostty_terminal_get(value->terminal, GHOSTTY_TERMINAL_DATA_TITLE, &title);
	return title;
}

static int ariadne_ghostty_select_range(AriadneGhosttyTerminal* value, uint16_t x1, uint32_t y1, uint16_t x2, uint32_t y2) {
	GhosttySelection selection = GHOSTTY_INIT_SIZED(GhosttySelection);
	GhosttyPoint point = {0}; point.tag = GHOSTTY_POINT_TAG_VIEWPORT;
	point.value.coordinate.x = x1; point.value.coordinate.y = y1;
	int result = ghostty_terminal_grid_ref(value->terminal, point, &selection.start);
	if (result != GHOSTTY_SUCCESS) return result;
	point.value.coordinate.x = x2; point.value.coordinate.y = y2;
	result = ghostty_terminal_grid_ref(value->terminal, point, &selection.end);
	if (result != GHOSTTY_SUCCESS) return result;
	return ghostty_terminal_set(value->terminal, GHOSTTY_TERMINAL_OPT_SELECTION, &selection);
}

static bool ariadne_ghostty_mouse_tracking(AriadneGhosttyTerminal* value) {
	bool tracking = false;
	ghostty_terminal_get(value->terminal, GHOSTTY_TERMINAL_DATA_MOUSE_TRACKING, &tracking);
	return tracking;
}

static int ariadne_ghostty_mouse(AriadneGhosttyTerminal* value, int action, int button, int mods,
	int x, int y, int cols, int rows, bool pressed, char* out, size_t cap, size_t* length) {
	GhosttyMouseEncoder encoder = NULL; GhosttyMouseEvent event = NULL;
	int result = ghostty_mouse_encoder_new(NULL, &encoder);
	if (result != GHOSTTY_SUCCESS) return result;
	result = ghostty_mouse_event_new(NULL, &event);
	if (result != GHOSTTY_SUCCESS) { ghostty_mouse_encoder_free(encoder); return result; }
	ghostty_mouse_encoder_setopt_from_terminal(encoder, value->terminal);
	GhosttyMouseEncoderSize size = GHOSTTY_INIT_SIZED(GhosttyMouseEncoderSize);
	size.screen_width = cols; size.screen_height = rows; size.cell_width = 1; size.cell_height = 1;
	ghostty_mouse_encoder_setopt(encoder, GHOSTTY_MOUSE_ENCODER_OPT_SIZE, &size);
	ghostty_mouse_encoder_setopt(encoder, GHOSTTY_MOUSE_ENCODER_OPT_ANY_BUTTON_PRESSED, &pressed);
	ghostty_mouse_event_set_action(event, (GhosttyMouseAction)action);
	if (button) ghostty_mouse_event_set_button(event, (GhosttyMouseButton)button);
	ghostty_mouse_event_set_mods(event, (GhosttyMods)mods);
	GhosttyMousePosition position = { .x = x, .y = y };
	ghostty_mouse_event_set_position(event, position);
	result = ghostty_mouse_encoder_encode(encoder, event, out, cap, length);
	ghostty_mouse_event_free(event); ghostty_mouse_encoder_free(encoder);
	return result;
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
	out->cursor_shape = cursor.visual_style == GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_BAR ? 6 :
		cursor.visual_style == GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_UNDERLINE ? 4 : 2;
	if (cursor.blinking) out->cursor_shape--;

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
				target->length = text.len;
			} else if (result == GHOSTTY_OUT_OF_SPACE) {
				target->extra_bytes = malloc(text.len);
				if (!target->extra_bytes) { result = GHOSTTY_OUT_OF_MEMORY; goto fail; }
				text.ptr = target->extra_bytes; text.cap = text.len;
				result = ghostty_render_state_row_cells_get(value->cells,
					GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_UTF8, &text);
				if (result != GHOSTTY_SUCCESS) goto fail;
				target->length = text.len;
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
			target->underline_style = style.underline;
			if (style.underline_color.tag != GHOSTTY_STYLE_COLOR_NONE) {
				GhosttyColorRgb color = style.underline_color.tag == GHOSTTY_STYLE_COLOR_RGB ?
					style.underline_color.value.rgb : colors.palette[style.underline_color.value.palette];
				target->underline_color = 1;
				target->underline_r = color.r;
				target->underline_g = color.g;
				target->underline_b = color.b;
			}
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
	"runtime/cgo"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
	"unsafe"
)

var (
	ErrUnsafePaste = errors.New("libghostty: paste requires confirmation")
	ErrNoSelection = errors.New("libghostty: no selection")
	ErrNoMatch     = errors.New("libghostty: no search match")
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

var ErrBusy = errors.New("libghostty: terminal is busy; retry the operation")

type Style struct {
	UnderlineStyle    uint8
	UnderlineColor    Color
	HasUnderlineColor bool
	Foreground        Color
	Background        Color
	Bold              bool
	Italic            bool
	Underline         bool
	Strikethrough     bool
	Faint             bool
	Blink             bool
}

type Cell struct {
	Text  string
	Width uint8
	Style Style
}

type Cursor struct {
	Shape   uint8
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

type Scrollbar struct {
	Total  uint64
	Offset uint64
	Length uint64
}

type SelectionAdjust uint8

const (
	SelectionLeft SelectionAdjust = iota
	SelectionRight
	SelectionUp
	SelectionDown
	SelectionHome
	SelectionEnd
	SelectionPageUp
	SelectionPageDown
	SelectionLineStart
	SelectionLineEnd
)

type ClipboardOperation string

const (
	ClipboardRead  ClipboardOperation = "read"
	ClipboardWrite ClipboardOperation = "write"
)

type ClipboardRequest struct {
	Operation ClipboardOperation
	Location  string
	Protocol  string
	Name      string
	Text      string
}

type ClipboardResponse struct {
	Allowed bool
	Text    string
}

type ClipboardHandler func(ClipboardRequest) ClipboardResponse

func (screen Screen) At(x, y int) Cell {
	if x < 0 || y < 0 || x >= screen.Cols || y >= screen.Rows {
		return Cell{}
	}
	return screen.Cells[y*screen.Cols+x]
}

type Terminal struct {
	mu               sync.Mutex
	handle           *C.AriadneGhosttyTerminal
	goHandle         cgo.Handle
	clipboardHandler ClipboardHandler
	clipboardMax     int
	pwd              atomic.Pointer[string]
	title            atomic.Pointer[string]
	mouseTracking    atomic.Bool
}

func NewTerminal(cols, rows int) (*Terminal, error) {
	if err := validSize(cols, rows); err != nil {
		return nil, err
	}
	var handle *C.AriadneGhosttyTerminal
	if result := C.ariadne_ghostty_terminal_new(C.uint16_t(cols), C.uint16_t(rows), &handle); result != 0 {
		return nil, fmt.Errorf("ghostty_terminal_new: result %d", int(result))
	}
	terminal := &Terminal{handle: handle, clipboardMax: 1 << 20}
	terminal.goHandle = cgo.NewHandle(terminal)
	if result := C.ariadne_ghostty_terminal_set_clipboard(handle, C.uintptr_t(terminal.goHandle)); result != 0 {
		terminal.goHandle.Delete()
		C.ariadne_ghostty_terminal_free(handle)
		return nil, fmt.Errorf("ghostty terminal clipboard callbacks: result %d", int(result))
	}
	runtime.SetFinalizer(terminal, (*Terminal).Close)
	return terminal, nil
}

func (terminal *Terminal) SetClipboardHandler(handler ClipboardHandler) error {
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if terminal.handle == nil {
		return errors.New("libghostty: terminal is closed")
	}
	terminal.clipboardHandler = handler
	return nil
}

func (terminal *Terminal) SetClipboardMaxBytes(maxBytes int) error {
	if maxBytes < 1 {
		return errors.New("libghostty: invalid clipboard byte limit")
	}
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if terminal.handle == nil {
		return errors.New("libghostty: terminal is closed")
	}
	if result := C.ariadne_ghostty_terminal_set_clipboard_max_bytes(terminal.handle, C.size_t(maxBytes)); result != 0 {
		return fmt.Errorf("ghostty clipboard byte limit: result %d", int(result))
	}
	terminal.clipboardMax = maxBytes
	return nil
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
	pwd := C.ariadne_ghostty_pwd(terminal.handle)
	workingDirectory := C.GoStringN((*C.char)(unsafe.Pointer(pwd.ptr)), C.int(pwd.len))
	terminal.pwd.Store(&workingDirectory)
	title := C.ariadne_ghostty_title(terminal.handle)
	// Copy the borrowed string while holding the VT lock, with a bounded cache.
	terminalTitle := strings.ToValidUTF8(C.GoStringN((*C.char)(unsafe.Pointer(title.ptr)), C.int(min(title.len, 4096))), "")
	terminal.title.Store(&terminalTitle)
	terminal.mouseTracking.Store(bool(C.ariadne_ghostty_mouse_tracking(terminal.handle)))
	if result != 0 {
		return nil, fmt.Errorf("ghostty_terminal_vt_write: result %d", int(result))
	}
	return C.GoBytes(unsafe.Pointer(response), C.int(responseLength)), nil
}

func (terminal *Terminal) Resize(cols, rows int) error {
	if err := validSize(cols, rows); err != nil {
		return err
	}
	if !terminal.mu.TryLock() {
		return ErrBusy
	}
	defer terminal.mu.Unlock()
	if terminal.handle == nil {
		return errors.New("libghostty: terminal is closed")
	}
	if result := C.ariadne_ghostty_terminal_resize(terminal.handle, C.uint16_t(cols), C.uint16_t(rows)); result != 0 {
		return fmt.Errorf("ghostty_terminal_resize: result %d", int(result))
	}
	return nil
}

func (terminal *Terminal) Scroll(delta int) error {
	return terminal.scroll(int(C.GHOSTTY_SCROLL_VIEWPORT_DELTA), delta)
}

func (terminal *Terminal) ScrollTop() error {
	return terminal.scroll(int(C.GHOSTTY_SCROLL_VIEWPORT_TOP), 0)
}

func (terminal *Terminal) ScrollBottom() error {
	return terminal.scroll(int(C.GHOSTTY_SCROLL_VIEWPORT_BOTTOM), 0)
}

func (terminal *Terminal) scroll(tag, amount int) error {
	if !terminal.mu.TryLock() {
		return ErrBusy
	}
	defer terminal.mu.Unlock()
	if terminal.handle == nil {
		return errors.New("libghostty: terminal is closed")
	}
	if result := C.ariadne_ghostty_terminal_scroll(terminal.handle, C.int(tag), C.intptr_t(amount)); result != 0 {
		return fmt.Errorf("ghostty_terminal_scroll_viewport: result %d", int(result))
	}
	return nil
}

func (terminal *Terminal) Scrollbar() (Scrollbar, error) {
	if !terminal.mu.TryLock() {
		return Scrollbar{}, ErrBusy
	}
	defer terminal.mu.Unlock()
	if terminal.handle == nil {
		return Scrollbar{}, errors.New("libghostty: terminal is closed")
	}
	var value C.GhosttyTerminalScrollbar
	if result := C.ariadne_ghostty_terminal_scrollbar(terminal.handle, &value); result != 0 {
		return Scrollbar{}, fmt.Errorf("ghostty terminal scrollbar: result %d", int(result))
	}
	return Scrollbar{Total: uint64(value.total), Offset: uint64(value.offset), Length: uint64(value.len)}, nil
}

func (terminal *Terminal) BeginSelection(x, y int) error {
	if x < 0 || y < 0 || x > 65535 || uint64(y) > uint64(^uint32(0)) {
		return errors.New("libghostty: invalid selection point")
	}
	if !terminal.mu.TryLock() {
		return ErrBusy
	}
	defer terminal.mu.Unlock()
	if terminal.handle == nil {
		return errors.New("libghostty: terminal is closed")
	}
	if result := C.ariadne_ghostty_terminal_begin_selection(terminal.handle, C.uint16_t(x), C.uint32_t(y)); result != 0 {
		return fmt.Errorf("ghostty begin selection: result %d", int(result))
	}
	return nil
}

func (terminal *Terminal) AdjustSelection(adjustment SelectionAdjust) error {
	if adjustment > SelectionLineEnd {
		return errors.New("libghostty: invalid selection adjustment")
	}
	if !terminal.mu.TryLock() {
		return ErrBusy
	}
	defer terminal.mu.Unlock()
	if terminal.handle == nil {
		return errors.New("libghostty: terminal is closed")
	}
	if result := C.ariadne_ghostty_terminal_adjust_selection(terminal.handle, C.int(adjustment)); result != 0 {
		return fmt.Errorf("ghostty adjust selection: result %d", int(result))
	}
	return nil
}

func (terminal *Terminal) ClearSelection() error {
	if !terminal.mu.TryLock() {
		return ErrBusy
	}
	defer terminal.mu.Unlock()
	if terminal.handle == nil {
		return errors.New("libghostty: terminal is closed")
	}
	if result := C.ariadne_ghostty_terminal_clear_selection(terminal.handle); result != 0 {
		return fmt.Errorf("ghostty clear selection: result %d", int(result))
	}
	return nil
}

func (terminal *Terminal) SelectionText() (string, error) {
	if !terminal.mu.TryLock() {
		return "", ErrBusy
	}
	defer terminal.mu.Unlock()
	if terminal.handle == nil {
		return "", errors.New("libghostty: terminal is closed")
	}
	var pointer *C.uint8_t
	var length C.size_t
	result := C.ariadne_ghostty_terminal_selection_text(terminal.handle, &pointer, &length)
	if result == C.GHOSTTY_NO_VALUE {
		return "", ErrNoSelection
	}
	if result != C.GHOSTTY_SUCCESS {
		return "", fmt.Errorf("ghostty selection text: result %d", int(result))
	}
	defer C.ghostty_free(nil, pointer, length)
	return C.GoStringN((*C.char)(unsafe.Pointer(pointer)), C.int(length)), nil
}

func (terminal *Terminal) Paste(data []byte, allowUnsafe bool) ([]byte, error) {
	if !terminal.mu.TryLock() {
		return nil, ErrBusy
	}
	defer terminal.mu.Unlock()
	if terminal.handle == nil {
		return nil, errors.New("libghostty: terminal is closed")
	}
	var input *C.uint8_t
	if len(data) != 0 {
		input = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	var response *C.uint8_t
	var responseLength C.size_t
	result := C.ariadne_ghostty_terminal_paste(
		terminal.handle, input, C.size_t(len(data)), C.bool(allowUnsafe), &response, &responseLength)
	if result == C.GHOSTTY_REJECTED {
		return nil, ErrUnsafePaste
	}
	if result != C.GHOSTTY_SUCCESS {
		return nil, fmt.Errorf("ghostty terminal paste: result %d", int(result))
	}
	return C.GoBytes(unsafe.Pointer(response), C.int(responseLength)), nil
}

func (terminal *Terminal) Search(needle string, next bool) (total, selected int, err error) {
	if needle == "" {
		return 0, 0, errors.New("libghostty: empty search")
	}
	if !terminal.mu.TryLock() {
		return 0, 0, ErrBusy
	}
	defer terminal.mu.Unlock()
	if terminal.handle == nil {
		return 0, 0, errors.New("libghostty: terminal is closed")
	}
	data := []byte(needle)
	var rawTotal, rawSelected C.size_t
	result := C.ariadne_ghostty_terminal_search(
		terminal.handle, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data)), C.bool(next), &rawTotal, &rawSelected)
	if result == C.GHOSTTY_NO_VALUE {
		return 0, 0, ErrNoMatch
	}
	if result != C.GHOSTTY_SUCCESS {
		return 0, 0, fmt.Errorf("ghostty terminal search: result %d", int(result))
	}
	return int(rawTotal), int(rawSelected), nil
}

func (terminal *Terminal) Screen() (Screen, error) {
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	return terminal.screenLocked()
}

// TryScreen never waits behind VT writes or an interactive clipboard callback.
func (terminal *Terminal) TryScreen() (Screen, error) {
	if !terminal.mu.TryLock() {
		return Screen{}, ErrBusy
	}
	defer terminal.mu.Unlock()
	return terminal.screenLocked()
}

func (terminal *Terminal) screenLocked() (Screen, error) {
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
		Cursor: Cursor{X: int(raw.cursor_x), Y: int(raw.cursor_y), Visible: raw.cursor_visible != 0, Shape: uint8(raw.cursor_shape)},
	}
	if count == 0 {
		return result, nil
	}
	cells := unsafe.Slice(raw.cells, count)
	for index := range cells {
		source := &cells[index]
		flags := uint8(source.flags)
		length := int(source.length)
		pointer := unsafe.Pointer(&source.bytes[0])
		if source.extra_bytes != nil {
			pointer = unsafe.Pointer(source.extra_bytes)
		}
		result.Cells[index] = Cell{
			Text:  C.GoStringN((*C.char)(pointer), C.int(length)),
			Width: uint8(source.width),
			Style: Style{
				UnderlineStyle:    uint8(source.underline_style),
				UnderlineColor:    Color{R: uint8(source.underline_r), G: uint8(source.underline_g), B: uint8(source.underline_b)},
				HasUnderlineColor: source.underline_color != 0,
				Foreground:        Color{R: uint8(source.foreground_r), G: uint8(source.foreground_g), B: uint8(source.foreground_b)},
				Background:        Color{R: uint8(source.background_r), G: uint8(source.background_g), B: uint8(source.background_b)},
				Bold:              flags&cellBold != 0,
				Italic:            flags&cellItalic != 0,
				Underline:         flags&cellUnderline != 0,
				Strikethrough:     flags&cellStrikethrough != 0,
				Faint:             flags&cellFaint != 0,
				Blink:             flags&cellBlink != 0,
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
		terminal.goHandle.Delete()
		terminal.goHandle = 0
		terminal.pwd.Store(nil)
		terminal.title.Store(nil)
		terminal.mouseTracking.Store(false)
		runtime.SetFinalizer(terminal, nil)
	}
}

// WorkingDirectory returns the terminal-reported value, not a daemon setting.
func (terminal *Terminal) WorkingDirectory() string {
	if value := terminal.pwd.Load(); value != nil {
		return *value
	}
	return ""
}

// Title returns the last processed OSC 0/2 title without waiting for a VT lock.
// It is frontend metadata and does not rename or persist a core Pane.
func (terminal *Terminal) Title() string {
	if value := terminal.title.Load(); value != nil {
		return *value
	}
	return ""
}

func (terminal *Terminal) SelectRange(x1, y1, x2, y2 int) error {
	if min(x1, y1, x2, y2) < 0 || max(x1, x2) > 65535 {
		return errors.New("libghostty: invalid selection range")
	}
	if !terminal.mu.TryLock() {
		return ErrBusy
	}
	defer terminal.mu.Unlock()
	if terminal.handle == nil {
		return errors.New("libghostty: terminal is closed")
	}
	if result := C.ariadne_ghostty_select_range(terminal.handle, C.uint16_t(x1), C.uint32_t(y1), C.uint16_t(x2), C.uint32_t(y2)); result != 0 {
		return fmt.Errorf("ghostty select range: result %d", int(result))
	}
	return nil
}

func (terminal *Terminal) MouseTracking() bool {
	return terminal.mouseTracking.Load()
}

// EncodeMouse takes normalized actions (press=0/release=1/motion=2), Ghostty
// button numbers and modifiers (Shift=1/Ctrl=2/Alt=4). Coordinates are cells.
func (terminal *Terminal) EncodeMouse(action, button, mods, x, y, cols, rows int, pressed bool) ([]byte, error) {
	if !terminal.mu.TryLock() {
		return nil, ErrBusy
	}
	defer terminal.mu.Unlock()
	if terminal.handle == nil {
		return nil, errors.New("libghostty: terminal is closed")
	}
	var buffer [256]C.char
	var length C.size_t
	if result := C.ariadne_ghostty_mouse(terminal.handle, C.int(action), C.int(button), C.int(mods), C.int(x), C.int(y), C.int(cols), C.int(rows), C.bool(pressed), &buffer[0], C.size_t(len(buffer)), &length); result != 0 {
		return nil, fmt.Errorf("ghostty mouse encode: result %d", int(result))
	}
	return C.GoBytes(unsafe.Pointer(&buffer[0]), C.int(length)), nil
}

func clipboardLocation(location C.int) string {
	switch location {
	case C.GHOSTTY_CLIPBOARD_LOCATION_SELECTION:
		return "selection"
	case C.GHOSTTY_CLIPBOARD_LOCATION_PRIMARY:
		return "primary"
	default:
		return "standard"
	}
}

func clipboardProtocol(kitty C.int) string {
	if kitty != 0 {
		return "kitty"
	}
	return "osc"
}

//export ariadneGoClipboardWrite
func ariadneGoClipboardWrite(handle C.uintptr_t, location C.int, kitty C.int, name *C.uint8_t, nameLength C.size_t, data *C.uint8_t, dataLength C.size_t) C.int {
	terminal, ok := cgo.Handle(handle).Value().(*Terminal)
	if !ok || terminal.clipboardHandler == nil {
		return C.GHOSTTY_CLIPBOARD_WRITE_RESULT_DENIED
	}
	if uint64(dataLength) > uint64(terminal.clipboardMax) {
		return C.GHOSTTY_CLIPBOARD_WRITE_RESULT_INVALID_DATA
	}
	text := C.GoStringN((*C.char)(unsafe.Pointer(data)), C.int(dataLength))
	if !utf8.ValidString(text) {
		return C.GHOSTTY_CLIPBOARD_WRITE_RESULT_INVALID_DATA
	}
	request := ClipboardRequest{
		Operation: ClipboardWrite, Location: clipboardLocation(location), Protocol: clipboardProtocol(kitty),
		Name: C.GoStringN((*C.char)(unsafe.Pointer(name)), C.int(nameLength)), Text: text,
	}
	if !terminal.clipboardHandler(request).Allowed {
		return C.GHOSTTY_CLIPBOARD_WRITE_RESULT_DENIED
	}
	return C.GHOSTTY_CLIPBOARD_WRITE_RESULT_SUCCESS
}

//export ariadneGoClipboardRead
func ariadneGoClipboardRead(handle C.uintptr_t, location C.int, kitty C.int, name *C.uint8_t, nameLength C.size_t, data **C.uint8_t, dataLength *C.size_t) C.int {
	terminal, ok := cgo.Handle(handle).Value().(*Terminal)
	if !ok || terminal.clipboardHandler == nil {
		return C.GHOSTTY_CLIPBOARD_READ_RESULT_DENIED
	}
	request := ClipboardRequest{
		Operation: ClipboardRead, Location: clipboardLocation(location), Protocol: clipboardProtocol(kitty),
		Name: C.GoStringN((*C.char)(unsafe.Pointer(name)), C.int(nameLength)),
	}
	response := terminal.clipboardHandler(request)
	if !response.Allowed {
		return C.GHOSTTY_CLIPBOARD_READ_RESULT_DENIED
	}
	if !utf8.ValidString(response.Text) || len(response.Text) > terminal.clipboardMax {
		return C.GHOSTTY_CLIPBOARD_READ_RESULT_IO_ERROR
	}
	raw := []byte(response.Text)
	if len(raw) != 0 {
		*data = (*C.uint8_t)(C.CBytes(raw))
		if *data == nil {
			return C.GHOSTTY_CLIPBOARD_READ_RESULT_IO_ERROR
		}
	}
	*dataLength = C.size_t(len(raw))
	return C.GHOSTTY_CLIPBOARD_READ_RESULT_SUCCESS
}

func validSize(cols, rows int) error {
	if cols < 1 || rows < 1 || cols > 65535 || rows > 65535 {
		return fmt.Errorf("libghostty: invalid terminal size %dx%d", cols, rows)
	}
	return nil
}
