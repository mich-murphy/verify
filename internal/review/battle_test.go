package review

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestBattlePacketExactByteBoundary(t *testing.T) {
	prefix, err := Build(Input{})
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range []int{-1, 0, 1} {
		in := Input{Diff: strings.Repeat("x", MaxPacket-len(prefix)+extra)}
		got, err := Build(in)
		if extra > 0 {
			if !errors.Is(err, ErrTooLarge) || got != nil {
				t.Fatalf("oversized packet escaped: len=%d err=%v", len(got), err)
			}
		} else if err != nil || len(got) != MaxPacket+extra || !bytes.HasSuffix(got, []byte(in.Diff)) {
			t.Fatalf("boundary %d: len=%d err=%v", extra, len(got), err)
		}
	}
}

func TestBattleCorrectnessReproIsOnlyBlockingContract(t *testing.T) {
	for _, kind := range Kinds {
		for _, repro := range []string{"null", `{"input":"x","expected":"y"}`} {
			raw := fmt.Sprintf(`{"findings":[{"kind":%q,"path":"a.go","line":0,"claim":"claim","repro":%s}],"resolved":[]}`, kind, repro)
			resp, err := Parse([]byte(raw), nil, []string{"a.go"})
			if err != nil {
				t.Fatal(err)
			}
			want := kind == "correctness" && repro != "null"
			if got := resp.Findings[0].Blocking(); got != want {
				t.Fatalf("kind=%s repro=%s blocking=%t, want %t", kind, repro, got, want)
			}
		}
	}
}

func BenchmarkBattlePacketBuild(b *testing.B) {
	for _, size := range []int{1024, 128 << 10, 250 << 10} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			in := Input{Diff: strings.Repeat("x", size), Messages: []string{"intent"}}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				if _, err := Build(in); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
