package shell

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsDisconnectedError(t *testing.T) {
	sentinel := errors.New("wsconn: control-plane socket is disconnected")
	other := errors.New("boom")

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"exact sentinel", sentinel, true},
		{"wrapped sentinel", fmt.Errorf("Logout: %w", sentinel), true},
		{"unrelated error", other, false},
		{"wrapped unrelated error", fmt.Errorf("Logout: %w", other), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsDisconnectedError(c.err, sentinel); got != c.want {
				t.Errorf("IsDisconnectedError(%v, sentinel) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestIsDisconnectedErrorNilSentinel(t *testing.T) {
	if IsDisconnectedError(errors.New("x"), nil) {
		t.Error("IsDisconnectedError with a nil sentinel must never report true")
	}
}
