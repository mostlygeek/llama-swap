package proxy

import (
	"strings"
	"testing"
)

func TestSwapProgressWriterFiltersLines(t *testing.T) {
	var lines []string
	writer := newSwapProgressWriter(func(line string) {
		lines = append(lines, line)
	}, nil)

	input := "hello world\nload_tensors: loading model tensors\npartial"
	if _, err := writer.Write([]byte(input)); err != nil {
		t.Fatalf("unexpected error writing: %v", err)
	}

	// flush remaining partial line and ensure it is ignored
	writer.Flush()

	expected := []string{"load_tensors: loading model tensors"}
	if len(lines) != len(expected) {
		t.Fatalf("unexpected number of lines. got %d want %d", len(lines), len(expected))
	}
	for i, line := range lines {
		if line != expected[i] {
			t.Fatalf("unexpected line at %d. got %q want %q", i, line, expected[i])
		}
	}
}

func TestSwapProgressWriterPreservesSpacing(t *testing.T) {
	var captured string
	writer := newSwapProgressWriter(func(line string) {
		captured = line
	}, nil)

	sample := "load_tensors:        CUDA0 model buffer size = 17917.21 MiB\n"
	if _, err := writer.Write([]byte(sample)); err != nil {
		t.Fatalf("unexpected error writing: %v", err)
	}

	expected := "load_tensors:        CUDA0 model buffer size = 17917.21 MiB"
	if captured != expected {
		t.Fatalf("unexpected captured line. got %q want %q", captured, expected)
	}
}

func TestSwapProgressWriterStreamsDotsIndividually(t *testing.T) {
	var dots []string
	writer := newSwapProgressWriter(nil, func(dot string) {
		dots = append(dots, dot)
	})

	sample := "............"
	if _, err := writer.Write([]byte(sample)); err != nil {
		t.Fatalf("unexpected error writing dots: %v", err)
	}

	expected := len(sample)
	if len(dots) != expected {
		t.Fatalf("unexpected number of dots. got %d want %d", len(dots), expected)
	}
	for i, dot := range dots {
		if dot != "." {
			t.Fatalf("dot at index %d was %q", i, dot)
		}
	}
}

func TestSwapProgressWriterStreamsDotLinesWithNewline(t *testing.T) {
	var dots []string
	writer := newSwapProgressWriter(nil, func(dot string) {
		dots = append(dots, dot)
	})

	sample := "............\n"
	if _, err := writer.Write([]byte(sample)); err != nil {
		t.Fatalf("unexpected error writing dots: %v", err)
	}

	expected := len(strings.TrimSuffix(sample, "\n"))
	if len(dots) != expected {
		t.Fatalf("unexpected number of dots. got %d want %d", len(dots), expected)
	}
}
