package tui

import (
	"bytes"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/aruzen/ariadne/internal/vt/libghostty"
)

func oraclePair(t testing.TB, w, h int) (*libghostty.Terminal, *libghostty.Terminal) {
	t.Helper()
	a, err := libghostty.NewTerminal(w, h)
	if err != nil {
		t.Fatal(err)
	}
	b, err := libghostty.NewTerminal(w, h)
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	t.Cleanup(b.Close)
	return a, b
}

func compareOracle(t testing.TB, a, b *libghostty.Terminal) {
	t.Helper()
	sa, err := a.Screen()
	if err != nil {
		t.Fatal(err)
	}
	sb, err := b.Screen()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sa, sb) {
		for i := range sa.Cells {
			if sa.Cells[i] != sb.Cells[i] {
				t.Fatalf("cell %d full=%+v diff=%+v", i, sa.Cells[i], sb.Cells[i])
			}
		}
		t.Fatalf("full cursor=%+v diff cursor=%+v", sa.Cursor, sb.Cursor)
	}
}

func TestDiffMatchesFullFrameAcrossWideAndStyleTransitions(t *testing.T) {
	a, b := oraclePair(t, 32, 6)
	rng := rand.New(rand.NewSource(42))
	var previous *Surface
	var before Cursor
	texts := []string{" ", "a", "界", "é", "👩‍💻", "🙂", "。"}
	for frame := 0; frame < 200; frame++ {
		next := NewSurface(32, 6, Style{Foreground: Color{R: 220}, Background: Color{B: 10}})
		if previous != nil {
			copy(next.Cells, previous.Cells)
		}
		for n := 0; n < 12; n++ {
			text := texts[rng.Intn(len(texts))]
			style := Style{Foreground: Color{R: uint8(rng.Intn(255)), G: 70}, Background: Color{B: 10}, Bold: rng.Intn(2) == 0, UnderlineStyle: uint8(rng.Intn(6)), HasUnderlineColor: true, UnderlineColor: Color{R: 12, G: 100, B: 150}}
			next.Set(rng.Intn(32), rng.Intn(6), Cell{Text: text, Width: uint8(textWidth(text)), Style: style})
		}
		cursor := Cursor{X: rng.Intn(32), Y: rng.Intn(6), Visible: rng.Intn(2) == 0, Shape: uint8(1 + rng.Intn(6))}
		if err := a.Write(EncodeFrame(next, cursor)); err != nil {
			t.Fatal(err)
		}
		if err := b.Write(EncodeDiff(previous, next, before, cursor)); err != nil {
			t.Fatal(err)
		}
		compareOracle(t, a, b)
		previous, before = next, cursor
	}
}

func FuzzDiffMatchesFullFrame(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 40, 100, 255, 1, 31, 4, 2})
	f.Fuzz(func(t *testing.T, data []byte) {
		a, b := oraclePair(t, 16, 3)
		previous := NewSurface(16, 3, Style{})
		cursor := Cursor{}
		_ = a.Write(EncodeFrame(previous, cursor))
		_ = b.Write(EncodeFrame(previous, cursor))
		texts := []string{"a", "界", "é", "🙂", " ", "👩‍💻"}
		for i, value := range data[:min(len(data), 256)] {
			next := NewSurface(16, 3, Style{})
			copy(next.Cells, previous.Cells)
			text := texts[int(value)%len(texts)]
			next.Set(int(value)%16, i%3, Cell{Text: text, Width: uint8(textWidth(text)), Style: Style{Bold: value&1 != 0, UnderlineStyle: value % 6, Foreground: Color{G: value}}})
			_ = a.Write(EncodeFrame(next, cursor))
			_ = b.Write(EncodeDiff(previous, next, cursor, cursor))
			compareOracle(t, a, b)
			previous = next
		}
	})
}

func TestSparseDiffReducesBytesByNinetyPercent(t *testing.T) {
	previous := NewSurface(200, 60, Style{})
	next := NewSurface(200, 60, Style{})
	next.Text(70, 20, 4, "test", Style{})
	full, diff := EncodeFrame(next, Cursor{}), EncodeDiff(previous, next, Cursor{}, Cursor{})
	if len(diff)*10 >= len(full) {
		t.Fatalf("diff=%d full=%d", len(diff), len(full))
	}
	if result := EncodeDiff(next, next, Cursor{}, Cursor{}); len(result) != 0 {
		t.Fatalf("unchanged output=%q", result)
	}
	if result := EncodeDiff(previous, NewSurface(1, 1, Style{}), Cursor{}, Cursor{}); !bytes.Equal(result, EncodeFrame(NewSurface(1, 1, Style{}), Cursor{})) {
		t.Fatal("resize did not force full frame")
	}
}

func TestWriterDiffUsesLastWrittenFrameAndPreservesControls(t *testing.T) {
	out := &gatedFrameOutput{started: make(chan struct{}), release: make(chan struct{}), writes: make(chan []byte, 8)}
	writer := newLatestFrameWriter(out)
	base := NewSurface(80, 3, Style{})
	discarded := NewSurface(80, 3, Style{})
	latest := NewSurface(80, 3, Style{})
	discarded.Text(2, 1, 1, "x", Style{})
	latest.Text(10, 1, 1, "y", Style{})
	if err := writer.SubmitSurface(base, Cursor{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-out.started:
	case <-time.After(time.Second):
		t.Fatal("write did not start")
	}
	_ = writer.SubmitSurface(discarded, Cursor{})
	_ = writer.SubmitControl([]byte("\x1b]52;c;YQ==\a"))
	_ = writer.SubmitControl([]byte("\x1b]52;c;Yg==\a"))
	_ = writer.SubmitSurface(latest, Cursor{})
	close(out.release)
	a, b := oraclePair(t, 80, 3)
	first := <-out.writes
	_ = b.Write(first)
	_ = a.Write(EncodeFrame(latest, Cursor{}))
	var second []byte
	select {
	case second = <-out.writes:
	case <-time.After(time.Second):
		t.Fatal("missing latest frame")
	}
	if !bytes.Contains(second, []byte("YQ==\a\x1b]52;c;Yg==")) {
		t.Fatalf("control order lost: %q", second)
	}
	_ = b.Write(second)
	compareOracle(t, a, b)
	if err := writer.Close(nil); err != nil {
		t.Fatal(err)
	}
}

func TestSurfaceGraphemesAndWideOverwrite(t *testing.T) {
	s := NewSurface(8, 1, Style{})
	s.Text(0, 0, 8, "é界👩‍💻", Style{})
	if s.At(0, 0).Text != "é" || s.At(1, 0).Width != 2 || s.At(2, 0).Width != 0 || s.At(3, 0).Width != 2 || s.At(4, 0).Width != 0 {
		t.Fatalf("graphemes=%+v", s.Cells)
	}
	s.Set(2, 0, Cell{Text: "界", Width: 2})
	s.Set(7, 0, Cell{Text: "界", Width: 2})
	for x, c := range s.Cells {
		if c.Width == 0 && (x == 0 || s.At(x-1, 0).Width != 2) {
			t.Fatalf("orphan tail at %d", x)
		}
	}
	if got := fitText("é界👩‍💻", 4); got != "é界…" {
		t.Fatalf("fit=%q", got)
	}
}
