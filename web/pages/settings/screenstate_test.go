package settings

import (
	"errors"
	"testing"
)

func TestComputeScreenState(t *testing.T) {
	errBoom := errors.New("boom")
	cases := []struct {
		name string
		in   ScreenInputs
		want ScreenState
	}{
		{"disabled wins over everything", ScreenInputs{DisabledReason: "kill switch", Err: errBoom, Loading: true, Disconnected: true}, ScreenDisabledWithReason},
		{"disconnected with no data", ScreenInputs{Disconnected: true, ItemCount: 0}, ScreenDisconnected},
		{"disconnected but has data stays populated", ScreenInputs{Disconnected: true, ItemCount: 3}, ScreenPopulated},
		{"error", ScreenInputs{Err: errBoom}, ScreenError},
		{"loading", ScreenInputs{Loading: true}, ScreenLoading},
		{"empty", ScreenInputs{ItemCount: 0}, ScreenEmpty},
		{"populated", ScreenInputs{ItemCount: 5}, ScreenPopulated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ComputeScreenState(c.in); got != c.want {
				t.Errorf("ComputeScreenState(%+v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
