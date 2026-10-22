package direct

import (
	"errors"
	"strings"
	"testing"
)

func FuzzSSEFraming(f *testing.F) {
	for _, seed := range []string{"data: {}\n\n", ": comment\r\nevent: delta\r\ndata: {\r\ndata: }\r\n\r\n", "data: 工具结果\n\n", "data: [DONE]\n\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, wire string) {
		if len(wire) > 65536 {
			t.Skip()
		}
		calls := 0
		err := scanSSE(strings.NewReader(wire), func(_, data string) error {
			calls++
			if len(data) > 1<<20 {
				t.Fatal("event exceeded enforced bound")
			}
			if calls > 1024 {
				return errStreamDone
			}
			return nil
		})
		if err != nil && !errors.Is(err, errStreamDone) && !strings.Contains(err.Error(), "provider") {
			t.Fatalf("unexpected framing error: %v", err)
		}
	})
}
